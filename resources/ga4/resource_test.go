package ga4

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
	"github.com/dumbmachine/fabricate/resources/ga4/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_ga4_test_token"

const goodsReportBody = `{"dimensions":[{"name":"transactionId"},{"name":"sessionSource"},{"name":"sessionMedium"},{"name":"sessionCampaignName"},{"name":"eventName"}],"metrics":[{"name":"eventCount"},{"name":"purchaseRevenue"},{"name":"itemRefundAmount"}],"dateRanges":[{"startDate":"2026-08-01","endDate":"2026-08-26"}]}`

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ga4.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	server, err := NewResource().NewServer(context.Background(), httpresource.ServerDependencies{
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
	doc := loadScenario(t, "acme-properties.v1.json")
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
	if len(state.Properties) != 2 || len(state.Events) != 3 || len(state.AudienceExports) != 0 {
		t.Fatalf("counts properties=%d events=%d exports=%d", len(state.Properties), len(state.Events), len(state.AudienceExports))
	}
	byID := map[string]fixtureEvent{}
	for _, event := range state.Events {
		byID[event.TransactionID] = event
	}
	if byID["10482"].SessionSource != "whatsapp" || byID["10482"].SessionMedium != "gupshup" || byID["10482"].SessionCampaignName != "monsoon-tee" || byID["10482"].EventName != "purchase" {
		t.Fatalf("10482 = %+v", byID["10482"])
	}
	if byID["10483"].SessionSource != "google" || byID["10483"].SessionMedium != "cpc" || byID["10483"].EventName != "purchase" {
		t.Fatalf("10483 = %+v", byID["10483"])
	}
	if byID["10484"].EventName != "refund" || byID["10484"].ItemRefundAmount != 1499 {
		t.Fatalf("10484 = %+v", byID["10484"])
	}
	if state.Properties[0].DisplayName != "Acme App" || state.Properties[1].DisplayName != "Acme Goods" {
		t.Fatalf("properties = %+v", state.Properties)
	}
}

func TestMinimalScenarioDumpsEmptyArrays(t *testing.T) {
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
		t.Fatalf("minimal round trip\nbefore=%s\nafter=%s", before, after)
	}
	for _, field := range []string{`"properties":[]`, `"events":[]`, `"audienceExports":[]`} {
		if !strings.Contains(string(dumped.State), field) {
			t.Fatalf("dump missing %s: %s", field, dumped.State)
		}
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"properties":[],"events":[],"audienceExports":[],"extra":true}`)
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	acme := loadScenario(t, "acme-properties.v1.json")
	acme.State = bytesReplace(t, acme.State, `"displayName": "Acme App"`, `"displayName": "Acme App", "extra": 1`)
	if err := (scenarioCodec{}).Validate(context.Background(), acme); err == nil {
		t.Fatal("expected unknown property field to fail")
	}
}

func bytesReplace(t *testing.T, raw []byte, old, new string) []byte {
	t.Helper()
	if !strings.Contains(string(raw), old) {
		t.Fatalf("pattern %s not in state", old)
	}
	return []byte(strings.Replace(string(raw), old, new, 1))
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
	want := []string{
		"properties.runReport",
		"properties.batchRunReports",
		"properties.runRealtimeReport",
		"properties.checkCompatibility",
		"properties.getMetadata",
		"properties.audienceExports.list",
		"properties.audienceExports.get",
		"properties.audienceExports.create",
		"properties.audienceExports.query",
	}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing operationId %s in %v", id, seen)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "ga4" || descriptor.DisplayName != "Google Analytics 4" || descriptor.Version != "v1beta" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "analyticsdata.googleapis.com" || len(descriptor.HostPrefixes) != 0 {
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
	if got, want := strings.Join(scenario.IDs(docs), ","), "ga4.acme-properties.v1,ga4.minimal.v1"; got != want {
		t.Fatalf("scenarios = %s", got)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	doc := loadScenario(t, "acme-properties.v1.json")
	db := openTestDB(t, doc)
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport", goodsReportBody, "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport", goodsReportBody, "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	report := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport", goodsReportBody, testToken)
	if report.Code != http.StatusOK {
		t.Fatalf("runReport = %d %s", report.Code, report.Body.String())
	}
	rows := decodeReport(t, report.Body.Bytes())
	if rows.Kind != "analyticsData#runReport" || rows.RowCount != 3 || rows.Metadata.CurrencyCode != "INR" || rows.Metadata.TimeZone != "Asia/Kolkata" {
		t.Fatalf("report header = %+v", rows)
	}
	got := map[string]decodedRow{}
	for _, row := range rows.Rows {
		got[row.DimensionValues[0].Value] = row
	}
	assertDims(t, got["10482"], "10482", "whatsapp", "gupshup", "monsoon-tee", "purchase")
	assertDims(t, got["10483"], "10483", "google", "cpc", "(not set)", "purchase")
	assertDims(t, got["10484"], "10484", "(not set)", "(not set)", "(not set)", "refund")
	if got["10482"].MetricValues[1].Value != "3097" || got["10484"].MetricValues[2].Value != "1499" {
		t.Fatalf("metrics = %+v %+v", got["10482"].MetricValues, got["10484"].MetricValues)
	}

	created := request(t, handler, http.MethodPost, "/v1beta/properties/309712345/audienceExports",
		`{"audience":"properties/309712345/audiences/purchasers","dimensions":[{"dimensionName":"transactionId"}]}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"done":true`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var operation struct {
		Name     string `json:"name"`
		Response struct {
			Name string `json:"name"`
		} `json:"response"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Response.Name != "properties/309712345/audienceExports/ga4.audienceExport-0001" {
		t.Fatalf("export name = %s", operation.Response.Name)
	}
	again := request(t, handler, http.MethodGet, "/v1beta/"+operation.Response.Name, "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"state":"ACTIVE"`) || !strings.Contains(again.Body.String(), `"audience":"properties/309712345/audiences/purchasers"`) {
		t.Fatalf("get export = %d %s", again.Code, again.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/v1beta/properties/309712345/audienceExports", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), operation.Response.Name) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	queried := request(t, handler, http.MethodPost, "/v1beta/"+operation.Response.Name+":query", `{}`, testToken)
	if queried.Code != http.StatusOK || !strings.Contains(queried.Body.String(), `"audienceRows":[]`) {
		t.Fatalf("query = %d %s", queried.Code, queried.Body.String())
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
	if len(state.AudienceExports) != 1 || state.AudienceExports[0].Name != operation.Response.Name {
		t.Fatalf("dumped exports = %+v", state.AudienceExports)
	}
	reloaded := newTestHandler(t, openTestDB(t, dumped), &testIDs{})
	persisted := request(t, reloaded, http.MethodGet, "/v1beta/"+operation.Response.Name, "", testToken)
	if persisted.Code != http.StatusOK || !strings.Contains(persisted.Body.String(), `"transactionId"`) {
		t.Fatalf("reloaded = %d %s", persisted.Code, persisted.Body.String())
	}
}

func TestReportFiltersAndBatch(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "acme-properties.v1.json")), &testIDs{})
	oneDay := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport",
		`{"dimensions":[{"name":"transactionId"}],"metrics":[{"name":"eventCount"}],"dateRanges":[{"startDate":"2026-08-25","endDate":"2026-08-25"}]}`, testToken)
	rows := decodeReport(t, oneDay.Body.Bytes())
	if oneDay.Code != http.StatusOK || rows.RowCount != 1 || rows.Rows[0].DimensionValues[0].Value != "10482" {
		t.Fatalf("one day = %d %+v", oneDay.Code, rows)
	}
	refunds := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport",
		`{"dimensions":[{"name":"transactionId"},{"name":"eventName"}],"metrics":[{"name":"itemRefundAmount"}],"dateRanges":[{"startDate":"7daysAgo","endDate":"today"}],"dimensionFilter":{"filter":{"fieldName":"eventName","stringFilter":{"matchType":"EXACT","value":"refund"}}}}`, testToken)
	filtered := decodeReport(t, refunds.Body.Bytes())
	if refunds.Code != http.StatusOK || filtered.RowCount != 1 || filtered.Rows[0].DimensionValues[0].Value != "10484" {
		t.Fatalf("refund filter = %d %+v", refunds.Code, filtered)
	}
	app := request(t, handler, http.MethodPost, "/v1beta/properties/309700001:runReport", goodsReportBody, testToken)
	empty := decodeReport(t, app.Body.Bytes())
	if app.Code != http.StatusOK || empty.RowCount != 0 || empty.Metadata.CurrencyCode != "USD" || len(empty.Rows) != 0 {
		t.Fatalf("acme app = %d %+v", app.Code, empty)
	}
	batch := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:batchRunReports",
		`{"requests":[{"dimensions":[{"name":"transactionId"}],"metrics":[{"name":"transactions"}],"dateRanges":[{"startDate":"2026-08-01","endDate":"2026-08-26"}]},{"dimensions":[{"name":"eventName"}],"metrics":[{"name":"eventCount"}],"dateRanges":[{"startDate":"2026-08-01","endDate":"2026-08-26"}]}]}`, testToken)
	if batch.Code != http.StatusOK || !strings.Contains(batch.Body.String(), `"kind":"analyticsData#batchRunReports"`) {
		t.Fatalf("batch = %d %s", batch.Code, batch.Body.String())
	}
	var batchBody struct {
		Reports []decodedReport `json:"reports"`
	}
	if err := json.Unmarshal(batch.Body.Bytes(), &batchBody); err != nil {
		t.Fatal(err)
	}
	if len(batchBody.Reports) != 2 || batchBody.Reports[0].RowCount != 3 || batchBody.Reports[1].RowCount != 2 {
		t.Fatalf("batch reports = %+v", batchBody.Reports)
	}
}

func TestMetadataCompatibilityAndErrors(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "acme-properties.v1.json")), &testIDs{})
	metadata := request(t, handler, http.MethodGet, "/v1beta/properties/309712345/metadata", "", testToken)
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"name":"properties/309712345/metadata"`) || !strings.Contains(metadata.Body.String(), `"apiName":"transactionId"`) {
		t.Fatalf("metadata = %d %s", metadata.Code, metadata.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/v1beta/properties/000/metadata", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "NOT_FOUND") {
		t.Fatalf("missing property = %d %s", missing.Code, missing.Body.String())
	}
	compat := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:checkCompatibility",
		`{"dimensions":[{"name":"transactionId"},{"name":"notADimension"}],"metrics":[{"name":"eventCount"}]}`, testToken)
	if compat.Code != http.StatusOK || !strings.Contains(compat.Body.String(), `"COMPATIBLE"`) || !strings.Contains(compat.Body.String(), `"INCOMPATIBLE"`) {
		t.Fatalf("compat = %d %s", compat.Code, compat.Body.String())
	}
	unknown := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runReport",
		`{"metrics":[{"name":"eventCount"}],"dateRanges":[{"startDate":"2026-08-01","endDate":"2026-08-26"}],"cohortSpec":{}}`, testToken)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", unknown.Code, unknown.Body.String())
	}
	realtime := request(t, handler, http.MethodPost, "/v1beta/properties/309712345:runRealtimeReport",
		`{"dimensions":[{"name":"eventName"}],"metrics":[{"name":"eventCount"}]}`, testToken)
	live := decodeReport(t, realtime.Body.Bytes())
	if realtime.Code != http.StatusOK || live.Kind != kindRealtime || live.RowCount != 0 {
		t.Fatalf("realtime = %d %+v", realtime.Code, live)
	}
}

func TestTwoGA4InstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-properties.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/v1beta/properties/309712345/audienceExports",
		`{"audience":"properties/309712345/audiences/purchasers","dimensions":[{"dimensionName":"eventName"}]}`, testToken)
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1beta/properties/309712345/audienceExports", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"audienceExports":[]`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

type decodedValue struct {
	Value string `json:"value"`
}

type decodedRow struct {
	DimensionValues []decodedValue `json:"dimensionValues"`
	MetricValues    []decodedValue `json:"metricValues"`
}

type decodedReport struct {
	Kind     string `json:"kind"`
	RowCount int    `json:"rowCount"`
	Rows     []decodedRow
	Metadata struct {
		CurrencyCode string `json:"currencyCode"`
		TimeZone     string `json:"timeZone"`
	} `json:"metadata"`
}

func decodeReport(t *testing.T, raw []byte) decodedReport {
	t.Helper()
	var report decodedReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v body=%s", err, raw)
	}
	return report
}

func assertDims(t *testing.T, row decodedRow, want ...string) {
	t.Helper()
	if len(row.DimensionValues) != len(want) {
		t.Fatalf("dims = %+v want %v", row.DimensionValues, want)
	}
	for i, value := range want {
		if row.DimensionValues[i].Value != value {
			t.Fatalf("dim %d = %q want %q in %+v", i, row.DimensionValues[i].Value, value, row.DimensionValues)
		}
	}
}
