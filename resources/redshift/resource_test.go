package redshift

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
	"github.com/dumbmachine/fabricate/resources/redshift/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_redshift_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "redshift.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func postTarget(t *testing.T, handler http.Handler, path, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	if target != "" {
		req.Header.Set("X-Amz-Target", target)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %d %s: %v", recorder.Code, recorder.Body.String(), err)
	}
	return body
}

func stringValues(value any) []string {
	var out []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		case string:
			out = append(out, typed)
		}
	}
	walk(value)
	return out
}

func hasAll(values []string, want ...string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range want {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-warehouse.v1.json")
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
	if len(state.Orders) != 3 || len(state.Payments) != 3 || len(state.Shipments) != 3 || len(state.SaasInvoices) != 1 {
		t.Fatalf("record counts orders=%d payments=%d shipments=%d saas=%d", len(state.Orders), len(state.Payments), len(state.Shipments), len(state.SaasInvoices))
	}
	invoice := state.SaasInvoices[0]
	if invoice.InvoiceID != "INV-4812" || invoice.AmountUSD != 1240 || invoice.Currency != "USD" || invoice.PaymentID != "pay_saas_4812" || invoice.DuplicatePaymentID != "pay_saas_4812b" {
		t.Fatalf("INV-4812 row = %+v", invoice)
	}
	docs, err := NewResource().ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]struct{}{}
	for _, embedded := range docs {
		ids[embedded.ID] = struct{}{}
	}
	if _, ok := ids["redshift.minimal.v1"]; !ok {
		t.Fatal("missing embedded minimal scenario")
	}
	if _, ok := ids["redshift.acme-warehouse.v1"]; !ok {
		t.Fatal("missing embedded acme scenario")
	}
}

func TestMinimalScenarioDumpsEmptySlices(t *testing.T) {
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
	text := string(after)
	for _, key := range []string{"orders", "payments", "shipments", "saasInvoices"} {
		if !strings.Contains(text, `"`+key+`":[]`) {
			t.Fatalf("dumped minimal state missing empty %s: %s", key, text)
		}
	}
}

func TestUnknownFieldsFail(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = encoded
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["extra"] = true
	encoded, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.Parse(encoded); err == nil {
		t.Fatal("expected unknown envelope field to fail")
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
	want := []string{"ExecuteStatement", "DescribeStatement", "GetStatementResult", "ListStatements"}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing operationId %s in %v", id, seen)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "redshift" || descriptor.DisplayName != "Amazon Redshift" || descriptor.Version != "2012-12-01" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "redshift-data.us-east-1.amazonaws.com" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-warehouse.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	createdAt := float64(time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Unix())

	unauthorized := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders"}`, "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "invalid synthetic bearer token") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	executed := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","DbUser":"acme","Sql":"SELECT * FROM commerce.orders"}`, testToken)
	if executed.Code != http.StatusOK {
		t.Fatalf("execute = %d %s", executed.Code, executed.Body.String())
	}
	if ct := executed.Header().Get("Content-Type"); ct != "application/x-amz-json-1.1" {
		t.Fatalf("content type = %s", ct)
	}
	body := decodeBody(t, executed)
	if body["Status"] != "FINISHED" || body["Id"] != "statement-0001" || body["ClusterIdentifier"] != "acme-warehouse" || body["Database"] != "analytics" || body["HasResultSet"] != true {
		t.Fatalf("execute body = %s", executed.Body.String())
	}
	if body["CreatedAt"] != createdAt || body["RedshiftPid"] != float64(48001) {
		t.Fatalf("execute timestamps = %s", executed.Body.String())
	}
	id := body["Id"].(string)

	described := postTarget(t, handler, "/", "RedshiftData.DescribeStatement", `{"Id":"`+id+`"}`, testToken)
	if described.Code != http.StatusOK {
		t.Fatalf("describe = %d %s", described.Code, described.Body.String())
	}
	description := decodeBody(t, described)
	if description["Status"] != "FINISHED" || description["Id"] != id || description["ResultRows"] != float64(3) || description["HasResultSet"] != true {
		t.Fatalf("describe body = %s", described.Body.String())
	}
	if !strings.Contains(description["QueryString"].(string), "commerce.orders") {
		t.Fatalf("query string = %s", described.Body.String())
	}

	result := postTarget(t, handler, "/", "RedshiftData.GetStatementResult", `{"Id":"`+id+`"}`, testToken)
	if result.Code != http.StatusOK {
		t.Fatalf("result = %d %s", result.Code, result.Body.String())
	}
	decoded := decodeBody(t, result)
	if decoded["TotalNumRows"] != float64(3) {
		t.Fatalf("result rows = %s", result.Body.String())
	}
	if !hasAll(stringValues(decoded), "10482", "10483", "10484", "Priya Nair", "Marco Silva", "Dana Whitfield", "1 x AG-TEE-02; 2 x AG-MUG-01", "1 x AG-NOTE-03") {
		t.Fatalf("order rows = %s", result.Body.String())
	}

	again := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+id+`"}`, testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "10482") {
		t.Fatalf("persisted result = %d %s", again.Code, again.Body.String())
	}

	payments := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.payments"}`, testToken)
	paymentResultID := decodeBody(t, payments)["Id"].(string)
	paymentRows := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+paymentResultID+`"}`, testToken)
	if !hasAll(stringValues(decodeBody(t, paymentRows)), "pay_Acme10482", "pay_Acme10483", "pay_Acme10484", "order_Acme10482", "rfnd_Acme10484") {
		t.Fatalf("payments = %s", paymentRows.Body.String())
	}

	shipments := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.shipments WHERE order_id = '10483'"}`, testToken)
	shipmentID := decodeBody(t, shipments)["Id"].(string)
	shipmentRows := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+shipmentID+`"}`, testToken)
	if shipmentRows.Code != http.StatusOK || !hasAll(stringValues(decodeBody(t, shipmentRows)), "SR10483AWB", "Bluedart", "Customer not available") {
		t.Fatalf("shipment = %d %s", shipmentRows.Code, shipmentRows.Body.String())
	}

	saas := postTarget(t, handler, "/", "RedshiftData.ExecuteStatement", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","StatementName":"acme-saas","Sql":"SELECT invoice_id, amount_usd, currency, status, payment_id, duplicate_payment_id FROM commerce.saas_invoices"}`, testToken)
	if saas.Code != http.StatusOK {
		t.Fatalf("saas execute = %d %s", saas.Code, saas.Body.String())
	}
	saasID := decodeBody(t, saas)["Id"].(string)
	saasRows := postTarget(t, handler, "/", "RedshiftData.GetStatementResult", `{"Id":"`+saasID+`"}`, testToken)
	if saasRows.Code != http.StatusOK || !strings.Contains(saasRows.Body.String(), `"longValue":1240`) || !hasAll(stringValues(decodeBody(t, saasRows)), "INV-4812", "USD", "sent", "pay_saas_4812", "pay_saas_4812b") {
		t.Fatalf("saas result = %d %s", saasRows.Code, saasRows.Body.String())
	}

	listed := postTarget(t, handler, "/ListStatements", "", `{}`, testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), saasID) || !strings.Contains(listed.Body.String(), id) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
}

func TestSQLSurfaceAndStatementPersistence(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-warehouse.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	rejected := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"DELETE FROM commerce.orders"}`, testToken)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "only supports a single SELECT") || !strings.Contains(rejected.Body.String(), "commerce.saas_invoices") {
		t.Fatalf("rejected sql = %d %s", rejected.Code, rejected.Body.String())
	}
	joined := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders JOIN commerce.payments"}`, testToken)
	if joined.Code != http.StatusBadRequest || !strings.Contains(joined.Body.String(), "only supports a single SELECT") {
		t.Fatalf("join = %d %s", joined.Code, joined.Body.String())
	}
	unknown := postTarget(t, handler, "/", "RedshiftData.ExecuteStatement", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.customers"}`, testToken)
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "unknown table") {
		t.Fatalf("unknown table = %d %s", unknown.Code, unknown.Body.String())
	}

	filtered := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT order_id, status FROM orders WHERE order_id = '10483'"}`, testToken)
	filteredID := decodeBody(t, filtered)["Id"].(string)
	filteredRows := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+filteredID+`"}`, testToken)
	if filteredRows.Code != http.StatusOK || !strings.Contains(filteredRows.Body.String(), "10483") || strings.Contains(filteredRows.Body.String(), "10482") || !strings.Contains(filteredRows.Body.String(), "ndr") {
		t.Fatalf("filtered = %d %s", filteredRows.Code, filteredRows.Body.String())
	}

	parameterized := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.payments WHERE payment_id = :id","Parameters":[{"name":"id","value":"pay_Acme10482"}]}`, testToken)
	parameterID := decodeBody(t, parameterized)["Id"].(string)
	parameterRows := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+parameterID+`"}`, testToken)
	if !strings.Contains(parameterRows.Body.String(), "pay_Acme10482") || strings.Contains(parameterRows.Body.String(), "pay_Acme10483") {
		t.Fatalf("parameter rows = %s", parameterRows.Body.String())
	}

	first := postTarget(t, handler, "/ExecuteStatement", "", `{"ClientToken":"retry-orders","ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders"}`, testToken)
	firstID := decodeBody(t, first)["Id"].(string)
	second := postTarget(t, handler, "/ExecuteStatement", "", `{"ClientToken":"retry-orders","ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.saas_invoices"}`, testToken)
	if decodeBody(t, second)["Id"] != firstID {
		t.Fatalf("client token did not reuse %s: %s", firstID, second.Body.String())
	}
	described := postTarget(t, handler, "/DescribeStatement", "", `{"Id":"`+firstID+`"}`, testToken)
	if !strings.Contains(described.Body.String(), "commerce.orders") || strings.Contains(described.Body.String(), "saas_invoices") {
		t.Fatalf("idempotent statement changed sql: %s", described.Body.String())
	}

	missing := postTarget(t, handler, "/DescribeStatement", "", `{"Id":"missing"}`, testToken)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "ResourceNotFoundException") {
		t.Fatalf("missing statement = %d %s", missing.Code, missing.Body.String())
	}
	extra := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders","Nope":true}`, testToken)
	if extra.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", extra.Code, extra.Body.String())
	}
	mismatch := postTarget(t, handler, "/ExecuteStatement", "RedshiftData.DescribeStatement", `{"Id":"statement-0001"}`, testToken)
	if mismatch.Code != http.StatusBadRequest || !strings.Contains(mismatch.Body.String(), "does not match") {
		t.Fatalf("target mismatch = %d %s", mismatch.Code, mismatch.Body.String())
	}
	noTarget := postTarget(t, handler, "/", "", `{"Sql":"SELECT * FROM commerce.orders"}`, testToken)
	if noTarget.Code != http.StatusBadRequest || !strings.Contains(noTarget.Body.String(), "X-Amz-Target") {
		t.Fatalf("missing target = %d %s", noTarget.Code, noTarget.Body.String())
	}
}

func TestMinimalSelectReturnsEmptyRecords(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "minimal.v1.json")), &testIDs{})
	executed := postTarget(t, handler, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders"}`, testToken)
	if executed.Code != http.StatusOK || decodeBody(t, executed)["Status"] != "FINISHED" {
		t.Fatalf("execute = %d %s", executed.Code, executed.Body.String())
	}
	id := decodeBody(t, executed)["Id"].(string)
	result := postTarget(t, handler, "/GetStatementResult", "", `{"Id":"`+id+`"}`, testToken)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"Records":[]`) || !strings.Contains(result.Body.String(), `"TotalNumRows":0`) {
		t.Fatalf("empty result = %d %s", result.Code, result.Body.String())
	}
}

func TestTwoRedshiftInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-warehouse.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	executed := postTarget(t, first, "/ExecuteStatement", "", `{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders"}`, testToken)
	id := decodeBody(t, executed)["Id"].(string)
	missing := postTarget(t, second, "/DescribeStatement", "", `{"Id":"`+id+`"}`, testToken)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "ResourceNotFoundException") {
		t.Fatalf("second instance leaked statement = %d %s", missing.Code, missing.Body.String())
	}
}
