package cloudflare

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
	"sync"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/httpresource/specserver"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_cloudflare_test_token"

type testSecrets map[string]string

func (s testSecrets) Get(_ context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", fmt.Errorf("missing secret %s", key)
	}
	return value, nil
}

type testIDs struct {
	mu     sync.Mutex
	counts map[string]int
}

func (ids *testIDs) Next(_ context.Context, kind string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if ids.counts == nil {
		ids.counts = map[string]int{}
	}
	ids.counts[kind]++
	return fmt.Sprintf("%s-%04d", kind, ids.counts[kind]), nil
}

func loadScenario(t *testing.T, name string) scenario.Document {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("scenarios", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func openTestDB(t *testing.T, doc scenario.Document) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "cloudflare.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	codec := NewResource().Scenarios()
	if err := codec.Initialize(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := codec.Load(context.Background(), db, doc); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestHandler(t *testing.T, db *sql.DB, ids *testIDs) http.Handler {
	t.Helper()
	resource := NewResource()
	server, err := resource.NewServer(context.Background(), httpresource.ServerDependencies{
		DB: db, Clock: httpresource.FixedClock{Time: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)},
		IDs: ids, Secrets: testSecrets{"token": testToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	return server.Handler()
}

func request(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Api-Key", token)
		req.Header.Set("X-Figma-Token", token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-zones.v1.json")
	codec := NewResource().Scenarios()
	if err := codec.Validate(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := scenario.CanonicalJSON(doc)
	after, _ := scenario.CanonicalJSON(dumped)
	if string(before) != string(after) {
		t.Fatalf("load/dump changed the baseline:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestCompiledOpenAPIContractIsValid(t *testing.T) {
	resource := NewResource()
	bound := resource.(*specserver.Bound)
	compiled := bound.Compiled()
	if compiled.Index.Len() < 500 {
		t.Fatalf("compiled operation count = %d, want the full Cloudflare catalog", compiled.Index.Len())
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, specserver.Digest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-zones.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/client/v4/zones", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/client/v4/zones", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `acme.example`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	got := request(t, handler, http.MethodGet, "/client/v4/zones/zone_acme", "", testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `acme.example`) {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/client/v4/zones", `{"name":"billing.acme.example"}`, testToken)
	if created.Code != http.StatusOK && created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `billing.acme.example`) {
		t.Fatalf("create body = %s", created.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	id := specserver.NestedID(payload)
	if id == "" {
		t.Fatalf("create missing id: %s", created.Body.String())
	}
	persisted := request(t, handler, http.MethodGet, strings.ReplaceAll("/client/v4/zones/{id}", "{id}", id), "", testToken)
	if persisted.Code != http.StatusOK || !strings.Contains(persisted.Body.String(), `billing.acme.example`) {
		t.Fatalf("persisted create = %d %s", persisted.Code, persisted.Body.String())
	}
}

func TestAllOperationsAreRouted(t *testing.T) {
	entries, err := os.ReadDir("scenarios")
	if err != nil {
		t.Fatal(err)
	}
	minimal := "acme-zones.v1.json"
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "minimal") {
			minimal = entry.Name()
			break
		}
	}
	db := openTestDB(t, loadScenario(t, minimal))
	handler := newTestHandler(t, db, &testIDs{})
	bound := NewResource().(*specserver.Bound)
	specserver.ExerciseAllOperations(t, handler, bound.Compiled(), testToken)
}
