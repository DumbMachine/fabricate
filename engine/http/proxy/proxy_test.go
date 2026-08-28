package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestProxyInterceptsGmailAndInjectsSyntheticToken(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/gmail/v1/users/me/profile" || request.URL.RawQuery != "alt=json" {
			t.Fatalf("unexpected request URI %s", request.URL.RequestURI())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer synthetic" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"emailAddress":"support@acme.example"}`)
	}))
	defer backend.Close()

	p, err := Start(t.TempDir(), []Route{{Host: "gmail.googleapis.com", Target: backend.URL, Token: "synthetic"}}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	client := proxyClient(t, p)
	request, _ := http.NewRequest(http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile?alt=json", nil)
	request.Header.Set("Authorization", "Bearer production-shaped-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestInterceptedHostsAreUniqueAndSorted(t *testing.T) {
	p := &Proxy{routes: []compiledRoute{
		{Route: Route{Host: "www.googleapis.com"}},
		{Route: Route{Host: "gmail.googleapis.com"}},
		{Route: Route{Host: "www.googleapis.com", PathPrefix: "/gmail/"}},
	}}
	hosts := p.InterceptedHosts()
	if got, want := strings.Join(hosts, ","), "gmail.googleapis.com,www.googleapis.com"; got != want {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
}

func TestProxyMintsTokenUsedByService(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer synthetic" {
			t.Fatalf("wrong token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	p, err := Start(t.TempDir(), []Route{
		{Host: "oauth2.googleapis.com", PathPrefix: "/token", Token: "synthetic", OAuthToken: true},
		{Host: "gmail.googleapis.com", Target: backend.URL, Token: "synthetic"},
	}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	client := proxyClient(t, p)
	response, err := client.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded",
		strings.NewReader("grant_type=refresh_token&refresh_token=existing-refresh"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"access_token":"synthetic"`) {
		t.Fatalf("token response = %d %s", response.StatusCode, body)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile", nil)
	request.Header.Set("Authorization", "Bearer synthetic")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("gmail status = %d", response.StatusCode)
	}
}

func TestProxyRejectsUnknownHostInStrictMode(t *testing.T) {
	p, err := Start(t.TempDir(), []Route{{Host: "gmail.googleapis.com", Target: "http://127.0.0.1:1", Token: "synthetic"}}, nil, Options{RejectUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	client := proxyClient(t, p)
	_, err = client.Get("https://api.stripe.com/v1/customers")
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("expected forbidden proxy error, got %v", err)
	}
}

func TestProxyAllowsExplicitPassthroughHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "live-but-explicit")
	}))
	defer server.Close()
	p, err := Start(t.TempDir(), []Route{{Host: "gmail.googleapis.com", Target: "http://127.0.0.1:1", Token: "synthetic"}}, nil, Options{Passthrough: []string{"127.0.0.1"}, RejectUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	proxyURL, _ := url.Parse(p.URL)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Transport: transport}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "live-but-explicit" {
		t.Fatalf("body = %q", body)
	}
}

func TestProxyPassesThroughUnmappedHostByDefault(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "live-through-default")
	}))
	defer server.Close()
	p, err := Start(t.TempDir(), []Route{{Host: "gmail.googleapis.com", Target: "http://127.0.0.1:1", Token: "synthetic"}}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	proxyURL, _ := url.Parse(p.URL)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	response, err := (&http.Client{Transport: transport}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "live-through-default" {
		t.Fatalf("body = %q", body)
	}
}

func proxyClient(t *testing.T, p *Proxy) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(p.CAPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to load CA")
	}
	proxyURL, _ := url.Parse(p.URL)
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
}
