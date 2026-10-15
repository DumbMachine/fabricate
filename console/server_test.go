package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
)

func TestCatalogListsEnvironmentsAndScenarios(t *testing.T) {
	manager := New(all.Registry())
	catalog, err := manager.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var gmail *CatalogEnvironment
	for i := range catalog.Environments {
		if catalog.Environments[i].Name == "acme-gmail" {
			gmail = &catalog.Environments[i]
			break
		}
	}
	if gmail == nil || gmail.Label != "Acme Gmail" || len(gmail.Services) != 1 {
		t.Fatalf("acme-gmail catalog entry: %#v", gmail)
	}
	service := gmail.Services[0]
	if service.Name != "support-mail" || service.Resource != "gmail" || service.Scenario != "gmail.acme-corp.v1" || service.DisplayName != "Gmail" {
		t.Fatalf("support-mail service: %#v", service)
	}
	if !containsHost(service.ProviderHosts, "gmail.googleapis.com") {
		t.Fatalf("provider hosts: %#v", service.ProviderHosts)
	}
	found := map[string]bool{}
	for _, scenario := range catalog.Scenarios {
		if scenario.Resource == "gmail" {
			found[scenario.ID] = true
		}
	}
	if !found["gmail.minimal.v1"] || !found["gmail.acme-corp.v1"] {
		t.Fatalf("gmail scenarios: %#v", found)
	}
}

func TestServeStartsScenarioAndClosesIt(t *testing.T) {
	manager := New(all.Registry())
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(func() {
		server.Close()
		_ = manager.Shutdown()
	})

	response := postJSON(t, server.URL+"/api/sessions", `{"kind":"scenario","id":"gmail.minimal.v1"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("start status %d: %s", response.StatusCode, readBody(t, response))
	}
	var session Session
	decodeJSON(t, response, &session)
	if session.Kind != "scenario" || session.Status != "ready" || session.Name != "gmail" || session.Label != "Gmail" || session.Proxy {
		t.Fatalf("session: %#v", session)
	}
	if len(session.Services) != 1 || session.Services[0].URL == "" || session.Services[0].Token == "" {
		t.Fatalf("service: %#v", session.Services)
	}
	service := session.Services[0]
	if service.URLVariable != "FAB_GMAIL_URL" || service.TokenVariable != "FAB_GMAIL_TOKEN" {
		t.Fatalf("variables: %#v", service)
	}
	if !strings.Contains(session.ConnectShell, service.URL) || !strings.Contains(session.ConnectShell, service.Token) {
		t.Fatalf("connect shell missing connection: %s", session.ConnectShell)
	}
	for _, phrase := range []string{"Do not use production accounts", "Do not commit these credentials", service.URL, service.Token, "fab serve"} {
		if !strings.Contains(session.AgentPrompt, phrase) {
			t.Fatalf("agent prompt missing %q:\n%s", phrase, session.AgentPrompt)
		}
	}

	profile, err := http.NewRequest(http.MethodGet, service.URL+"/gmail/v1/users/me/profile", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.Header.Set("Authorization", "Bearer "+service.Token)
	profileResponse, err := http.DefaultClient.Do(profile)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, profileResponse)
	if profileResponse.StatusCode != http.StatusOK || !strings.Contains(body, "emailAddress") {
		t.Fatalf("profile %d: %s", profileResponse.StatusCode, body)
	}

	page := get(t, server.URL+"/sandboxes/"+session.ID)
	if page.StatusCode != http.StatusOK || !strings.Contains(readBody(t, page), "Fabricate") {
		t.Fatalf("console page status %d", page.StatusCode)
	}

	closed := do(t, http.MethodDelete, server.URL+"/api/sessions/"+session.ID, "")
	if closed.StatusCode != http.StatusNoContent {
		t.Fatalf("close status %d", closed.StatusCode)
	}
	again := do(t, http.MethodDelete, server.URL+"/api/sessions/"+session.ID, "")
	if again.StatusCode != http.StatusNoContent {
		t.Fatalf("second close status %d", again.StatusCode)
	}
	missing := get(t, server.URL+"/api/sessions/"+session.ID)
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("get after close %d", missing.StatusCode)
	}
	afterClose, err := http.Get(service.URL + "/gmail/v1/users/me/profile")
	if err != nil {
		t.Fatal(err)
	}
	if afterClose.StatusCode != http.StatusNotFound {
		t.Fatalf("service status after close %d", afterClose.StatusCode)
	}
	afterClose.Body.Close()
}

func TestServePublishesServicesOnTheRequestHost(t *testing.T) {
	manager := New(all.Registry())
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = manager.Shutdown() })

	created := postJSON(t, server.URL+"/api/sessions", `{"kind":"scenario","id":"gmail.minimal.v1"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("start status %d: %s", created.StatusCode, readBody(t, created))
	}
	var started Session
	decodeJSON(t, created, &started)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/sessions/"+started.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "p-4173-pod.example"
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get status %d: %s", response.StatusCode, readBody(t, response))
	}
	var session Session
	decodeJSON(t, response, &session)
	want := "https://p-4173-pod.example/s/" + session.ID + "/" + session.Services[0].Name
	if session.Services[0].URL != want {
		t.Fatalf("published url %q", session.Services[0].URL)
	}
	if !strings.Contains(session.ConnectShell, want) || !strings.Contains(session.AgentPrompt, "console host") {
		t.Fatalf("instructions missing published url:\n%s\n%s", session.ConnectShell, session.AgentPrompt)
	}

	profile, err := http.NewRequest(http.MethodGet, server.URL+"/s/"+session.ID+"/"+session.Services[0].Name+"/gmail/v1/users/me/profile", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.Header.Set("Authorization", "Bearer "+session.Services[0].Token)
	profileResponse, err := http.DefaultClient.Do(profile)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, profileResponse)
	if profileResponse.StatusCode != http.StatusOK || !strings.Contains(body, "emailAddress") {
		t.Fatalf("proxied profile %d: %s", profileResponse.StatusCode, body)
	}
	missing := get(t, server.URL+"/s/"+session.ID+"/missing/gmail/v1/users/me/profile")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown service status %d", missing.StatusCode)
	}
	missing.Body.Close()
}

func TestServeProxyInstructions(t *testing.T) {
	manager := New(all.Registry())
	t.Cleanup(func() { _ = manager.Shutdown() })
	session, err := manager.Start(context.Background(), StartRequest{Kind: "scenario", ID: "gmail.minimal.v1", Proxy: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(session.ID) })
	if session.ProxyConnection == nil || session.ProxyConnection.URL == "" || session.ProxyConnection.CAPath == "" {
		t.Fatalf("proxy connection: %#v", session.ProxyConnection)
	}
	if !strings.Contains(session.ConnectShell, "HTTP_PROXY=") || strings.Contains(session.ConnectShell, "curl ") {
		t.Fatalf("proxy shell should export the proxy and not call production:\n%s", session.ConnectShell)
	}
	for _, phrase := range []string{"Proxy mode is on", "Do not send the Fabricate bearer token", session.ProxyConnection.URL, "gmail.googleapis.com"} {
		if !strings.Contains(session.AgentPrompt, phrase) {
			t.Fatalf("proxy prompt missing %q:\n%s", phrase, session.AgentPrompt)
		}
	}
}

func TestServeRejectsUnknownTargets(t *testing.T) {
	server := httptest.NewServer(New(all.Registry()).Handler())
	t.Cleanup(server.Close)
	for _, body := range []string{
		`{"kind":"environment","id":"missing"}`,
		`{"kind":"scenario","id":"gmail.missing.v1"}`,
		`{"kind":"sandbox","id":"acme-gmail"}`,
		`{"kind":"environment"}`,
	} {
		response := postJSON(t, server.URL+"/api/sessions", body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s status %d: %s", body, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
}

func containsHost(hosts []string, host string) bool {
	for _, candidate := range hosts {
		if candidate == host {
			return true
		}
	}
	return false
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	return do(t, http.MethodPost, url, body)
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	return do(t, http.MethodGet, url, "")
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeJSON(t *testing.T, response *http.Response, dest any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(body))
}
