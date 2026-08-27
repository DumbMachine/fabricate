package specserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/httpresource"
	_ "modernc.org/sqlite"
)

const testToken = "fab_spec_test_token"

type testSecrets map[string]string

func (s testSecrets) Get(_ context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", fmt.Errorf("missing secret %s", key)
	}
	return value, nil
}

type testIDs struct{ n int }

func (ids *testIDs) Next(_ context.Context, kind string) (string, error) {
	ids.n++
	return fmt.Sprintf("%s-%04d", kind, ids.n), nil
}

func testSpec(t *testing.T) *CompiledSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSpec(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func testHandler(t *testing.T, compiled *CompiledSpec) http.Handler {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "spec.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(SchemaSQL()); err != nil {
		t.Fatal(err)
	}
	server, err := New(context.Background(), compiled, httpresource.ServerDependencies{
		DB: db, Clock: httpresource.FixedClock{}, IDs: &testIDs{}, Secrets: testSecrets{"token": testToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestCompilePrefixesAndWrap(t *testing.T) {
	compiled := testSpec(t)
	if compiled.Version != "3.0.3" {
		t.Fatalf("version = %s", compiled.Version)
	}
	ops := compiled.Index.Operations()
	if len(ops) != 7 {
		t.Fatalf("ops = %d %#v", len(ops), ops)
	}
	found := map[string]bool{}
	for _, op := range ops {
		found[op.Method+" "+op.Path] = true
	}
	if !found["GET /v1/items"] || !found["GET /v1/zones/{zone_id}"] {
		t.Fatalf("prefixed paths missing: %#v", found)
	}
}

func TestGitHubGitPath(t *testing.T) {
	if !GitHubGitPath("/repos/{owner}/{repo}/git/blobs/{file_sha}") {
		t.Fatal("git blobs")
	}
	if !GitHubGitPath("/repos/{owner}/{repo}/contents/{path}") {
		t.Fatal("contents")
	}
	if GitHubGitPath("/repos/{owner}/{repo}/issues") {
		t.Fatal("issues should be kept")
	}
	if GitHubGitPath("/repos/{owner}/{repo}/releases/generate-notes") {
		t.Fatal("generate-notes is not git")
	}
}

func TestDocumentCRUDAndCloudflareWrap(t *testing.T) {
	compiled := testSpec(t)
	handler := testHandler(t, compiled)

	unauthorized := httptest.NewRequest(http.MethodGet, "/v1/items", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unauthorized)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", rec.Code)
	}

	created := doExercise(t, handler, http.MethodPost, "/v1/items", `{"name":"alpha"}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item["id"] != "items-0001" || item["name"] != "alpha" {
		t.Fatalf("created = %#v", item)
	}

	listed := doExercise(t, handler, http.MethodGet, "/v1/items", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"alpha"`) || !strings.Contains(listed.Body.String(), `"data"`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	got := doExercise(t, handler, http.MethodGet, "/v1/items/items-0001", "", testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"alpha"`) {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}

	zone := doExercise(t, handler, http.MethodPost, "/v1/zones", `{"name":"acme.example"}`, testToken)
	if zone.Code != http.StatusOK || !strings.Contains(zone.Body.String(), `"result"`) || !strings.Contains(zone.Body.String(), `"acme.example"`) {
		t.Fatalf("zone create = %d %s", zone.Code, zone.Body.String())
	}

	ExerciseAllOperations(t, handler, compiled, testToken)
}

func TestTemplatedServerHostIsNotAPathPrefix(t *testing.T) {
	compiled, err := CompileSpec([]byte(`{
          "openapi": "3.0.3",
          "info": {"title": "S", "version": "1"},
          "servers": [{"url": "https://{region}.sentry.io"}],
          "paths": {"/api/0/projects/": {"get": {"operationId": "list", "responses": {"200": {"description": "ok"}}}}}
        }`), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range compiled.Index.Operations() {
		if op.Path != "/api/0/projects/" && !strings.HasPrefix(op.Path, "/api/0/projects") {
			t.Fatalf("unexpected path %s", op.Path)
		}
		if strings.Contains(op.Path, "https://") {
			t.Fatalf("host leaked into path: %s", op.Path)
		}
		found = true
	}
	if !found {
		t.Fatal("no operations")
	}
}

func TestDropPaths(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSpec(raw, func(path string) bool { return strings.HasPrefix(path, "/zones") })
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range compiled.Index.Operations() {
		if strings.Contains(op.Path, "zones") {
			t.Fatalf("dropped path still present: %s", op.Path)
		}
	}
}
