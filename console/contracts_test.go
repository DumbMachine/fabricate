package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestHTTPCompositionContractsAndIsolation(t *testing.T) {
	m := New(all.Registry())
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	defer m.Shutdown()
	// Contracts come from the compiled resource; unsupported capabilities fail.
	contract := get(t, server.URL+"/api/resources/asana/contract?operations=GetTask,UpdateTask")
	if contract.StatusCode != 200 {
		t.Fatal(readBody(t, contract))
	}
	var view struct {
		BasePath string         `json:"basePath"`
		OpenAPI  map[string]any `json:"openapi"`
	}
	decodeJSON(t, contract, &view)
	if view.BasePath != "/api/1.0" {
		t.Fatalf("base path: %q", view.BasePath)
	}
	absent := get(t, server.URL+"/api/resources/asana/contract?operations=unsupported")
	if absent.StatusCode != 422 {
		t.Fatal(readBody(t, absent))
	}
	absent.Body.Close()
	baseline := get(t, server.URL+"/api/scenarios/asana.acme-sprint.v1/document")
	var original struct {
		Document scenario.Document `json:"document"`
		Digest   string            `json:"digest"`
	}
	decodeJSON(t, baseline, &original)
	if !strings.HasPrefix(original.Digest, "sha256:") {
		t.Fatal("missing baseline digest")
	}
	state := map[string]any{}
	if err := json.Unmarshal(original.Document.State, &state); err != nil {
		t.Fatal(err)
	}
	tasks := state["tasks"].([]any)
	tasks[0].(map[string]any)["notes"] = "custom baseline"
	custom, _ := json.Marshal(state)
	created, err := m.Start(context.Background(), StartRequest{Kind: "composition", ID: "independent-tasks", Services: map[string]ServiceRequest{
		"east": {Resource: "asana", Scenario: original.Document.ID, State: custom},
		"west": {Resource: "asana", Scenario: original.Document.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Services) != 2 || created.Services[0].Token == created.Services[1].Token || created.Services[0].URL == created.Services[1].URL {
		t.Fatal("services share identity")
	}
	// Mutate one provider through the public forwarding route, then export both.
	east := created.Services[0]
	gid := tasks[0].(map[string]any)["gid"].(string)
	request, _ := http.NewRequest("PUT", server.URL+"/s/"+created.ID+"/east/api/1.0/tasks/"+gid, strings.NewReader(`{"data":{"notes":"changed once"}}`))
	request.Header.Set("Authorization", "Bearer "+east.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(readBody(t, response))
	}
	response.Body.Close()
	exported := get(t, server.URL+"/api/sessions/"+created.ID+"/state")
	var snapshot struct {
		Services map[string]scenario.Document `json:"services"`
	}
	decodeJSON(t, exported, &snapshot)
	if !strings.Contains(string(snapshot.Services["east"].State), "changed once") {
		t.Fatal("mutation missing")
	}
	if strings.Contains(string(snapshot.Services["west"].State), "changed once") {
		t.Fatal("mutation leaked into other instance")
	}
	// Credentials from one instance cannot authorize another.
	request, _ = http.NewRequest("GET", server.URL+"/s/"+created.ID+"/west/api/1.0/tasks/"+gid, nil)
	request.Header.Set("Authorization", "Bearer "+east.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 400 {
		t.Fatal("cross-instance credential accepted")
	}
	response.Body.Close()
	before := len(m.List())
	_, err = m.Start(context.Background(), StartRequest{Kind: "composition", ID: "invalid", Services: map[string]ServiceRequest{"one": {Resource: "asana", Scenario: original.Document.ID}, "two": {Resource: "unsupported", Scenario: "unsupported.minimal.v1"}}})
	if err == nil || len(m.List()) != before {
		t.Fatal("invalid composition partially published")
	}
}

func TestControlCredentialAndCustomStateValidation(t *testing.T) {
	t.Setenv("FAB_CONSOLE_TOKEN", "operator-token")
	m := New(all.Registry())
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	defer m.Shutdown()
	response := get(t, server.URL+"/.well-known/fabricate")
	if response.StatusCode != 401 {
		t.Fatal("unprotected control plane")
	}
	response.Body.Close()
	request, _ := http.NewRequest("GET", server.URL+"/.well-known/fabricate", nil)
	request.Header.Set("Authorization", "Bearer operator-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(readBody(t, response))
	}
	response.Body.Close()
	for _, body := range []string{
		`{"kind":"composition","id":"bad","services":{"tasks":{"resource":"asana","scenario":"asana.acme-sprint.v1","state":{}}}}`,
		`{"kind":"composition","id":"bad","services":{"tasks":{"resource":"asana","scenario":"asana.acme-sprint.v1","scenario_file":"/etc/passwd"}}}`,
	} {
		request, _ = http.NewRequest("POST", server.URL+"/api/sessions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer operator-token")
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 400 {
			t.Fatalf("invalid state status: %d", response.StatusCode)
		}
		response.Body.Close()
	}
	if len(m.List()) != 0 {
		t.Fatal("invalid state allocated session")
	}
}
