package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testOperator = "test-mcp-operator-key"

func controlJSON(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://console.example"+path, strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func testProjectToken(t *testing.T, h http.Handler, id string, max int) (string, string) {
	t.Helper()
	w := controlJSON(t, h, testOperator, "POST", "/api/projects", Project{ID: id, MaxSandboxes: max, DefaultTTLSeconds: 600, MaxTTLSeconds: 3600})
	if w.Code != 200 {
		t.Fatalf("project create HTTP %d", w.Code)
	}
	w = controlJSON(t, h, testOperator, "POST", "/api/projects/"+id+"/tokens", projectTokenRequest{Name: "test-agent", TTLSeconds: 3600})
	if w.Code != 201 {
		t.Fatalf("token create HTTP %d", w.Code)
	}
	var out struct {
		Token      string       `json:"token"`
		Credential ProjectToken `json:"credential"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token, out.Credential.ID
}

type bearerTransport struct{ token string }

func (tr bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tr.token)
	return http.DefaultTransport.RoundTrip(r)
}
func sdkSession(t *testing.T, base, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "fabricate-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func sdkCall(t *testing.T, session *mcp.ClientSession, name string, args any, fail bool) json.RawMessage {
	t.Helper()
	out, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError != fail {
		t.Fatalf("tool %s isError=%v, wanted %v; %v", name, out.IsError, fail, out.Content)
	}
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if !fail && json.Unmarshal(raw, &wrapper) != nil {
		t.Fatal("missing structured output")
	}
	return wrapper.Data
}
func TestMCPRealClientAndProjectBoundary(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	h := m.Handler()
	alpha, alphaTokenID := testProjectToken(t, h, "alpha", 2)
	beta, _ := testProjectToken(t, h, "beta", 2)
	server := httptest.NewServer(h)
	defer server.Close()
	client := sdkSession(t, server.URL, alpha)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil {
			t.Fatal("tool missing input schema")
		}
	}
	for _, name := range []string{"catalog_list", "resource_contract", "scenario_document", "scenario_validate", "sandbox_create", "sandbox_request_status", "sandbox_list", "sandbox_get", "sandbox_renew", "sandbox_close", "sandbox_logs", "sandbox_state", "service_read", "service_request"} {
		if !names[name] {
			t.Fatal("missing tool", name)
		}
	}
	if names["project_token_create"] {
		t.Fatal("project credential was offered operator tools")
	}
	sdkCall(t, client, "sandbox_create", map[string]any{"kind": "composition", "id": "invalid-custom", "idempotencyKey": "invalid-custom-1", "services": map[string]any{"one": map[string]any{"resource": "asana", "scenario": "asana.acme-sprint.v1", "state": map[string]any{}}}}, true)
	args := map[string]any{"kind": "composition", "id": "agent-workspace", "idempotencyKey": "agent-creation-1", "services": map[string]any{"east": map[string]string{"resource": "asana", "scenario": "asana.acme-sprint.v1"}, "west": map[string]string{"resource": "asana", "scenario": "asana.acme-sprint.v1"}}}
	raw := sdkCall(t, client, "sandbox_create", args, false)
	var session Session
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	if session.Project != "alpha" || len(session.Services) != 2 || session.ExpiresAt == nil {
		t.Fatal("creation omitted ownership/TTL")
	}
	var same Session
	json.Unmarshal(sdkCall(t, client, "sandbox_create", args, false), &same)
	if same.ID != session.ID {
		t.Fatal("idempotent creation duplicated")
	}
	sdkCall(t, client, "sandbox_request_status", map[string]string{"key": "agent-creation-1"}, false)
	sdkCall(t, client, "resource_contract", map[string]any{"resource": "asana", "operations": []string{"unsupported"}}, true)
	resource, _ := m.registry.Get("asana")
	doc, _ := resource.Scenario("asana.acme-sprint.v1")
	var state struct {
		Tasks []struct {
			GID string `json:"gid"`
		} `json:"tasks"`
	}
	json.Unmarshal(doc.State, &state)
	path := "/api/1.0/tasks/" + state.Tasks[0].GID
	sdkCall(t, client, "service_read", map[string]any{"id": session.ID, "service": "east", "path": path}, false)
	sdkCall(t, client, "service_request", map[string]any{"id": session.ID, "service": "east", "method": "PUT", "path": path, "body": map[string]any{"data": map[string]string{"notes": "MCP mutation"}}}, false)
	if !strings.Contains(string(sdkCall(t, client, "service_request", map[string]any{"id": session.ID, "service": "east", "method": "GET", "path": path}, false)), "MCP mutation") {
		t.Fatal("independent readback missing mutation")
	}
	if strings.Contains(string(sdkCall(t, client, "service_request", map[string]any{"id": session.ID, "service": "west", "method": "GET", "path": path}, false)), "MCP mutation") {
		t.Fatal("mutation crossed service boundary")
	}
	sdkCall(t, client, "service_request", map[string]any{"id": session.ID, "service": "east", "method": "GET", "path": "/unsupported"}, true)
	sdkCall(t, client, "service_request", map[string]any{"id": session.ID, "service": "east", "method": "GET", "path": "http://example.com/"}, true)
	other := sdkSession(t, server.URL, beta)
	for _, name := range []string{"sandbox_get", "sandbox_state", "sandbox_logs"} {
		sdkCall(t, other, name, map[string]string{"id": session.ID}, true)
	}
	if strings.Contains(string(sdkCall(t, other, "sandbox_list", map[string]any{}, false)), session.ID) {
		t.Fatal("list leaked another project")
	}
	sdkCall(t, other, "sandbox_close", map[string]string{"id": session.ID}, false)
	if _, ok := m.Get(session.ID); !ok {
		t.Fatal("cross-project close modified sandbox")
	}
	sdkCall(t, client, "sandbox_renew", map[string]any{"id": session.ID, "ttlSeconds": 1200}, false)
	sdkCall(t, client, "sandbox_state", map[string]string{"id": session.ID}, false)
	sdkCall(t, client, "sandbox_logs", map[string]string{"id": session.ID}, false)
	sdkCall(t, client, "sandbox_close", map[string]string{"id": session.ID}, false)
	sdkCall(t, client, "sandbox_create", args, true)
	if w := controlJSON(t, h, alpha, "GET", "/api/projects", nil); w.Code != 403 {
		t.Fatal("HTTP scope bypass", w.Code)
	}
	w := controlJSON(t, h, testOperator, "DELETE", "/api/projects/alpha/tokens/"+alphaTokenID, nil)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err = client.ListTools(context.Background(), nil); err == nil {
		t.Fatal("revoked credential remained usable")
	}
}

func TestMCPTransportProtocolBoundary(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	handler := m.Handler()
	call := func(method, body, token, origin, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://console.example/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-client","version":"1"}}}`
	accept := "application/json, text/event-stream"
	if w := call("POST", init, "", "", accept); w.Code != 401 {
		t.Fatal("unauthenticated MCP", w.Code)
	}
	if w := call("POST", init, testOperator, "https://untrusted.example", accept); w.Code != 403 {
		t.Fatal("origin not rejected", w.Code)
	}
	if w := call("GET", "", testOperator, "", accept); w.Code != 405 {
		t.Fatal("stateless GET", w.Code)
	}
	if w := call("POST", init, testOperator, "", ""); w.Code != 400 {
		t.Fatal("Accept not checked", w.Code)
	}
	w := call("POST", init, testOperator, "", accept)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"protocolVersion":"2025-11-25"`) {
		t.Fatal("legacy initialize failed", w.Code, w.Body.String())
	}
	w = call("POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, testOperator, "", accept)
	if w.Code != 202 {
		t.Fatal("notification acknowledgement", w.Code)
	}
	if w = call("POST", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, testOperator, "", accept); w.Code != 200 || !strings.Contains(w.Body.String(), "sandbox_create") {
		t.Fatal("legacy tools list", w.Code)
	}
}
