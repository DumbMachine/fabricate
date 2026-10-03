package airbyte

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/airbyte/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_airbyte_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "airbyte.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-syncs.v1.json")
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
	if len(state.Sources) != 3 || len(state.Destinations) != 1 || len(state.Connections) != 2 || len(state.Jobs) != 2 {
		t.Fatalf("counts sources=%d destinations=%d connections=%d jobs=%d", len(state.Sources), len(state.Destinations), len(state.Connections), len(state.Jobs))
	}
	if state.Sources[0].SourceID != "source_shopify" || state.Sources[0].Configuration.Shop != "acme-goods.myshopify.com" {
		t.Fatalf("shopify source = %+v", state.Sources[0])
	}
	if state.Sources[1].SourceID != "source_razorpay" || state.Sources[2].SourceID != "source_shiprocket" {
		t.Fatalf("sources = %+v", state.Sources)
	}
	dest := state.Destinations[0]
	if dest.DestinationID != "dest_redshift" || dest.Configuration.Host != "acme-warehouse" || dest.Configuration.Database != "analytics" || dest.Configuration.Schema != "commerce" {
		t.Fatalf("destination = %+v", dest)
	}
	if state.Connections[0].ConnectionID != "conn_shopify_goods" || state.Connections[0].SourceID != "source_shopify" || state.Connections[0].DestinationID != "dest_redshift" {
		t.Fatalf("shopify connection = %+v", state.Connections[0])
	}
	if state.Connections[1].ConnectionID != "conn_razorpay" || state.Connections[1].SourceID != "source_razorpay" {
		t.Fatalf("razorpay connection = %+v", state.Connections[1])
	}
	if state.Jobs[0].JobID != "job_10482" || state.Jobs[0].Status != "succeeded" || state.Jobs[0].ConnectionID != "conn_shopify_goods" {
		t.Fatalf("succeeded job = %+v", state.Jobs[0])
	}
	failed := state.Jobs[1]
	if failed.JobID != "job_fail_ndr" || failed.Status != "failed" || failed.SourceID != "source_shiprocket" || !strings.Contains(failed.FailureReason, "shipment 10483") {
		t.Fatalf("failed job = %+v", failed)
	}
}

func TestMinimalScenarioDumpsEmptySlices(t *testing.T) {
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
	for _, key := range []string{"sources", "destinations", "connections", "jobs"} {
		if !bytes.Contains(dumped.State, []byte(`"`+key+`":[]`)) {
			t.Fatalf("dump missing empty %s: %s", key, dumped.State)
		}
	}
	if bytes.Contains(dumped.State, []byte("null")) {
		t.Fatalf("empty dump contains null: %s", dumped.State)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	codec := scenarioCodec{}
	unknown := doc
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	unknown.State = raw
	if err := codec.Validate(context.Background(), unknown); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	nested := doc
	delete(state, "extra")
	state["sources"] = []any{map[string]any{
		"sourceId": "source_shopify", "name": "Acme Goods Shopify", "sourceType": "shopify",
		"workspaceId": "ws_acme", "configuration": map[string]any{"sourceType": "shopify"}, "unexpected": true,
	}}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	nested.State = raw
	if err := codec.Validate(context.Background(), nested); err == nil {
		t.Fatal("expected unknown nested field to fail")
	}

	envelope := bytes.Replace(mustRead(t, "scenarios/minimal.v1.json"), []byte(`"state"`), []byte(`"extra": true, "state"`), 1)
	if _, err := scenario.Parse(envelope); err == nil {
		t.Fatal("expected unknown envelope field to fail")
	}
}

func TestScenarioValidationRejectsBadReferences(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["destinations"] = []any{map[string]any{
		"destinationId": "dest_redshift", "name": "Acme Redshift", "destinationType": "redshift", "workspaceId": "ws_acme",
		"configuration": map[string]any{"destinationType": "redshift", "host": "acme-warehouse", "database": "analytics", "schema": "commerce"},
	}}
	state["connections"] = []any{map[string]any{
		"connectionId": "conn_missing", "name": "Missing", "sourceId": "source_missing", "destinationId": "dest_redshift",
		"workspaceId": "ws_acme", "status": "active", "schedule": map[string]any{"scheduleType": "manual"},
		"dataResidency": "auto", "configurations": map[string]any{"streams": []any{map[string]any{"name": "orders"}}}, "createdAt": 1,
	}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	err = scenarioCodec{}.Validate(context.Background(), doc)
	if err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("dangling connection = %v", err)
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
	want := []string{"listSources", "getSource", "listDestinations", "getDestination", "listConnections", "getConnection", "listJobs", "getJob", "createJob"}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing operationId %s in %v", id, seen)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "airbyte" || descriptor.DisplayName != "Airbyte" || descriptor.Version != "v1" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "api.airbyte.com" || len(descriptor.HostPrefixes) != 0 {
		t.Fatalf("hosts = %+v prefixes = %+v", descriptor.ProviderHosts, descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.IDs(docs); len(got) != 2 || got[0] != "airbyte.acme-syncs.v1" || got[1] != "airbyte.minimal.v1" {
		t.Fatalf("scenario ids = %v", got)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	doc := loadScenario(t, "acme-syncs.v1.json")
	db := openTestDB(t, doc)
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/jobs", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1/jobs", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	sources := request(t, handler, http.MethodGet, "/v1/sources", "", testToken)
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"source_shopify"`) || !strings.Contains(sources.Body.String(), `"source_razorpay"`) || !strings.Contains(sources.Body.String(), `"source_shiprocket"`) {
		t.Fatalf("sources = %d %s", sources.Code, sources.Body.String())
	}
	if dataLen(t, sources.Body.Bytes()) != 3 {
		t.Fatalf("source count = %s", sources.Body.String())
	}

	shop := request(t, handler, http.MethodGet, "/v1/sources/source_shopify", "", testToken)
	if shop.Code != http.StatusOK || !strings.Contains(shop.Body.String(), `"acme-goods.myshopify.com"`) {
		t.Fatalf("shopify source = %d %s", shop.Code, shop.Body.String())
	}
	warehouse := request(t, handler, http.MethodGet, "/v1/destinations/dest_redshift", "", testToken)
	if warehouse.Code != http.StatusOK || !strings.Contains(warehouse.Body.String(), `"acme-warehouse"`) || !strings.Contains(warehouse.Body.String(), `"analytics"`) || !strings.Contains(warehouse.Body.String(), `"commerce"`) {
		t.Fatalf("destination = %d %s", warehouse.Code, warehouse.Body.String())
	}
	connections := request(t, handler, http.MethodGet, "/v1/connections", "", testToken)
	if connections.Code != http.StatusOK || dataLen(t, connections.Body.Bytes()) != 2 || !strings.Contains(connections.Body.String(), `"conn_shopify_goods"`) || !strings.Contains(connections.Body.String(), `"conn_razorpay"`) {
		t.Fatalf("connections = %d %s", connections.Code, connections.Body.String())
	}

	jobs := request(t, handler, http.MethodGet, "/v1/jobs", "", testToken)
	if jobs.Code != http.StatusOK || dataLen(t, jobs.Body.Bytes()) != 2 {
		t.Fatalf("jobs = %d %s", jobs.Code, jobs.Body.String())
	}
	failed := request(t, handler, http.MethodGet, "/v1/jobs/job_fail_ndr", "", testToken)
	if failed.Code != http.StatusOK || !strings.Contains(failed.Body.String(), `"source_shiprocket"`) || !strings.Contains(failed.Body.String(), "shipment 10483") || !strings.Contains(failed.Body.String(), `"failed"`) {
		t.Fatalf("failed job = %d %s", failed.Code, failed.Body.String())
	}
	succeeded := request(t, handler, http.MethodGet, "/v1/jobs/job_10482", "", testToken)
	if succeeded.Code != http.StatusOK || !strings.Contains(succeeded.Body.String(), `"succeeded"`) || !strings.Contains(succeeded.Body.String(), `"conn_shopify_goods"`) {
		t.Fatalf("succeeded job = %d %s", succeeded.Code, succeeded.Body.String())
	}
	failedOnly := request(t, handler, http.MethodGet, "/v1/jobs?status=failed", "", testToken)
	if failedOnly.Code != http.StatusOK || dataLen(t, failedOnly.Body.Bytes()) != 1 || !strings.Contains(failedOnly.Body.String(), `"job_fail_ndr"`) {
		t.Fatalf("failed filter = %d %s", failedOnly.Code, failedOnly.Body.String())
	}
	ordered := request(t, handler, http.MethodGet, "/v1/jobs?orderBy=createdAt%7CASC", "", testToken)
	if ordered.Code != http.StatusOK || !strings.Contains(ordered.Body.String(), `"job_fail_ndr"`) {
		t.Fatalf("order = %d %s", ordered.Code, ordered.Body.String())
	}
	var orderedJobs struct {
		Data []struct {
			JobID string `json:"jobId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(ordered.Body.Bytes(), &orderedJobs); err != nil {
		t.Fatal(err)
	}
	if len(orderedJobs.Data) != 2 || orderedJobs.Data[0].JobID != "job_fail_ndr" || orderedJobs.Data[1].JobID != "job_10482" {
		t.Fatalf("createdAt ASC = %+v", orderedJobs.Data)
	}

	created := request(t, handler, http.MethodPost, "/v1/jobs", `{"connectionId":"conn_shopify_goods","jobType":"sync"}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"jobId":"airbyte.job-0001"`) || !strings.Contains(created.Body.String(), `"running"`) || !strings.Contains(created.Body.String(), `"conn_shopify_goods"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/v1/jobs/airbyte.job-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"jobId":"airbyte.job-0001"`) || !strings.Contains(again.Body.String(), `"running"`) || !strings.Contains(again.Body.String(), `"2026-08-26T12:00:00Z"`) {
		t.Fatalf("get created = %d %s", again.Code, again.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/v1/jobs", "", testToken)
	if listed.Code != http.StatusOK || dataLen(t, listed.Body.Bytes()) != 3 || !strings.Contains(listed.Body.String(), `"airbyte.job-0001"`) {
		t.Fatalf("jobs after create = %d %s", listed.Code, listed.Body.String())
	}

	codec := scenarioCodec{}
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.Validate(context.Background(), dumped); err != nil {
		t.Fatal(err)
	}
	reloaded := openTestDB(t, dumped)
	dumpedAgain, err := codec.Dump(context.Background(), reloaded, dumped.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	left, _ := scenario.CanonicalJSON(dumped)
	right, _ := scenario.CanonicalJSON(dumpedAgain)
	if string(left) != string(right) {
		t.Fatalf("write dump did not round trip:\nfirst=%s\nsecond=%s", left, right)
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-syncs.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1/jobs/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "job not found") {
		t.Fatalf("missing job = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodGet, "/v1/jobs?limit=0", "", testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit = %d %s", invalid.Code, invalid.Body.String())
	}
	unknown := request(t, handler, http.MethodPost, "/v1/jobs", `{"connectionId":"conn_missing","jobType":"sync"}`, testToken)
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "connection not found") {
		t.Fatalf("unknown connection = %d %s", unknown.Code, unknown.Body.String())
	}
}

func TestTwoAirbyteInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-syncs.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/v1/jobs", `{"connectionId":"conn_razorpay","jobType":"sync"}`, testToken)
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/jobs/airbyte.job-0001", "", testToken)
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

func dataLen(t *testing.T, raw []byte) int {
	t.Helper()
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return len(body.Data)
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
