package specserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/dumbmachine/fabricate/httpresource"
)

type Server struct {
	idx     *Index
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	token   string
	handler http.Handler
}

func New(ctx context.Context, spec *CompiledSpec, dependencies httpresource.ServerDependencies) (*Server, error) {
	if spec == nil || spec.Index == nil {
		return nil, fmt.Errorf("specserver: compiled OpenAPI document is required")
	}
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("specserver: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("specserver: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("specserver: synthetic token is empty")
	}
	impl := &Server{
		idx:   spec.Index,
		db:    dependencies.DB,
		clock: dependencies.Clock,
		ids:   dependencies.IDs,
		token: token,
	}
	impl.handler = http.HandlerFunc(impl.serve)
	return impl, nil
}

func (s *Server) Handler() http.Handler       { return s.handler }
func (s *Server) Close(context.Context) error { return nil }

func (s *Server) serve(w http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "unauthorized"})
		return
	}
	rt, params, ok := s.idx.find(request.Method, request.URL.Path)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "not found"})
		return
	}
	if params == nil {
		params = map[string]string{}
	}
	body, err := readObject(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid JSON body"})
		return
	}
	body = unwrapBody(body, rt.wrap)
	for key, value := range params {
		if _, exists := body[key]; !exists {
			body[key] = value
		}
	}
	ctx := request.Context()
	list := looksLikeList(rt.wrap, rt.item) && request.Method == http.MethodGet && !rt.item
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		if rt.item {
			id := itemIDFromParams(rt.template, params)
			record, err := getDocument(ctx, s.db, rt.collection, id)
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, wrapError(rt.wrap, "not found"))
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
				return
			}
			if request.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			writeJSON(w, http.StatusOK, wrapValue(rt.wrap, record, false))
			return
		}
		records, err := listDocuments(ctx, s.db, rt.collection, params)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if list {
			writeJSON(w, http.StatusOK, wrapValue(rt.wrap, records, true))
			return
		}
		if len(records) > 0 {
			writeJSON(w, http.StatusOK, wrapValue(rt.wrap, records[0], false))
			return
		}
		writeJSON(w, http.StatusOK, wrapValue(rt.wrap, map[string]any{}, false))
	case http.MethodPost:
		id, err := s.ids.Next(ctx, rt.collection)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		if existing, ok := body["id"]; ok && fmt.Sprint(existing) != "" && fmt.Sprint(existing) != "<nil>" {
			id = fmt.Sprint(existing)
		} else {
			body["id"] = id
		}
		if err := upsertDocument(ctx, s.db, rt.collection, id, body); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		status := http.StatusOK
		if rt.created {
			status = http.StatusCreated
		}
		writeJSON(w, status, wrapValue(rt.wrap, body, false))
	case http.MethodPut, http.MethodPatch:
		id := itemIDFromParams(rt.template, params)
		if id == "" {
			id = fmt.Sprint(body["id"])
		}
		if id == "" || id == "<nil>" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "id is required"})
			return
		}
		existing, err := getDocument(ctx, s.db, rt.collection, id)
		if err != nil && err != sql.ErrNoRows {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		if existing == nil {
			existing = map[string]any{}
		}
		for key, value := range body {
			existing[key] = value
		}
		existing["id"] = id
		if err := upsertDocument(ctx, s.db, rt.collection, id, existing); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, wrapValue(rt.wrap, existing, false))
	case http.MethodDelete:
		id := itemIDFromParams(rt.template, params)
		if err := deleteDocument(ctx, s.db, rt.collection, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodOptions:
		w.Header().Set("Allow", rt.method)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
	}
}

func (s *Server) authorized(request *http.Request) bool {
	header := request.Header.Get("Authorization")
	switch {
	case header == "Bearer "+s.token, header == "token "+s.token, header == "Token "+s.token, header == s.token:
		return true
	}
	for _, key := range []string{"X-Api-Key", "X-API-Key", "x-api-key", "X-Figma-Token", "X-Auth-Key", "X-Exa-Api-Key"} {
		if request.Header.Get(key) == s.token {
			return true
		}
	}
	if request.URL.Query().Get("api_key") == s.token || request.URL.Query().Get("key") == s.token {
		return true
	}
	return false
}

func readObject(request *http.Request) (map[string]any, error) {
	fields := map[string]any{}
	if request.Body == nil || request.Method == http.MethodGet || request.Method == http.MethodDelete || request.Method == http.MethodHead || request.Method == http.MethodOptions {
		return fields, nil
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	_ = request.Body.Close()
	if len(bytes.TrimSpace(raw)) == 0 {
		return fields, nil
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
