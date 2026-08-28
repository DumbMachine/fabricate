package docsexamples

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestCatalogFromOpenAPIDropsEmptyPaths(t *testing.T) {
	spec := `{
  "paths": {
    "/goals": {},
    "/tasks": {
      "get": {"operationId": "getTasks", "summary": "List tasks"},
      "post": {"operationId": "createTask", "summary": "Create a task"}
    },
    "/tasks/{id}": {
      "parameters": [{"name": "id", "in": "path"}],
      "get": {"operationId": "getTask"}
    }
  }
}`
	catalog, err := CatalogFromOpenAPI("asana", "Asana v1", []byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Integration != "asana" || catalog.API != "Asana v1" {
		t.Fatalf("header = %+v", catalog)
	}
	if len(catalog.Operations) != 3 {
		t.Fatalf("operations = %#v", catalog.Operations)
	}
	if catalog.Operations[0] != (Operation{Method: "GET", Path: "/tasks", OperationID: "getTasks", Summary: "List tasks"}) {
		t.Fatalf("first = %+v", catalog.Operations[0])
	}
	if catalog.Operations[1].Method != "POST" || catalog.Operations[1].OperationID != "createTask" {
		t.Fatalf("second = %+v", catalog.Operations[1])
	}
	if catalog.Operations[2] != (Operation{Method: "GET", Path: "/tasks/{id}", OperationID: "getTask"}) {
		t.Fatalf("third = %+v", catalog.Operations[2])
	}
}

func TestCatalogFromOpenAPIRequiresOperationID(t *testing.T) {
	_, err := CatalogFromOpenAPI("gmail", "Gmail v1", []byte(`{"paths":{"/profile":{"get":{"summary":"Profile"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "missing operationId") {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogFromOpenAPIRequiresOperations(t *testing.T) {
	_, err := CatalogFromOpenAPI("gmail", "Gmail v1", []byte(`{"paths":{"/drafts":{}}}`))
	if err == nil || !strings.Contains(err.Error(), "no operations") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteOperationsSkipUnchanged(t *testing.T) {
	repo := t.TempDir()
	registry := mustRegistry(t, stubResource{
		id:   "gmail",
		name: "Gmail",
		spec: []byte(`{"paths":{"/gmail/v1/users/{userId}/profile":{"get":{"operationId":"gmail.users.getProfile","summary":"Profile"}}}}`),
	})
	if err := WriteOperations(repo, registry, nil, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(repo, generatedDir, "gmail.operations.json")
	first, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), `"gmail.users.getProfile"`) {
		t.Fatalf("got %s", first)
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Fatal("expected trailing newline")
	}
	if err := os.WriteFile(dest, append([]byte("stale"), first...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteOperations(repo, registry, []string{"gmail"}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatalf("rewrite = %s", second)
	}
	if err := WriteOperations(repo, registry, nil, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	third, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(third) != string(first) {
		t.Fatalf("skip changed file: %s", third)
	}
}

func TestWriteOperationsRejectsUnknownResource(t *testing.T) {
	registry := mustRegistry(t, stubResource{id: "gmail", name: "Gmail", spec: []byte(`{"paths":{"/profile":{"get":{"operationId":"getProfile"}}}}`)})
	err := WriteOperations(t.TempDir(), registry, []string{"asana"}, ioDiscard())
	if err == nil || !strings.Contains(err.Error(), `unknown resource "asana"`) {
		t.Fatalf("got %v", err)
	}
}

func TestWriteOperationsAcceptsResourcePathSelector(t *testing.T) {
	repo := t.TempDir()
	registry := mustRegistry(t,
		stubResource{id: "gmail", name: "Gmail", spec: []byte(`{"paths":{"/profile":{"get":{"operationId":"getProfile"}}}}`)},
		stubResource{id: "asana", name: "Asana", spec: []byte(`{"paths":{"/tasks":{"get":{"operationId":"getTasks"}}}}`)},
	)
	if err := WriteOperations(repo, registry, []string{"resources/asana"}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, generatedDir, "asana.operations.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, generatedDir, "gmail.operations.json")); !os.IsNotExist(err) {
		t.Fatalf("gmail should not be written: %v", err)
	}
}

func TestLoadRejectsOperationsOutputPath(t *testing.T) {
	repo := writeExampleRepo(t)
	specPath := filepath.Join(repo, "packages/docs-content/resources/_examples/gmail-list-messages.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "gmail-list-messages.json", "gmail.operations.json", 1))
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "operations catalog") {
		t.Fatalf("got %v", err)
	}
}

type stubResource struct {
	id   string
	name string
	spec []byte
}

func (s stubResource) Descriptor() httpresource.Descriptor {
	return httpresource.Descriptor{ID: s.id, DisplayName: s.name, Version: "v1"}
}

func (s stubResource) Contract() httpresource.Contract {
	return httpresource.Contract{OpenAPIJSON: s.spec}
}

func (stubResource) Scenarios() httpresource.ScenarioCodec { return nil }

func (stubResource) Scenario(string) (scenario.Document, error) {
	return scenario.Document{}, nil
}

func (stubResource) NewServer(context.Context, httpresource.ServerDependencies) (httpresource.Server, error) {
	return nil, nil
}

func mustRegistry(t *testing.T, resources ...httpresource.Resource) *httpresource.Registry {
	t.Helper()
	registry, err := httpresource.NewRegistry(resources...)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
