package outreach

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
	"github.com/dumbmachine/fabricate/resources/outreach/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_outreach_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "outreach.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	req.Header.Set("Content-Type", "application/vnd.api+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-sequences.v1.json")
	codec := scenarioCodec{}
	if err := codec.Validate(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	before, err := scenario.CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	after, err := scenario.CanonicalJSON(dumped)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("load/dump changed the baseline:\nbefore=%s\nafter=%s", before, after)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Sequences) != 2 || len(state.Prospects) != 3 || len(state.SequenceStates) != 3 || len(state.Tasks) != 1 {
		t.Fatalf("counts sequences=%d prospects=%d sequenceStates=%d tasks=%d", len(state.Sequences), len(state.Prospects), len(state.SequenceStates), len(state.Tasks))
	}
	if state.Sequences[0].Name != "Northwind expansion" || state.Sequences[1].Name != "Helix Bio trial" {
		t.Fatalf("sequence names = %#v", state.Sequences)
	}
	if !strings.Contains(state.Tasks[0].Note, "INV-4812") || state.Tasks[0].Action != "call" {
		t.Fatalf("call task = %#v", state.Tasks[0])
	}
}

func TestMinimalScenarioRoundTripsEmptyArrays(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	codec := scenarioCodec{}
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := scenario.CanonicalJSON(doc)
	after, _ := scenario.CanonicalJSON(dumped)
	if string(before) != string(after) {
		t.Fatalf("load/dump changed the empty baseline:\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.Contains(string(after), `"sequences":[]`) || !strings.Contains(string(after), `"tasks":[]`) {
		t.Fatalf("empty slices were not dumped as arrays: %s", after)
	}
}

func TestCanonicalJSONIgnoresStateKeyOrder(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	reordered := doc
	reordered.State = []byte(`{"tasks":[],"sequences":[],"prospects":[],"sequenceStates":[]}`)
	if err := (scenarioCodec{}).Validate(context.Background(), reordered); err != nil {
		t.Fatal(err)
	}
	before, err := scenario.CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	after, err := scenario.CanonicalJSON(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("key order changed canonical JSON:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	err = (scenarioCodec{}).Validate(context.Background(), doc)
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("expected unknown field error, got %v", err)
	}

	nested := loadScenario(t, "acme-sequences.v1.json")
	var nestedState map[string]any
	if err := json.Unmarshal(nested.State, &nestedState); err != nil {
		t.Fatal(err)
	}
	sequences := nestedState["sequences"].([]any)
	sequences[0].(map[string]any)["nope"] = "x"
	raw, err = json.Marshal(nestedState)
	if err != nil {
		t.Fatal(err)
	}
	nested.State = raw
	err = (scenarioCodec{}).Validate(context.Background(), nested)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected nested unknown field error, got %v", err)
	}

	envelope := []byte(`{"$contract":"fabricate.scenario","$contractVersion":1,"$id":"outreach.minimal.v1","$resource":"outreach","$resourceVersion":"v2","state":{"sequences":[],"prospects":[],"sequenceStates":[],"tasks":[]},"extra":true}`)
	if _, err := scenario.Parse(envelope); err == nil {
		t.Fatal("expected unknown envelope field error")
	}
}

func TestScenarioReferencesAndTaskConsistency(t *testing.T) {
	doc := loadScenario(t, "acme-sequences.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state.Tasks[0].ProspectID = "missing"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown prospect error")
	}

	doc = loadScenario(t, "acme-sequences.v1.json")
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state.Prospects[0].Emails = []string{"not-an-email"}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected invalid email error")
	}

	doc = loadScenario(t, "acme-sequences.v1.json")
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state.Tasks[0].Completed = true
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected completed/state mismatch error")
	}
}

func TestEmbeddedScenarios(t *testing.T) {
	resource := NewResource()
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("embedded scenarios = %d", len(docs))
	}
	doc, err := resource.Scenario("outreach.acme-sequences.v1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Resource != "outreach" || doc.ResourceVersion != "v2" {
		t.Fatalf("embedded scenario = %s %s", doc.Resource, doc.ResourceVersion)
	}
	if _, err := resource.Scenario("outreach.missing.v1"); err == nil {
		t.Fatal("expected unknown scenario error")
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
	if len(seen) != 13 {
		t.Fatalf("compiled operation count = %d, want 13: %#v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if got := resource.Descriptor().ProviderHosts; len(got) != 1 || got[0] != "api.outreach.io" {
		t.Fatalf("provider hosts = %#v", got)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	doc := loadScenario(t, "acme-sequences.v1.json")
	db := openTestDB(t, doc)
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/api/v2/sequences", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Unauthorized") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/api/v2/sequences", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	sequences := request(t, handler, http.MethodGet, "/api/v2/sequences", "", testToken)
	if sequences.Code != http.StatusOK || !strings.Contains(sequences.Body.String(), `"name":"Northwind expansion"`) || !strings.Contains(sequences.Body.String(), `"name":"Helix Bio trial"`) {
		t.Fatalf("sequences = %d %s", sequences.Code, sequences.Body.String())
	}

	prospects := request(t, handler, http.MethodGet, "/api/v2/prospects", "", testToken)
	for _, email := range []string{"dana@northwind.example", "jules@northwind.example", "noah@helixbio.example"} {
		if prospects.Code != http.StatusOK || !strings.Contains(prospects.Body.String(), email) {
			t.Fatalf("prospects missing %s = %d %s", email, prospects.Code, prospects.Body.String())
		}
	}

	northwind := request(t, handler, http.MethodGet, "/api/v2/sequenceStates?filter[sequence][id]=1", "", testToken)
	if northwind.Code != http.StatusOK || !strings.Contains(northwind.Body.String(), `"id":"1"`) || !strings.Contains(northwind.Body.String(), `"id":"2"`) || strings.Contains(northwind.Body.String(), `"id":"3"`) {
		t.Fatalf("northwind sequence states = %d %s", northwind.Code, northwind.Body.String())
	}

	tasks := request(t, handler, http.MethodGet, "/api/v2/tasks", "", testToken)
	if tasks.Code != http.StatusOK || !strings.Contains(tasks.Body.String(), "INV-4812") || !strings.Contains(tasks.Body.String(), `"completed":false`) || !strings.Contains(tasks.Body.String(), `"action":"call"`) {
		t.Fatalf("tasks = %d %s", tasks.Code, tasks.Body.String())
	}

	updated := request(t, handler, http.MethodPatch, "/api/v2/tasks/1", `{"data":{"type":"task","id":"1","attributes":{"completed":true}}}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"completed":true`) || !strings.Contains(updated.Body.String(), `"state":"complete"`) {
		t.Fatalf("complete task = %d %s", updated.Code, updated.Body.String())
	}

	again := request(t, handler, http.MethodGet, "/api/v2/tasks/1", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"completed":true`) || !strings.Contains(again.Body.String(), `"state":"complete"`) || !strings.Contains(again.Body.String(), `"completedAt":"2026-08-26T12:00:00Z"`) || !strings.Contains(again.Body.String(), "INV-4812") {
		t.Fatalf("task after complete = %d %s", again.Code, again.Body.String())
	}

	codec := scenarioCodec{}
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || !state.Tasks[0].Completed || state.Tasks[0].State != "complete" || state.Tasks[0].CompletedAt != "2026-08-26T12:00:00Z" {
		t.Fatalf("dumped task = %#v", state.Tasks)
	}

	created := request(t, handler, http.MethodPost, "/api/v2/tasks", `{"data":{"type":"task","attributes":{"action":"email","note":"Trial check-in"},"relationships":{"prospect":{"data":{"type":"prospect","id":"3"}},"sequence":{"data":{"type":"sequence","id":"2"}}}}}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"outreach.task-0001"`) {
		t.Fatalf("create task = %d %s", created.Code, created.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/api/v2/tasks?filter[sequence][id]=2", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"outreach.task-0001"`) || !strings.Contains(listed.Body.String(), "Trial check-in") {
		t.Fatalf("tasks after create = %d %s", listed.Code, listed.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-sequences.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/api/v2/tasks/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.HasPrefix(missing.Body.String(), `{"errors":`) || !strings.Contains(missing.Body.String(), `"id":"resourceNotFound"`) || !strings.Contains(missing.Body.String(), "task") {
		t.Fatalf("missing task = %d %s", missing.Code, missing.Body.String())
	}

	unknown := request(t, handler, http.MethodPatch, "/api/v2/tasks/1", `{"data":{"type":"task","attributes":{"nope":true}}}`, testToken)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown attribute = %d %s", unknown.Code, unknown.Body.String())
	}

	invalid := request(t, handler, http.MethodPost, "/api/v2/prospects", `{"data":{"type":"prospect","attributes":{"firstName":"","emails":["a@b.example"]}}}`, testToken)
	if invalid.Code != http.StatusBadRequest && invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid prospect = %d %s", invalid.Code, invalid.Body.String())
	}

	conflict := request(t, handler, http.MethodPatch, "/api/v2/tasks/1", `{"data":{"type":"task","attributes":{"completed":true,"state":"incomplete"}}}`, testToken)
	if conflict.Code != http.StatusUnprocessableEntity || !strings.Contains(conflict.Body.String(), "disagree") {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestTwoOutreachInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-sequences.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPatch, "/api/v2/tasks/1", `{"data":{"type":"task","attributes":{"completed":true}}}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("complete = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/api/v2/tasks/1", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"completed":false`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
