package environments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogLoadsOfficialEnvironment(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 {
		t.Fatalf("catalog has %d environments", len(names))
	}
	spec, err := Load("acme-support-desk")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Metadata.Name != "acme-support-desk" || len(spec.Services) != 4 {
		t.Fatalf("unexpected environment: %#v", spec)
	}
}

func TestResolveLoadsOfficialNameAndManifestPath(t *testing.T) {
	spec, err := Resolve("acme-gmail")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Metadata.Name != "acme-gmail" || spec.Services["support-mail"].Resource != "gmail" {
		t.Fatalf("unexpected environment: %#v", spec)
	}

	path := filepath.Join(t.TempDir(), "local.yaml")
	raw := []byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata:
  name: local-mail
services:
  mail:
    resource: gmail
    scenario: gmail.minimal.v1
`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err = Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Metadata.Name != "local-mail" {
		t.Fatalf("path environment: %#v", spec)
	}

	_, err = Resolve("not-an-environment")
	if err == nil || !strings.Contains(err.Error(), "unknown environment") || !strings.Contains(err.Error(), "acme-gmail") {
		t.Fatalf("unexpected error: %v", err)
	}
}
