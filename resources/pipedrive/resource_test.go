package pipedrive

import (
	"context"
	"database/sql"
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
	"github.com/dumbmachine/fabricate/resources/pipedrive/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_pipedrive_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "pipedrive.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	codec := scenarioCodec{}
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
	if body != "" && !strings.Contains(req.Header.Get("Content-Type"), "form") {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-pipeline.v1.json")
	codec := scenarioCodec{}
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
	spec, err := generated.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			if operation.OperationID == "" {
				t.Fatalf("%s %s has no operationId", method, path)
			}
			if previous, exists := seen[operation.OperationID]; exists {
				t.Fatalf("operationId %q is duplicated by %s and %s %s", operation.OperationID, previous, method, path)
			}
			seen[operation.OperationID] = method + " " + path
		}
	}
	if len(seen) != 11 {
		t.Fatalf("compiled operation count = %d, want 11", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-pipeline.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/api/v2/persons", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	persons := request(t, handler, http.MethodGet, "/api/v2/persons", "", testToken)
	if persons.Code != http.StatusOK || !strings.Contains(persons.Body.String(), `"dana@northwind.example"`) {
		t.Fatalf("persons = %d %s", persons.Code, persons.Body.String())
	}

	deal := request(t, handler, http.MethodGet, "/deals/301", "", testToken)
	if deal.Code != http.StatusOK || !strings.Contains(deal.Body.String(), `"INV-4812"`) {
		t.Fatalf("deal = %d %s", deal.Code, deal.Body.String())
	}

	updated := request(t, handler, http.MethodPatch, "/api/v2/deals/301", `{"status":"won"}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"won"`) {
		t.Fatalf("update deal = %d %s", updated.Code, updated.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/api/v2/persons", `{"name":"Casey Pike","emails":[{"value":"casey@acme.example","primary":true}]}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"Casey Pike"`) {
		t.Fatalf("create person = %d %s", created.Code, created.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/api/v2/persons", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"Casey Pike"`) {
		t.Fatalf("list after create = %d %s", listed.Code, listed.Body.String())
	}
}
