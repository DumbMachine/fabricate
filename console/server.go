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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog", m.handleCatalog)
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("POST /api/sessions", m.handleStart)
	mux.HandleFunc("GET /api/sessions/{id}", m.handleGet)
	mux.HandleFunc("DELETE /api/sessions/{id}", m.handleDelete)
	mux.HandleFunc("/s/", m.proxyService)
	mux.Handle("/", spaHandler(uiSub()))
	return mux
}

func (m *Manager) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	catalog, err := m.Catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (m *Manager) handleList(w http.ResponseWriter, r *http.Request) {
	sessions := m.List()
	for i := range sessions {
		sessions[i] = present(sessions[i], r)
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (m *Manager) handleStart(w http.ResponseWriter, r *http.Request) {
	var request StartRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid start request"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid start request"))
		return
	}
	session, err := m.Start(r.Context(), request)
	if err != nil {
		status := http.StatusBadRequest
		var unknownEnv unknownEnvironmentError
		var unknownScenario unknownScenarioError
		if !errors.As(err, &unknownEnv) && !errors.As(err, &unknownScenario) && !isClientStartError(err) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, present(session, r))
}

func isClientStartError(err error) bool {
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
	writeJSON(w, http.StatusOK, present(session, r))
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

func present(session Session, r *http.Request) Session {
	base := requestBase(r)
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
	session.ConnectShell = connectShell(session)
	session.AgentPrompt = agentPrompt(session)
	return session
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
