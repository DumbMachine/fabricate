package asana

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
	"github.com/dumbmachine/fabricate/resources/asana/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_asana_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "asana.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	if body != "" {
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
	doc := loadScenario(t, "acme-sprint.v1.json")
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
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 10 {
		t.Fatalf("Acme scenario contains %d tasks, want 10", len(state.Tasks))
	}
}

func TestCompiledOpenAPIContractIsValid(t *testing.T) {
	resource := NewResource()
	spec, err := generated.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("compiled OpenAPI is invalid: %v", err)
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
	if len(seen) != 15 {
		t.Fatalf("compiled operation count = %d, want 15", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-sprint.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/users/me", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	me := request(t, handler, http.MethodGet, "/users/me", "", testToken)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"val@acme.example"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}

	prefixed := request(t, handler, http.MethodGet, "/api/1.0/users/me", "", testToken)
	if prefixed.Code != http.StatusOK || !strings.Contains(prefixed.Body.String(), `"user-val"`) {
		t.Fatalf("prefixed me = %d %s", prefixed.Code, prefixed.Body.String())
	}

	tasks := request(t, handler, http.MethodGet, "/projects/proj-checkout/tasks", "", testToken)
	if tasks.Code != http.StatusOK || !strings.Contains(tasks.Body.String(), `"task-double-charge"`) {
		t.Fatalf("project tasks = %d %s", tasks.Code, tasks.Body.String())
	}

	updated := request(t, handler, http.MethodPut, "/tasks/task-double-charge", `{"data":{"completed":true}}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"completed":true`) {
		t.Fatalf("complete task = %d %s", updated.Code, updated.Body.String())
	}

	comment := request(t, handler, http.MethodPost, "/tasks/task-double-charge/stories", `{"data":{"text":"Refund issued as rf_4812."}}`, testToken)
	if comment.Code != http.StatusCreated || !strings.Contains(comment.Body.String(), "Refund issued") {
		t.Fatalf("comment = %d %s", comment.Code, comment.Body.String())
	}

	stories := request(t, handler, http.MethodGet, "/tasks/task-double-charge/stories", "", testToken)
	if stories.Code != http.StatusOK || !strings.Contains(stories.Body.String(), "Refund issued") {
		t.Fatalf("stories after comment = %d %s", stories.Code, stories.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/tasks", `{"data":{"name":"Follow up with Northwind","projects":["proj-checkout"],"assignee":"me"}}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"asana.task-0001"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}

	again := request(t, handler, http.MethodGet, "/projects/proj-checkout/tasks", "", testToken)
	if !strings.Contains(again.Body.String(), `"asana.task-0001"`) {
		t.Fatalf("created task missing from project list: %s", again.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-sprint.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/tasks/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"message":"task not found"`) {
		t.Fatalf("missing task = %d %s", missing.Code, missing.Body.String())
	}

	invalid := request(t, handler, http.MethodPost, "/tasks", `{"data":{}}`, testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "name is required") {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestTwoAsanaInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-sprint.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPut, "/tasks/task-sso", `{"data":{"completed":true}}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("complete = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/tasks/task-sso", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"completed":false`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
