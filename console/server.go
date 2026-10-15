package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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

func (m *Manager) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.List())
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
	writeJSON(w, http.StatusCreated, session)
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
	writeJSON(w, http.StatusOK, session)
}

func (m *Manager) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := m.Close(r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
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
