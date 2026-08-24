package environment

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"os"
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
proxy: {enabled: true}
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
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"messagesTotal":12`) {
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
	if !strings.Contains(string(logBody), `"messagesTotal":12`) || strings.Contains(string(logBody), runtime.Services["support-mail"].Token) {
		t.Fatalf("request log missing payload or leaked token: %s", logBody)
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
