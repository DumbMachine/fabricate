package environment

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
)

func TestRuntimeServesAcmeGmailThroughTransparentProxy(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	registry := all.Registry()
	spec, err := Parse([]byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata: {name: acme-gmail}
services:
  support-mail: {resource: gmail, scenario: gmail.acme-corp.v1}
`))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, registry, true)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := runtime.StateDir
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	client := runtimeProxyClient(t, runtime)

	tokenResponse, err := client.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded",
		strings.NewReader("grant_type=refresh_token&refresh_token=existing"))
	if err != nil {
		t.Fatal(err)
	}
	tokenBody, _ := io.ReadAll(tokenResponse.Body)
	tokenResponse.Body.Close()
	if !strings.Contains(string(tokenBody), runtime.Services["support-mail"].Token) {
		t.Fatalf("OAuth token does not match service token: %s", tokenBody)
	}

	request, _ := http.NewRequest(http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile", nil)
	request.Header.Set("Authorization", "Bearer anything")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"messagesTotal":28`) {
		t.Fatalf("profile = %d %s", response.StatusCode, body)
	}
	logPath := runtime.Requests.Path()
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state directory still exists: %v", err)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("request log was not retained: %v", err)
	}
	if !strings.Contains(string(logBody), `"messagesTotal":28`) || strings.Contains(string(logBody), runtime.Services["support-mail"].Token) {
		t.Fatalf("request log missing payload or leaked token: %s", logBody)
	}
}

func TestCheckedInEnvironmentsStart(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "environments", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no checked-in environment manifests")
	}
	registry := all.Registry()
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Setenv("FAB_LOG_DIR", t.TempDir())
			spec, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := Start(context.Background(), spec, registry, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close(context.Background()) })
			if got, want := len(runtime.Services), len(spec.Services); got != want {
				t.Fatalf("started %d services, want %d", got, want)
			}
			for name, service := range runtime.Services {
				if service.URL == "" || service.Token == "" {
					t.Fatalf("service %q missing URL or token", name)
				}
			}
		})
	}
}

func TestRuntimeServesAcmeSupportDeskAcrossServices(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-support-desk.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	for _, name := range []string{"support-mail", "inbox", "board", "crm"} {
		if runtime.Services[name] == nil {
			t.Fatalf("missing service %q (%d started)", name, len(runtime.Services))
		}
	}
	client := runtimeProxyClient(t, runtime)
	token := func(name string) string { return runtime.Services[name].Token }

	mail := authorizedGET(t, client, "https://gmail.googleapis.com/gmail/v1/users/me/messages/msg-0001", token("support-mail"))
	assertContains(t, mail, "gmail INV-4812", "INV-4812", "dana@northwind.example")

	inbox := authorizedGET(t, client, "https://api.intercom.io/conversations/101", token("inbox"))
	assertContains(t, inbox, "intercom INV-4812", "INV-4812", "contact-dana")

	ssoInbox := authorizedGET(t, client, "https://api.intercom.io/conversations/102", token("inbox"))
	assertContains(t, ssoInbox, "intercom Contoso", "Contoso", "contoso-eu")

	board := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-double-charge", token("board"))
	assertContains(t, board, "asana INV-4812", "INV-4812", "Northwind")

	ssoTask := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-sso", token("board"))
	assertContains(t, ssoTask, "asana Contoso", "contoso-eu")

	stories := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-sso/stories", token("board"))
	assertContains(t, stories, "asana Mei Chen comment", "mei.chen@contoso.example")

	deal := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/deals/301", token("crm"))
	assertContains(t, deal, "hubspot INV-4812", "INV-4812", "Northwind")

	mei := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/contacts/103", token("crm"))
	assertContains(t, mei, "hubspot Mei Chen", "mei.chen@contoso.example")

	listed := authorizedGET(t, http.DefaultClient, runtime.Services["support-mail"].URL+"/gmail/v1/users/me/messages?q=INV-4812", token("support-mail"))
	assertContains(t, listed, "direct Gmail search", "msg-0001")
}

func TestRuntimeServesAcmeBillingOps(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-billing-ops.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	for _, name := range []string{"support-mail", "inbox", "crm"} {
		if runtime.Services[name] == nil {
			t.Fatalf("missing service %q (%d started)", name, len(runtime.Services))
		}
	}
	client := runtimeProxyClient(t, runtime)
	mail := authorizedGET(t, client, "https://gmail.googleapis.com/gmail/v1/users/me/messages/msg-0001", runtime.Services["support-mail"].Token)
	inbox := authorizedGET(t, client, "https://api.intercom.io/conversations/101", runtime.Services["inbox"].Token)
	deal := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/deals/301", runtime.Services["crm"].Token)
	assertContains(t, mail, "billing-ops gmail", "INV-4812")
	assertContains(t, inbox, "billing-ops intercom", "INV-4812")
	assertContains(t, deal, "billing-ops hubspot", "INV-4812")
}

func authorizedGET(t *testing.T, client *http.Client, rawURL, token string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d %s", rawURL, response.StatusCode, body)
	}
	return string(body)
}

func assertContains(t *testing.T, body, label string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("%s missing %q: %s", label, needle, body)
		}
	}
}

func runtimeProxyClient(t *testing.T, runtime *Runtime) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(runtime.Proxy.CAPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	proxyURL, _ := url.Parse(runtime.Proxy.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
}
