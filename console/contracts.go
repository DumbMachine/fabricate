package console

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dumbmachine/fabricate/scenario"
)

// Contract discovery exposes the actual compiled surface, never a second spec.
func (m *Manager) handleContract(w http.ResponseWriter, r *http.Request) {
	resource, ok := m.registry.Get(r.PathValue("id"))
	if !ok {
		writeError(w, 404, fmt.Errorf("unknown resource"))
		return
	}
	contract := resource.Contract()
	var spec map[string]any
	if err := json.Unmarshal(contract.OpenAPIJSON, &spec); err != nil {
		writeError(w, 500, err)
		return
	}
	basePath := ""
	if servers, ok := spec["servers"].([]any); ok && len(servers) > 0 {
		if server, ok := servers[0].(map[string]any); ok {
			if raw, ok := server["url"].(string); ok {
				if u, err := url.Parse(raw); err == nil {
					basePath = strings.TrimRight(u.Path, "/")
				}
			}
		}
	}
	if filter := r.URL.Query().Get("operations"); filter != "" {
		wanted := map[string]bool{}
		for _, id := range strings.Split(filter, ",") {
			wanted[id] = false
		}
		paths, _ := spec["paths"].(map[string]any)
		selected := map[string]any{}
		for path, raw := range paths {
			item, _ := raw.(map[string]any)
			next := map[string]any{}
			for method, value := range item {
				operation, ok := value.(map[string]any)
				if !ok {
					continue
				}
				id, _ := operation["operationId"].(string)
				if _, keep := wanted[id]; keep && id != "" {
					next[method] = value
					wanted[id] = true
				}
			}
			if len(next) > 0 {
				if params, ok := item["parameters"]; ok {
					next["parameters"] = params
				}
				selected[path] = next
			}
		}
		for id, found := range wanted {
			if !found {
				writeError(w, 422, fmt.Errorf("unsupported operation %q", id))
				return
			}
		}
		spec["paths"] = selected
	}
	writeJSON(w, 200, map[string]any{"resource": resource.Descriptor(), "basePath": basePath, "openapi": spec, "stateSchema": json.RawMessage(contract.ScenarioJSON)})
}

func (m *Manager) handleDocument(w http.ResponseWriter, r *http.Request) {
	resourceID, _, _ := strings.Cut(r.PathValue("id"), ".")
	resource, ok := m.registry.Get(resourceID)
	if !ok {
		writeError(w, 404, fmt.Errorf("unknown resource"))
		return
	}
	doc, err := resource.Scenario(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	raw, err := scenario.CanonicalJSON(doc)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"document": doc, "digest": scenario.Digest(raw)})
}

func (m *Manager) handleValidateState(w http.ResponseWriter, r *http.Request) {
	resource, ok := m.registry.Get(r.PathValue("id"))
	if !ok {
		writeError(w, 404, fmt.Errorf("unknown resource"))
		return
	}
	raw := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	var doc scenario.Document
	raw.DisallowUnknownFields()
	if err := raw.Decode(&doc); err != nil {
		writeError(w, 400, fmt.Errorf("invalid scenario document"))
		return
	}
	if err := raw.Decode(new(any)); err != io.EOF {
		writeError(w, 400, fmt.Errorf("one scenario document required"))
		return
	}
	if doc.Resource != resource.Descriptor().ID {
		writeError(w, 422, fmt.Errorf("resource identity mismatch"))
		return
	}
	if err := resource.Scenarios().Validate(r.Context(), doc); err != nil {
		writeError(w, 422, err)
		return
	}
	canonical, err := scenario.CanonicalJSON(doc)
	if err != nil {
		writeError(w, 422, err)
		return
	}
	writeJSON(w, 200, map[string]any{"valid": true, "digest": scenario.Digest(canonical)})
}

func (m *Manager) handleState(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[r.PathValue("id")]
	if !ok {
		writeError(w, 404, fmt.Errorf("sandbox not running"))
		return
	}
	documents := map[string]scenario.Document{}
	for name, service := range session.runtime.Services {
		spec := session.spec.Services[name]
		baseline, err := service.Resource.Scenario(spec.Scenario)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		doc, err := service.Resource.Scenarios().Dump(r.Context(), service.State.DB(), baseline.Metadata())
		if err != nil {
			writeError(w, 500, err)
			return
		}
		documents[name] = doc
	}
	writeJSON(w, 200, map[string]any{"id": session.id, "services": documents})
}
