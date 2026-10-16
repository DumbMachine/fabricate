package httpresource

import (
	"context"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/scenario"
)

type stubResource struct{ descriptor Descriptor }

func (s stubResource) Descriptor() Descriptor                          { return s.descriptor }
func (s stubResource) Contract() Contract                              { return Contract{} }
func (s stubResource) Scenarios() ScenarioCodec                        { return nil }
func (s stubResource) ScenarioDocuments() ([]scenario.Document, error) { return nil, nil }
func (s stubResource) Scenario(string) (scenario.Document, error) {
	return scenario.Document{}, nil
}
func (s stubResource) NewServer(context.Context, ServerDependencies) (Server, error) {
	return nil, nil
}

func TestNewRegistryValidatesHostPrefixes(t *testing.T) {
	hosts := []string{"www.zohoapis.com", "accounts.zoho.com"}
	tests := []struct {
		name     string
		prefixes map[string]string
		wantErr  []string
	}{
		{name: "no prefixes", prefixes: nil},
		{name: "valid prefix", prefixes: map[string]string{"www.zohoapis.com": "/books/"}},
		{name: "valid nested prefix", prefixes: map[string]string{"www.zohoapis.com": "/books/v3/", "accounts.zoho.com": "/oauth/"}},
		{name: "unknown host", prefixes: map[string]string{"api.zohoapis.com": "/books/"}, wantErr: []string{`"zohobooks"`, `"api.zohoapis.com"`, "does not match any provider host"}},
		{name: "host case mismatch", prefixes: map[string]string{"WWW.zohoapis.com": "/books/"}, wantErr: []string{`"zohobooks"`, `"WWW.zohoapis.com"`, "does not match any provider host"}},
		{name: "host with trailing dot", prefixes: map[string]string{"www.zohoapis.com.": "/books/"}, wantErr: []string{`"www.zohoapis.com."`, "does not match any provider host"}},
		{name: "empty prefix", prefixes: map[string]string{"www.zohoapis.com": ""}, wantErr: []string{`"zohobooks"`, `"www.zohoapis.com"`, "is empty"}},
		{name: "whitespace prefix", prefixes: map[string]string{"www.zohoapis.com": "  \t"}, wantErr: []string{`"www.zohoapis.com"`, "is empty"}},
		{name: "padded prefix", prefixes: map[string]string{"www.zohoapis.com": " /books/ "}, wantErr: []string{`"www.zohoapis.com"`, "surrounding whitespace"}},
		{name: "missing leading slash", prefixes: map[string]string{"www.zohoapis.com": "books/"}, wantErr: []string{`"zohobooks"`, `"www.zohoapis.com"`, `must start with "/"`}},
		{name: "missing trailing slash", prefixes: map[string]string{"www.zohoapis.com": "/books"}, wantErr: []string{`"www.zohoapis.com"`, `must end with "/"`}},
		{name: "root prefix", prefixes: map[string]string{"www.zohoapis.com": "/"}, wantErr: []string{`"www.zohoapis.com"`, "claims the whole host"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := stubResource{descriptor: Descriptor{ID: "zohobooks", ProviderHosts: hosts, HostPrefixes: tt.prefixes}}
			registry, err := NewRegistry(resource)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("NewRegistry() error = %v", err)
				}
				if _, ok := registry.Get("zohobooks"); !ok {
					t.Fatal("resource not registered")
				}
				return
			}
			if err == nil {
				t.Fatalf("NewRegistry() succeeded, want error containing %q", tt.wantErr)
			}
			if registry != nil {
				t.Fatalf("NewRegistry() registry = %v, want nil on error", registry)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("NewRegistry() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestNewRegistryValidatesTokenRoutes(t *testing.T) {
	google := TokenRoute{Host: "oauth2.googleapis.com", Path: "/token"}
	tests := []struct {
		name    string
		routes  []TokenRoute
		wantErr []string
	}{
		{name: "no routes"},
		{name: "google route", routes: []TokenRoute{google}},
		{name: "two endpoints", routes: []TokenRoute{google, {Host: "accounts.zoho.com", Path: "/oauth/v2/token"}}},
		{name: "empty host", routes: []TokenRoute{{Path: "/token"}}, wantErr: []string{`"gmail"`, "host is empty"}},
		{name: "uppercase host", routes: []TokenRoute{{Host: "OAuth2.googleapis.com", Path: "/token"}}, wantErr: []string{`"OAuth2.googleapis.com"`, "lowercase bare hostname"}},
		{name: "host with port", routes: []TokenRoute{{Host: "oauth2.googleapis.com:443", Path: "/token"}}, wantErr: []string{"lowercase bare hostname"}},
		{name: "host with path", routes: []TokenRoute{{Host: "oauth2.googleapis.com/token", Path: "/token"}}, wantErr: []string{"lowercase bare hostname"}},
		{name: "host with trailing dot", routes: []TokenRoute{{Host: "oauth2.googleapis.com.", Path: "/token"}}, wantErr: []string{"lowercase bare hostname"}},
		{name: "empty path", routes: []TokenRoute{{Host: "oauth2.googleapis.com"}}, wantErr: []string{"must name an endpoint"}},
		{name: "root path", routes: []TokenRoute{{Host: "oauth2.googleapis.com", Path: "/"}}, wantErr: []string{"must name an endpoint"}},
		{name: "relative path", routes: []TokenRoute{{Host: "oauth2.googleapis.com", Path: "token"}}, wantErr: []string{`must start with "/"`}},
		{name: "trailing slash", routes: []TokenRoute{{Host: "oauth2.googleapis.com", Path: "/token/"}}, wantErr: []string{`must not end with "/"`}},
		{name: "padded path", routes: []TokenRoute{{Host: "oauth2.googleapis.com", Path: " /token"}}, wantErr: []string{"whitespace"}},
		{name: "query in path", routes: []TokenRoute{{Host: "oauth2.googleapis.com", Path: "/token?x=1"}}, wantErr: []string{"query"}},
		{name: "duplicate", routes: []TokenRoute{google, google}, wantErr: []string{"declared twice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := stubResource{descriptor: Descriptor{ID: "gmail", ProviderHosts: []string{"gmail.googleapis.com"}, TokenRoutes: tt.routes}}
			registry, err := NewRegistry(resource)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("NewRegistry() error = %v", err)
				}
				if _, ok := registry.Get("gmail"); !ok {
					t.Fatal("resource not registered")
				}
				return
			}
			if err == nil || registry != nil {
				t.Fatalf("NewRegistry() = %v, %v; want error containing %q", registry, err, tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("NewRegistry() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}
