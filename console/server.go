package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

func (m *Manager) Handler() http.Handler {
	routes := m.controlRoutes()
	mux := http.NewServeMux()
	mux.Handle("/mcp", m.mcpHandler(routes))
	mux.Handle("/", routes)
	return m.consoleAuth(mux)
}
func (m *Manager) controlRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/fabricate", m.handleAgentIndex)
	mux.HandleFunc("GET /api/resources/{id}/contract", m.handleContract)
	mux.HandleFunc("GET /api/scenarios/{id}/document", m.handleDocument)
	mux.HandleFunc("POST /api/resources/{id}/validate", m.handleValidateState)
	mux.HandleFunc("GET /api/sessions/{id}/state", m.handleState)
	mux.HandleFunc("GET /api/config", m.handleConfig)
	mux.HandleFunc("GET /api/catalog", m.handleCatalog)
	mux.HandleFunc("GET /api/scenarios/{id}", m.handleScenario)
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("POST /api/sessions", m.handleStart)
	mux.HandleFunc("POST /api/sessions/{id}/renew", m.handleRenew)
	mux.HandleFunc("POST /api/sessions/{id}/services/{service}/request", m.handleServiceRequest)
	mux.HandleFunc("GET /api/requests/{key}", m.handleRequestStatus)
	mux.HandleFunc("DELETE /api/requests/{key}", m.handleRequestCancel)
	mux.HandleFunc("GET /api/projects", m.handleProjects)
	mux.HandleFunc("POST /api/projects", m.handleProjects)
	mux.HandleFunc("PATCH /api/projects/{project}", m.handleProjectUpdate)
	mux.HandleFunc("GET /api/projects/{project}/tokens", m.handleProjectTokens)
	mux.HandleFunc("POST /api/projects/{project}/tokens", m.handleProjectTokens)
	mux.HandleFunc("DELETE /api/projects/{project}/tokens/{token}", m.handleProjectTokens)
	mux.HandleFunc("GET /api/sessions/{id}", m.handleGet)
	mux.HandleFunc("GET /api/sessions/{id}/logs", m.handleLogs)
	mux.HandleFunc("DELETE /api/sessions/{id}", m.handleDelete)
	mux.HandleFunc("/s/", m.proxyService)
	mux.Handle("/api/", http.NotFoundHandler())
	mux.Handle("/", spaHandler(uiSub()))
	return m.scopeControl(mux)
}

func (m *Manager) handleAgentIndex(w http.ResponseWriter, r *http.Request) {
	catalog, err := m.Catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessions := m.ListFor(r.Context())
	for i := range sessions {
		sessions[i] = m.present(sessions[i], r)
	}
	writeJSON(w, http.StatusOK, buildAgentIndex(m.Deployment(), catalog, sessions, joinIndexURL(m.publicBase(r))))
}

func (m *Manager) handleConfig(w http.ResponseWriter, r *http.Request) {
	p := actor(r.Context())
	project, err := m.project(r.Context(), p.Project)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, 200, struct {
		Deployment
		Operator bool    `json:"operator"`
		Project  Project `json:"project"`
	}{m.Deployment(), p.Admin, project})
}

func (m *Manager) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	catalog, err := m.Catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (m *Manager) handleScenario(w http.ResponseWriter, r *http.Request) {
	view, err := m.Scenario(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		var unknown unknownScenarioError
		if errors.As(err, &unknown) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (m *Manager) handleList(w http.ResponseWriter, r *http.Request) {
	sessions := m.ListFor(r.Context())
	for i := range sessions {
		sessions[i] = m.present(sessions[i], r)
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (m *Manager) handleStart(w http.ResponseWriter, r *http.Request) {
	var request StartRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid start request"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid start request"))
		return
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		if request.IdempotencyKey != "" && request.IdempotencyKey != key {
			writeError(w, 400, fmt.Errorf("conflicting idempotency keys"))
			return
		}
		request.IdempotencyKey = key
	}
	session, err := m.Start(r.Context(), request)
	if err != nil {
		status := http.StatusBadRequest
		var unknownEnv unknownEnvironmentError
		var unknownScenario unknownScenarioError
		if !errors.As(err, &unknownEnv) && !errors.As(err, &unknownScenario) && !isClientStartError(err) {
			status = http.StatusInternalServerError
		}
		var coded apiError
		if errors.As(err, &coded) {
			status = coded.Status
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, m.present(session, r))
}

func isClientStartError(err error) bool {
	var invalid invalidStartError
	if errors.As(err, &invalid) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "kind must be") ||
		strings.Contains(message, "id is required") ||
		strings.Contains(message, "too many running sandboxes")
}

func (m *Manager) handleGet(w http.ResponseWriter, r *http.Request) {
	session, ok := m.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("sandbox not running"))
		return
	}
	writeJSON(w, http.StatusOK, m.present(session, r))
}

func (m *Manager) handleLogs(w http.ResponseWriter, r *http.Request) {
	session, ok := m.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("sandbox not running"))
		return
	}
	entries, truncated, err := readSessionLog(session.RequestLog, logEntryLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, RequestLogView{ID: session.ID, Entries: entries, Truncated: truncated})
}

func (m *Manager) proxyService(w http.ResponseWriter, r *http.Request) {
	id, name, rest, ok := splitServicePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, ok := m.serviceURL(id, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	upstream, err := url.Parse(target)
	if err != nil || upstream.Host == "" {
		http.Error(w, "sandbox service unavailable", http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.URL.Path = rest
			request.Out.URL.RawPath = ""
			request.Out.Host = upstream.Host
			// The operator session belongs only to the control plane.
			cookies := request.Out.Cookies()
			request.Out.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != consoleCookie {
					request.Out.AddCookie(cookie)
				}
			}
		},
		ModifyResponse: func(response *http.Response) error {
			// Provider responses are untrusted API data, not console pages.
			response.Header.Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
			response.Header.Set("X-Content-Type-Options", "nosniff")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "sandbox service unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func splitServicePath(raw string) (id, name, rest string, ok bool) {
	trimmed := strings.TrimPrefix(raw, "/s/")
	if trimmed == raw || trimmed == "" {
		return "", "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	rest = "/"
	if len(parts) == 3 {
		rest = "/" + parts[2]
	}
	return parts[0], parts[1], rest, true
}

func (m *Manager) present(session Session, r *http.Request) Session {
	base := m.publicBase(r)
	for i, service := range session.Services {
		if service.Name == "" {
			continue
		}
		public := strings.TrimRight(base, "/") + "/s/" + session.ID + "/" + service.Name
		for j, variable := range session.Variables {
			if variable.Name == service.URLVariable {
				session.Variables[j].Value = public
			}
		}
		session.Services[i].URL = public
	}
	session.Logs = strings.TrimRight(base, "/") + "/api/sessions/" + session.ID + "/logs"
	session.ConnectShell = connectShell(session)
	session.AgentPrompt = agentPrompt(session) + "\nFor lifecycle control, configure the Streamable HTTP MCP endpoint " + strings.TrimRight(base, "/") + "/mcp with a project control credential. This prompt does not include a control credential.\n"
	return session
}

func (m *Manager) publicBase(r *http.Request) string {
	if configured := m.configuredBase(); configured != "" {
		return configured
	}
	return requestBase(r)
}

func requestBase(r *http.Request) string {
	proto := firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if proto == "" {
		if r.TLS != nil || publicHostname(host) {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + host
}

func firstHeaderValue(value string) string {
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func publicHostname(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	return net.ParseIP(host) == nil
}

func (m *Manager) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := m.Close(r.PathValue("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errAnchored) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "/" || clean == "/index.html" {
			serveIndex(w, r, files)
			return
		}
		info, err := fs.Stat(fsys, strings.TrimPrefix(clean, "/"))
		if err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, files)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, files http.Handler) {
	w.Header().Set("Cache-Control", "no-cache")
	clone := r.Clone(r.Context())
	clone.URL.Path = "/"
	files.ServeHTTP(w, clone)
}
