package httpresource

import (
	"context"
	"testing"

	"github.com/dumbmachine/fabricate/scenario"
)

type stubResource struct{ descriptor Descriptor }

func (s stubResource) Descriptor() Descriptor { return s.descriptor }
func (stubResource) Contract() Contract       { return Contract{} }
func (stubResource) Scenarios() ScenarioCodec { return nil }
func (stubResource) ScenarioDocuments() ([]scenario.Document, error) {
	return nil, nil
}
func (stubResource) Scenario(string) (scenario.Document, error) {
	return scenario.Document{}, nil
}
func (stubResource) NewServer(context.Context, ServerDependencies) (Server, error) {
	return nil, nil
}

func TestNewRegistryRejectsNonCanonicalHostPrefixes(t *testing.T) {
	base := Descriptor{
		ID:            "books",
		ProviderHosts: []string{"www.zohoapis.com"},
		HostPrefixes:  map[string]string{"www.zohoapis.com": "/books/"},
	}
	if _, err := NewRegistry(stubResource{descriptor: base}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"books", "/books", " /books/ "} {
		descriptor := base
		descriptor.HostPrefixes = map[string]string{"www.zohoapis.com": prefix}
		if _, err := NewRegistry(stubResource{descriptor: descriptor}); err == nil {
			t.Fatalf("prefix %q was accepted", prefix)
		}
	}
	foreign := base
	foreign.HostPrefixes = map[string]string{"other.example": "/books/"}
	if _, err := NewRegistry(stubResource{descriptor: foreign}); err == nil {
		t.Fatal("prefix host outside ProviderHosts was accepted")
	}
	oauth := Descriptor{
		ID:            "gmail",
		ProviderHosts: []string{"www.googleapis.com"},
		HostPrefixes:  map[string]string{"www.googleapis.com": "/gmail/"},
		ProxyRoutes:   []ProxyRoute{{Host: "oauth2.googleapis.com", PathPrefix: "/token", OAuthToken: true}},
	}
	if _, err := NewRegistry(stubResource{descriptor: oauth}); err != nil {
		t.Fatal(err)
	}
	badRoute := oauth
	badRoute.ProxyRoutes = []ProxyRoute{{Host: "oauth2.googleapis.com", PathPrefix: "token"}}
	if _, err := NewRegistry(stubResource{descriptor: badRoute}); err == nil {
		t.Fatal("proxy route prefix without a leading slash was accepted")
	}
}
