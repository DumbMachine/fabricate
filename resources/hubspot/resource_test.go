package hubspot

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
	"github.com/dumbmachine/fabricate/resources/hubspot/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_hubspot_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hubspot.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	var stalled *fixtureObject
	for i := range state.Deals {
		if state.Deals[i].ID == "301" {
			stalled = &state.Deals[i]
			break
		}
	}
	if stalled == nil || stalled.Properties["dealstage"] != "appointmentscheduled" || stalled.Properties["invoice"] != "INV-4812" {
		t.Fatalf("stalled INV-4812 deal missing: %+v", state.Deals)
	}
}

func TestCompiledOpenAPIContractIsValid(t *testing.T) {
	resource := NewResource()
	spec, err := generated.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	// HubSpot's published CRM documents include constructs kin-openapi
	// rejects (for example extra operation siblings). The stored JSON stays
	// unmodified; stateful tests below cover the implemented operations.
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
	if len(seen) != 20 {
		t.Fatalf("compiled operation count = %d, want 20", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-pipeline.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/crm/v3/objects/contacts", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	contacts := request(t, handler, http.MethodGet, "/crm/v3/objects/contacts", "", testToken)
	if contacts.Code != http.StatusOK || !strings.Contains(contacts.Body.String(), `"dana@northwind.example"`) {
		t.Fatalf("contacts = %d %s", contacts.Code, contacts.Body.String())
	}

	aliased := request(t, handler, http.MethodGet, "/crm/v3/objects/deals/301", "", testToken)
	canonical := request(t, handler, http.MethodGet, "/crm/v3/objects/0-3/301", "", testToken)
	if aliased.Code != http.StatusOK || canonical.Code != http.StatusOK || !strings.Contains(aliased.Body.String(), "Northwind checkout expansion") {
		t.Fatalf("deal alias = %d %s canonical = %d %s", aliased.Code, aliased.Body.String(), canonical.Code, canonical.Body.String())
	}

	updated := request(t, handler, http.MethodPatch, "/crm/v3/objects/deals/301", `{"properties":{"dealstage":"closedwon"}}`, testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"closedwon"`) {
		t.Fatalf("update deal = %d %s", updated.Code, updated.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/crm/v3/objects/tickets", `{"properties":{"subject":"Follow up with Northwind","hs_ticket_priority":"MEDIUM"}}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"hubspot.tickets-0001"`) {
		t.Fatalf("create ticket = %d %s", created.Code, created.Body.String())
	}

	found := request(t, handler, http.MethodPost, "/crm/v3/objects/contacts/search", `{"query":"dana@northwind.example"}`, testToken)
	if found.Code != http.StatusOK || !strings.Contains(found.Body.String(), `"total":1`) {
		t.Fatalf("search = %d %s", found.Code, found.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-pipeline.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/crm/v3/objects/contacts/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"message":"contact not found"`) {
		t.Fatalf("missing contact = %d %s", missing.Code, missing.Body.String())
	}
}

func TestTwoHubSpotInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-pipeline.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPatch, "/crm/v3/objects/deals/301", `{"properties":{"dealstage":"closedwon"}}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("update = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/crm/v3/objects/deals/301", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"appointmentscheduled"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
