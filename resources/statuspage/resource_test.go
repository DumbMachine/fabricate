package statuspage

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
	"github.com/dumbmachine/fabricate/resources/statuspage/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_statuspage_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "statuspage.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-status.v1.json")
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
	if state.Page.ID != "page-acme" || state.Page.Subdomain != "status.acme.example" {
		t.Fatalf("page = %+v", state.Page)
	}
	if len(state.Components) != 2 || len(state.Incidents) != 2 {
		t.Fatalf("components=%d incidents=%d", len(state.Components), len(state.Incidents))
	}
	if state.Components[0].Name != "Checkout" || state.Components[0].Status != "degraded_performance" {
		t.Fatalf("checkout = %+v", state.Components[0])
	}
	if state.Components[1].Name != "Order tracking" || state.Components[1].Status != "operational" {
		t.Fatalf("order tracking = %+v", state.Components[1])
	}
	if state.Incidents[0].Name != "Checkout double-charge on INV-4812" || state.Incidents[0].Status != "investigating" {
		t.Fatalf("open incident = %+v", state.Incidents[0])
	}
	if state.Incidents[1].Name != "NDR notifications delayed" || state.Incidents[1].Status != "resolved" || !strings.Contains(state.Incidents[1].Updates[0].Body, "10483") {
		t.Fatalf("resolved incident = %+v", state.Incidents[1])
	}
}

func TestMinimalScenarioRoundTripsEmptySlices(t *testing.T) {
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
	if !strings.Contains(string(after), `"components":[]`) || !strings.Contains(string(after), `"incidents":[]`) {
		t.Fatalf("empty slices were not dumped as arrays: %s", after)
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
		t.Fatal("expected unknown field error")
	}

	nested := loadScenario(t, "acme-status.v1.json")
	var acme map[string]any
	if err := json.Unmarshal(nested.State, &acme); err != nil {
		t.Fatal(err)
	}
	components := acme["components"].([]any)
	component := components[0].(map[string]any)
	component["nope"] = "x"
	raw, err = json.Marshal(acme)
	if err != nil {
		t.Fatal(err)
	}
	nested.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), nested); err == nil {
		t.Fatal("expected nested unknown field error")
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
	if len(seen) != 11 {
		t.Fatalf("compiled operation count = %d, want 11: %v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if resource.Descriptor().DisplayName != "Statuspage" || resource.Descriptor().ID != "statuspage" {
		t.Fatalf("descriptor = %+v", resource.Descriptor())
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-status.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Could not authenticate") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents", "", "other-token")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	page := request(t, handler, http.MethodGet, "/v1/pages/page-acme", "", testToken)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"subdomain":"status.acme.example"`) {
		t.Fatalf("page = %d %s", page.Code, page.Body.String())
	}

	components := request(t, handler, http.MethodGet, "/v1/pages/page-acme/components", "", testToken)
	if components.Code != http.StatusOK || !strings.Contains(components.Body.String(), `"name":"Checkout"`) || !strings.Contains(components.Body.String(), `"status":"degraded_performance"`) || !strings.Contains(components.Body.String(), `"name":"Order tracking"`) || !strings.Contains(components.Body.String(), `"status":"operational"`) {
		t.Fatalf("components = %d %s", components.Code, components.Body.String())
	}

	incidents := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents", "", testToken)
	if incidents.Code != http.StatusOK || !strings.Contains(incidents.Body.String(), "Checkout double-charge on INV-4812") || !strings.Contains(incidents.Body.String(), `"status":"investigating"`) || !strings.Contains(incidents.Body.String(), "NDR notifications delayed") || !strings.Contains(incidents.Body.String(), "10483") {
		t.Fatalf("incidents = %d %s", incidents.Code, incidents.Body.String())
	}

	unresolved := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents/unresolved", "", testToken)
	if unresolved.Code != http.StatusOK || !strings.Contains(unresolved.Body.String(), "inc-double-charge") || strings.Contains(unresolved.Body.String(), "inc-ndr-10483") {
		t.Fatalf("unresolved = %d %s", unresolved.Code, unresolved.Body.String())
	}

	got := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents/inc-ndr-10483", "", testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"resolved"`) || !strings.Contains(got.Body.String(), "order 10483") {
		t.Fatalf("get incident = %d %s", got.Code, got.Body.String())
	}

	found := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents?q=10483", "", testToken)
	if found.Code != http.StatusOK || !strings.Contains(found.Body.String(), "inc-ndr-10483") || strings.Contains(found.Body.String(), "inc-double-charge") {
		t.Fatalf("search = %d %s", found.Code, found.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1/pages/page-acme/incidents", `{"incident":{"name":"Checkout webhook timeouts","status":"identified","body":"Checkout webhooks are timing out.","component_ids":["comp-checkout"],"components":{"comp-checkout":"partial_outage"}}}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"inc-0001"`) || !strings.Contains(created.Body.String(), "timing out") {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}

	again := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"id":"inc-0001"`) || !strings.Contains(again.Body.String(), "Checkout webhook timeouts") {
		t.Fatalf("incidents after create = %d %s", again.Code, again.Body.String())
	}
	checkout := request(t, handler, http.MethodGet, "/v1/pages/page-acme/components/comp-checkout", "", testToken)
	if checkout.Code != http.StatusOK || !strings.Contains(checkout.Body.String(), `"status":"partial_outage"`) {
		t.Fatalf("checkout after create = %d %s", checkout.Code, checkout.Body.String())
	}
	persisted := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents/inc-0001", "", testToken)
	if persisted.Code != http.StatusOK || !strings.Contains(persisted.Body.String(), `"impact":"major"`) {
		t.Fatalf("created incident = %d %s", persisted.Code, persisted.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-status.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1/pages/page-acme/incidents/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "incident not found") {
		t.Fatalf("missing incident = %d %s", missing.Code, missing.Body.String())
	}
	missingPage := request(t, handler, http.MethodGet, "/v1/pages/other/incidents", "", testToken)
	if missingPage.Code != http.StatusNotFound || !strings.Contains(missingPage.Body.String(), "page not found") {
		t.Fatalf("missing page = %d %s", missingPage.Code, missingPage.Body.String())
	}
	unknown := request(t, handler, http.MethodPost, "/v1/pages/page-acme/incidents", `{"incident":{"name":"Ghost","component_ids":["nope"]}}`, testToken)
	if unknown.Code != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body.String(), "unknown component nope") {
		t.Fatalf("unknown component = %d %s", unknown.Code, unknown.Body.String())
	}
	invalid := request(t, handler, http.MethodPatch, "/v1/pages/page-acme/components/comp-checkout", `{"component":{"status":"degraded"}}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d %s", invalid.Code, invalid.Body.String())
	}
	empty := request(t, handler, http.MethodPost, "/v1/pages/page-acme/incidents", `{}`, testToken)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty create = %d %s", empty.Code, empty.Body.String())
	}
}

func TestTwoStatuspageInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-status.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/v1/pages/page-acme/incidents", `{"incident":{"name":"Isolated incident","body":"Only the first page sees this."}}`, testToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/pages/page-acme/incidents", "", testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Isolated incident") || !strings.Contains(untouched.Body.String(), "inc-double-charge") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
