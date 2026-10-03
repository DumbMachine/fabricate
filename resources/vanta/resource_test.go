package vanta

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/vanta/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_vanta_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "vanta.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func TestScenariosValidateAndRoundTrip(t *testing.T) {
	codec := scenarioCodec{}
	for _, name := range []string{"minimal.v1.json", "acme-compliance.v1.json"} {
		t.Run(name, func(t *testing.T) {
			doc := loadScenario(t, name)
			if err := codec.Validate(context.Background(), doc); err != nil {
				t.Fatal(err)
			}
			reordered := doc
			reordered.State = reverseObjectKeys(t, doc.State)
			before, err := scenario.CanonicalJSON(doc)
			if err != nil {
				t.Fatal(err)
			}
			reorderedCanonical, err := scenario.CanonicalJSON(reordered)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(reorderedCanonical) {
				t.Fatal("canonical JSON changed when object key order changed")
			}
			db := openTestDB(t, reordered)
			dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
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
		})
	}

	doc := loadScenario(t, "acme-compliance.v1.json")
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Vendors) != 2 || len(state.SecurityReviews) != 2 || len(state.Tests) != 1 || len(state.Entities) != 1 || len(state.Documents) != 1 || len(state.Uploads) != 1 {
		t.Fatalf("record counts vendors=%d reviews=%d tests=%d entities=%d documents=%d uploads=%d",
			len(state.Vendors), len(state.SecurityReviews), len(state.Tests), len(state.Entities), len(state.Documents), len(state.Uploads))
	}

	minimal := loadScenario(t, "minimal.v1.json")
	minimalDB := openTestDB(t, minimal)
	minimalDump, err := codec.Dump(context.Background(), minimalDB, minimal.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := scenario.CanonicalJSON(minimalDump)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"vendors", "securityReviews", "tests", "entities", "documents", "uploads"} {
		if !bytes.Contains(canonical, []byte(`"`+key+`":[]`)) {
			t.Fatalf("empty %s did not dump as []: %s", key, canonical)
		}
	}
}

func TestUnknownScenarioFieldsFail(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "acme-compliance.v1.json")
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
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	doc = loadScenario(t, "acme-compliance.v1.json")
	state = map[string]any{}
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	vendors := state["vendors"].([]any)
	vendors[0].(map[string]any)["unexpected"] = "nope"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown vendor field to fail")
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
	if len(seen) != 10 {
		t.Fatalf("compiled operation count = %d, want 10", len(seen))
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "vanta" || descriptor.DisplayName != "Vanta" || descriptor.Version != "v1" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "api.vanta.com" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(scenario.IDs(docs), ","), "vanta.acme-compliance.v1,vanta.minimal.v1"; got != want {
		t.Fatalf("scenario ids = %s", got)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-compliance.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/vendors", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1/vendors", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	vendors := request(t, handler, http.MethodGet, "/v1/vendors", "", testToken)
	if vendors.Code != http.StatusOK || !strings.Contains(vendors.Body.String(), `"Shiprocket"`) || !strings.Contains(vendors.Body.String(), `"Razorpay"`) {
		t.Fatalf("vendors = %d %s", vendors.Code, vendors.Body.String())
	}
	if !strings.Contains(vendors.Body.String(), `"APPROVED"`) || !strings.Contains(vendors.Body.String(), `"latestDecision":null`) {
		t.Fatalf("vendor decisions = %s", vendors.Body.String())
	}

	shiprocket := request(t, handler, http.MethodGet, "/v1/vendors?name=ship", "", testToken)
	if shiprocket.Code != http.StatusOK || strings.Contains(shiprocket.Body.String(), `"Razorpay"`) || !strings.Contains(shiprocket.Body.String(), `"Shiprocket"`) {
		t.Fatalf("name filter = %d %s", shiprocket.Code, shiprocket.Body.String())
	}

	reviews := request(t, handler, http.MethodGet, "/v1/vendors/vendor-shiprocket/security-reviews", "", testToken)
	if reviews.Code != http.StatusOK || !strings.Contains(reviews.Body.String(), `"decision":null`) || !strings.Contains(reviews.Body.String(), "Security review is open") {
		t.Fatalf("shiprocket reviews = %d %s", reviews.Code, reviews.Body.String())
	}

	tests := request(t, handler, http.MethodGet, "/v1/tests", "", testToken)
	if tests.Code != http.StatusOK || !strings.Contains(tests.Body.String(), `"Access review for Shopify admin"`) || !strings.Contains(tests.Body.String(), `"NEEDS_ATTENTION"`) {
		t.Fatalf("tests = %d %s", tests.Code, tests.Body.String())
	}
	passing := request(t, handler, http.MethodGet, "/v1/tests?statusFilter=OK", "", testToken)
	if passing.Code != http.StatusOK || !strings.Contains(passing.Body.String(), `"data":[]`) {
		t.Fatalf("passing tests = %d %s", passing.Code, passing.Body.String())
	}

	got := request(t, handler, http.MethodGet, "/v1/tests/shopify-admin-access-review", "", testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "old.contractor@acme.example") || !strings.Contains(got.Body.String(), "access-review-2026-08.pdf") {
		t.Fatalf("get test = %d %s", got.Code, got.Body.String())
	}

	entities := request(t, handler, http.MethodGet, "/v1/tests/shopify-admin-access-review/entities", "", testToken)
	if entities.Code != http.StatusOK || !strings.Contains(entities.Body.String(), `"displayName":"old.contractor@acme.example"`) || !strings.Contains(entities.Body.String(), `"FAILING"`) {
		t.Fatalf("entities = %d %s", entities.Code, entities.Body.String())
	}

	uploads := request(t, handler, http.MethodGet, "/v1/documents/doc-shopify-admin-access/uploads", "", testToken)
	if uploads.Code != http.StatusOK || !strings.Contains(uploads.Body.String(), `"fileName":"access-review-2026-08.pdf"`) {
		t.Fatalf("uploads = %d %s", uploads.Code, uploads.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/v1/vendors/missing", "", testToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing vendor = %d %s", missing.Code, missing.Body.String())
	}
	invalidPage := request(t, handler, http.MethodGet, "/v1/vendors?pageSize=0", "", testToken)
	if invalidPage.Code != http.StatusBadRequest {
		t.Fatalf("pageSize = %d %s", invalidPage.Code, invalidPage.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1/documents", `{"title":"NDR evidence note","description":"Shipment 10483 NDR packet.","timeSensitivity":"MOST_RECENT","cadence":"P0D","reminderWindow":"P0D","isSensitive":false}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"vanta.document-0001"`) || !strings.Contains(created.Body.String(), `"Needs document"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/v1/documents/vanta.document-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"NDR evidence note"`) {
		t.Fatalf("persisted document = %d %s", again.Code, again.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/v1/documents", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"vanta.document-0001"`) || !strings.Contains(listed.Body.String(), `"doc-shopify-admin-access"`) {
		t.Fatalf("documents after create = %d %s", listed.Code, listed.Body.String())
	}

	dumped, err := (scenarioCodec{}).Dump(context.Background(), db, loadScenario(t, "acme-compliance.v1.json").Metadata())
	if err != nil {
		t.Fatal(err)
	}
	if err := (scenarioCodec{}).Validate(context.Background(), dumped); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dumped.State, []byte("NDR evidence note")) {
		t.Fatalf("dump lost the created document: %s", dumped.State)
	}
}

func TestTwoVantaInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-compliance.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/v1/documents", `{"title":"Only on the first instance","description":"Isolation check.","timeSensitivity":"MOST_RECENT","cadence":"P0D","reminderWindow":"P0D","isSensitive":false}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/documents/vanta.document-0001", "", testToken)
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

func reverseObjectKeys(t *testing.T, raw []byte) []byte {
	t.Helper()
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(encoded)
		buf.WriteByte(':')
		buf.Write(state[key])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}
