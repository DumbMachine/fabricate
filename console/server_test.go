package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/environment"
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
	if err := manager.Configure(Config{Proxy: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	session, err := manager.Start(context.Background(), StartRequest{Kind: "scenario", ID: "gmail.minimal.v1"})
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

func TestAdoptedSandboxStaysUpWhileAnotherCanStart(t *testing.T) {
	manager := New(all.Registry())
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = manager.Shutdown() })

	spec := environment.Spec{
		APIVersion: environment.APIVersion,
		Kind:       "Environment",
		Metadata:   environment.Metadata{Name: "gmail"},
		Services: map[string]environment.ServiceSpec{
			"gmail": {Resource: "gmail", Scenario: "gmail.minimal.v1"},
		},
	}
	runtime, err := environment.Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := manager.Adopt(spec, runtime, true, "scenario")
	if err != nil {
		_ = runtime.Close(context.Background())
		t.Fatal(err)
	}
	if !adopted.Anchored || !adopted.Proxy || adopted.ProxyConnection == nil {
		t.Fatalf("adopted session: %#v", adopted)
	}
	if !strings.Contains(adopted.AgentPrompt, "stays up until that command exits") {
		t.Fatalf("prompt: %s", adopted.AgentPrompt)
	}

	response := get(t, server.URL+"/api/sessions/"+adopted.ID)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get status %d: %s", response.StatusCode, readBody(t, response))
	}
	var listed Session
	decodeJSON(t, response, &listed)
	if !listed.Anchored || listed.Services[0].Name != "gmail" {
		t.Fatalf("listed session: %#v", listed)
	}
	closed := do(t, http.MethodDelete, server.URL+"/api/sessions/"+adopted.ID, "")
	if closed.StatusCode != http.StatusConflict {
		t.Fatalf("close anchored status %d: %s", closed.StatusCode, readBody(t, closed))
	}
	if err := manager.Close(adopted.ID); !errors.Is(err, errAnchored) {
		t.Fatalf("close error: %v", err)
	}

	created := postJSON(t, server.URL+"/api/sessions", `{"kind":"scenario","id":"gmail.minimal.v1"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("start another status %d: %s", created.StatusCode, readBody(t, created))
	}
	var extra Session
	decodeJSON(t, created, &extra)
	if extra.Anchored || extra.ID == adopted.ID {
		t.Fatalf("extra session: %#v", extra)
	}
	removed := do(t, http.MethodDelete, server.URL+"/api/sessions/"+extra.ID, "")
	if removed.StatusCode != http.StatusNoContent {
		t.Fatalf("close extra status %d", removed.StatusCode)
	}
	still := get(t, server.URL+"/api/sessions/"+adopted.ID)
	if still.StatusCode != http.StatusOK {
		t.Fatalf("anchored session status after extra close %d", still.StatusCode)
	}
	still.Body.Close()
}

func TestServeRejectsUnknownTargets(t *testing.T) {
	server := httptest.NewServer(New(all.Registry()).Handler())
	t.Cleanup(server.Close)
	for _, body := range []string{
		`{"kind":"environment","id":"missing"}`,
		`{"kind":"scenario","id":"gmail.missing.v1"}`,
		`{"kind":"sandbox","id":"acme-gmail"}`,
		`{"kind":"environment"}`,
		`{"kind":"scenario","id":"gmail.minimal.v1","proxy":true}`,
	} {
		response := postJSON(t, server.URL+"/api/sessions", body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s status %d: %s", body, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
}

func TestConfiguredProxyIgnoresTheRequest(t *testing.T) {
	manager := New(all.Registry())
	if err := manager.Configure(Config{Proxy: true, PublicURL: "https://console.example.com/app/"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = manager.Shutdown() })

	config := get(t, server.URL+"/api/config")
	if config.StatusCode != http.StatusOK {
		t.Fatalf("config status %d: %s", config.StatusCode, readBody(t, config))
	}
	var deployment Deployment
	decodeJSON(t, config, &deployment)
	if !deployment.Proxy || deployment.PublicURL != "https://console.example.com/app" {
		t.Fatalf("deployment: %#v", deployment)
	}

	created := postJSON(t, server.URL+"/api/sessions", `{"kind":"scenario","id":"gmail.minimal.v1"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("start status %d: %s", created.StatusCode, readBody(t, created))
	}
	var session Session
	decodeJSON(t, created, &session)
	if !session.Proxy || session.ProxyConnection == nil {
		t.Fatalf("session: %#v", session)
	}
	want := "https://console.example.com/app/s/" + session.ID + "/" + session.Services[0].Name
	if session.Services[0].URL != want {
		t.Fatalf("published url %q", session.Services[0].URL)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/sessions/"+session.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "internal.example"
	request.Header.Set("X-Forwarded-Proto", "http")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var listed Session
	decodeJSON(t, response, &listed)
	if listed.Services[0].URL != want {
		t.Fatalf("configured host lost: %q", listed.Services[0].URL)
	}
}

func TestNormalizePublicURL(t *testing.T) {
	normalized, err := NormalizePublicURL(" HTTPS://Console.Example.com/app/ ")
	if err != nil || normalized != "https://Console.Example.com/app" {
		t.Fatalf("normalized %q err %v", normalized, err)
	}
	if _, err := NormalizePublicURL("console.example.com"); err == nil {
		t.Fatal("relative URL accepted")
	}
	if _, err := NormalizePublicURL("https://user:pass@console.example.com"); err == nil {
		t.Fatal("user info accepted")
	}
	if _, err := NormalizePublicURL("https://console.example.com/app?x=1"); err == nil {
		t.Fatal("query accepted")
	}
	empty, err := NormalizePublicURL("  ")
	if err != nil || empty != "" {
		t.Fatalf("empty %q err %v", empty, err)
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
