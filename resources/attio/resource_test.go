package attio

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
	"github.com/dumbmachine/fabricate/resources/attio/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_attio_test_token"

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
	return fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", ids.counts[kind]), nil
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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "attio.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-workspace.v1.json")
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
	if len(state.Records) != 21 {
		t.Fatalf("records = %d, want 21", len(state.Records))
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
	if len(seen) != 6 {
		t.Fatalf("compiled operation count = %d, want 6", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-workspace.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v2/objects", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	people := request(t, handler, http.MethodPost, "/v2/objects/people/records/query", `{}`, testToken)
	if people.Code != http.StatusOK || !strings.Contains(people.Body.String(), `"dana@northwind.example"`) {
		t.Fatalf("people query = %d %s", people.Code, people.Body.String())
	}

	deal := request(t, handler, http.MethodGet, "/v2/objects/deals/records/c1111111-1111-4111-8111-111111111111", "", testToken)
	if deal.Code != http.StatusOK || !strings.Contains(deal.Body.String(), `"INV-4812"`) {
		t.Fatalf("deal = %d %s", deal.Code, deal.Body.String())
	}

	updated := request(t, handler, http.MethodPatch, "/v2/objects/deals/records/c1111111-1111-4111-8111-111111111111",
		`{"data":{"values":{"stage":[{"value":"closed_won","attribute_type":"text"}]}}}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"closed_won"`) {
		t.Fatalf("patch deal = %d %s", updated.Code, updated.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v2/objects/people/records",
		`{"data":{"values":{"name":[{"value":"Casey Pike"}],"email_addresses":[{"email_address":"casey@acme.example"}]}}}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"casey@acme.example"`) {
		t.Fatalf("create person = %d %s", created.Code, created.Body.String())
	}

	members := request(t, handler, http.MethodGet, "/v2/workspace_members", "", testToken)
	if members.Code != http.StatusOK || !strings.Contains(members.Body.String(), `"val@acme.example"`) {
		t.Fatalf("members = %d %s", members.Code, members.Body.String())
	}
}

func TestMissingRecordNotFound(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-workspace.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	missing := request(t, handler, http.MethodGet, "/v2/objects/people/records/ffffffff-ffff-4fff-8fff-ffffffffffff", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"record not found"`) {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
}
