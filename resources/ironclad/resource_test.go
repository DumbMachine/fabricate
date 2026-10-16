package ironclad

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
	"github.com/dumbmachine/fabricate/resources/ironclad/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_ironclad_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ironclad.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-contracts.v1.json")
	resource := NewResource()
	loaded, err := resource.Scenario("ironclad.acme-contracts.v1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != doc.ID {
		t.Fatalf("embedded scenario = %s, want %s", loaded.ID, doc.ID)
	}
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
	if len(state.Workflows) != 3 || len(state.Records) != 2 || len(state.Comments) != 1 || len(state.Approvals) != 3 {
		t.Fatalf("counts workflows=%d records=%d comments=%d approvals=%d", len(state.Workflows), len(state.Records), len(state.Comments), len(state.Approvals))
	}
	byID := map[string]fixtureWorkflow{}
	for _, workflow := range state.Workflows {
		byID[workflow.ID] = workflow
	}
	fern := byID["wf-fernworks-msa"]
	if fern.Status != "completed" || fern.CounterpartyName != "Priya Nair" || fern.EnvelopeID != "env-fernworks-msa" || fern.Filename != "Fernworks-MSA.pdf" {
		t.Fatalf("fernworks workflow = %+v", fern)
	}
	tiny := byID["wf-tinyshop-cancel"]
	if tiny.Status != "active" || tiny.Step != "Review" || tiny.CounterpartyName != "Marco Silva" || tiny.EnvelopeID != "env-tinyshop-refund" || tiny.Filename != "TinyShop-cancellation.pdf" {
		t.Fatalf("tinyshop workflow = %+v", tiny)
	}
	delhivery := byID["wf-delhivery-msa"]
	if delhivery.Status != "active" || delhivery.AssigneeID != "user-iris" || len(delhivery.RecordIDs) != 0 || delhivery.EnvelopeID != "" {
		t.Fatalf("delhivery workflow = %+v", delhivery)
	}
	if state.CurrentUser.Email != "iris@acme.example" {
		t.Fatalf("current user = %+v", state.CurrentUser)
	}
}

func TestMinimalScenarioRoundTripsEmptyCollections(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
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
		t.Fatalf("load/dump changed the minimal baseline:\nbefore=%s\nafter=%s", before, after)
	}
	for _, collection := range []string{`"workflows":[]`, `"records":[]`, `"comments":[]`, `"approvals":[]`} {
		if !strings.Contains(string(after), collection) {
			t.Fatalf("minimal dump missing %s: %s", collection, after)
		}
	}
}

func TestUnknownScenarioFieldsFail(t *testing.T) {
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
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail validation")
	}

	doc = loadScenario(t, "acme-contracts.v1.json")
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	workflows := state["workflows"].([]any)
	first := workflows[0].(map[string]any)
	first["unexpected"] = "nope"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown workflow field to fail validation")
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
	descriptor := resource.Descriptor()
	if descriptor.ID != "ironclad" || descriptor.DisplayName != "Ironclad" || descriptor.Version != "v1" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "na1.ironcladapp.com" {
		t.Fatalf("hosts = %#v", descriptor.ProviderHosts)
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
	if len(seen) != 9 {
		t.Fatalf("compiled operation count = %d, want 9: %#v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-contracts.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/public/api/v1/workflows", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/public/api/v1/workflows", "", "other-token")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	active := request(t, handler, http.MethodGet, "/public/api/v1/workflows", "", testToken)
	if active.Code != http.StatusOK || !strings.Contains(active.Body.String(), `"wf-delhivery-msa"`) || !strings.Contains(active.Body.String(), `"wf-tinyshop-cancel"`) || strings.Contains(active.Body.String(), `"wf-fernworks-msa"`) {
		t.Fatalf("active workflows = %d %s", active.Code, active.Body.String())
	}
	if strings.Index(active.Body.String(), `"wf-delhivery-msa"`) > strings.Index(active.Body.String(), `"wf-tinyshop-cancel"`) {
		t.Fatalf("active workflow order = %s", active.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/public/api/v1/workflows?status=active&status=completed", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"wf-fernworks-msa"`) || !strings.Contains(listed.Body.String(), `"count":3`) {
		t.Fatalf("listed workflows = %d %s", listed.Code, listed.Body.String())
	}

	fern := request(t, handler, http.MethodGet, "/public/api/v1/workflows/wf-fernworks-msa", "", testToken)
	if fern.Code != http.StatusOK || !strings.Contains(fern.Body.String(), `"Priya Nair"`) || !strings.Contains(fern.Body.String(), `"env-fernworks-msa"`) || !strings.Contains(fern.Body.String(), `"Fernworks-MSA.pdf"`) || !strings.Contains(fern.Body.String(), `"completed"`) {
		t.Fatalf("fernworks = %d %s", fern.Code, fern.Body.String())
	}
	byReadable := request(t, handler, http.MethodGet, "/public/api/v1/workflows/IC-204", "", testToken)
	if byReadable.Code != http.StatusOK || !strings.Contains(byReadable.Body.String(), `"wf-fernworks-msa"`) {
		t.Fatalf("readable workflow = %d %s", byReadable.Code, byReadable.Body.String())
	}

	record := request(t, handler, http.MethodGet, "/public/api/v1/records/rec-fernworks-msa", "", testToken)
	if record.Code != http.StatusOK || !strings.Contains(record.Body.String(), `"env-fernworks-msa"`) || !strings.Contains(record.Body.String(), `"Fernworks-MSA.pdf"`) {
		t.Fatalf("record = %d %s", record.Code, record.Body.String())
	}
	tiny := request(t, handler, http.MethodGet, "/public/api/v1/records/IC-R209", "", testToken)
	if tiny.Code != http.StatusOK || !strings.Contains(tiny.Body.String(), `"env-tinyshop-refund"`) || !strings.Contains(tiny.Body.String(), `"TinyShop-cancellation.pdf"`) || !strings.Contains(tiny.Body.String(), `"Marco Silva"`) {
		t.Fatalf("tinyshop record = %d %s", tiny.Code, tiny.Body.String())
	}

	approvals := request(t, handler, http.MethodGet, "/public/api/v1/workflows/wf-delhivery-msa/approvals", "", testToken)
	if approvals.Code != http.StatusOK || !strings.Contains(approvals.Body.String(), `"iris@acme.example"`) || !strings.Contains(approvals.Body.String(), `"pending"`) {
		t.Fatalf("approvals = %d %s", approvals.Code, approvals.Body.String())
	}

	comment := request(t, handler, http.MethodPost, "/public/api/v1/workflows/wf-delhivery-msa/comments", `{"comment":"Iris, please review the Delhivery MSA."}`, testToken)
	if comment.Code != http.StatusOK || !strings.Contains(comment.Body.String(), "Delhivery MSA") || !strings.Contains(comment.Body.String(), `"iris@acme.example"`) {
		t.Fatalf("comment = %d %s", comment.Code, comment.Body.String())
	}
	comments := request(t, handler, http.MethodGet, "/public/api/v1/workflows/wf-delhivery-msa/comments", "", testToken)
	if comments.Code != http.StatusOK || !strings.Contains(comments.Body.String(), "please review the Delhivery MSA") {
		t.Fatalf("comments after write = %d %s", comments.Code, comments.Body.String())
	}

	launched := request(t, handler, http.MethodPost, "/public/api/v1/workflows", `{"template":"tmpl-msa","attributes":{"counterpartyName":"Harbor Capital"}}`, testToken)
	if launched.Code != http.StatusOK || !strings.Contains(launched.Body.String(), `"ironclad.workflow-0001"`) || !strings.Contains(launched.Body.String(), `"Harbor Capital"`) {
		t.Fatalf("launch = %d %s", launched.Code, launched.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/public/api/v1/workflows", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"ironclad.workflow-0001"`) {
		t.Fatalf("list after launch = %d %s", again.Code, again.Body.String())
	}

	updated := request(t, handler, http.MethodPatch, "/public/api/v1/records/rec-tinyshop-cancel", `{"name":"TinyShop cancellation reviewed"}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"TinyShop cancellation reviewed"`) {
		t.Fatalf("update record = %d %s", updated.Code, updated.Body.String())
	}
	reread := request(t, handler, http.MethodGet, "/public/api/v1/records/rec-tinyshop-cancel", "", testToken)
	if reread.Code != http.StatusOK || !strings.Contains(reread.Body.String(), `"TinyShop cancellation reviewed"`) || !strings.Contains(reread.Body.String(), `"2026-08-26T12:00:00Z"`) {
		t.Fatalf("record after update = %d %s", reread.Code, reread.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-contracts.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/public/api/v1/workflows/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"workflow does not exist"`) {
		t.Fatalf("missing workflow = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/public/api/v1/workflows/wf-delhivery-msa/comments", `{"comment":""}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("empty comment = %d %s", invalid.Code, invalid.Body.String())
	}
	removed := request(t, handler, http.MethodPatch, "/public/api/v1/records/rec-fernworks-msa", `{"removeProperties":["counterpartyName"]}`, testToken)
	if removed.Code != http.StatusBadRequest || !strings.Contains(removed.Body.String(), "cannot be removed") {
		t.Fatalf("remove counterparty = %d %s", removed.Code, removed.Body.String())
	}
}

func TestTwoIroncladInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-contracts.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/public/api/v1/workflows/wf-delhivery-msa/comments", `{"comment":"Only on the first instance."}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("comment = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/public/api/v1/workflows/wf-delhivery-msa/comments", "", testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Only on the first instance") || !strings.Contains(untouched.Body.String(), `"count":0`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
