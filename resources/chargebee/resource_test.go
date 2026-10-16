package chargebee

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
	"github.com/dumbmachine/fabricate/resources/chargebee/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_chargebee_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "chargebee.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func request(t *testing.T, handler http.Handler, method, path, body, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		if contentType == "" {
			contentType = "application/x-www-form-urlencoded"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	codec := scenarioCodec{}
	for _, name := range []string{"minimal.v1.json", "acme-billing.v1.json"} {
		t.Run(name, func(t *testing.T) {
			doc := loadScenario(t, name)
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
		})
	}

	doc := loadScenario(t, "acme-billing.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Customers) != 3 || len(state.Subscriptions) != 3 || len(state.Invoices) != 3 || len(state.Transactions) != 2 || len(state.Comments) != 1 {
		t.Fatalf("counts customers=%d subscriptions=%d invoices=%d transactions=%d comments=%d",
			len(state.Customers), len(state.Subscriptions), len(state.Invoices), len(state.Transactions), len(state.Comments))
	}
	raw, err := os.ReadFile(filepath.Join("scenarios", "acme-billing.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, needle := range []string{"INV-4812", "pay_saas_4812", "pay_saas_4812b", "INV-2207", "INV-1188", "non_renewing", "cus_northwind", "sub_tinyshop_pro"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("scenario missing %s", needle)
		}
	}
	if strings.Contains(text, "10482") || strings.Contains(text, "INR") {
		t.Fatal("shop order facts leaked into the SaaS billing scenario")
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"customers":[],"subscriptions":[],"invoices":[],"transactions":[],"comments":[],"orders":[]}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	doc.State = []byte(`{"customers":[{"id":"cus_x","first_name":"A","last_name":"B","email":"a@acme.example","phone":"+1","company":"Acme","auto_collection":"on","net_term_days":0,"allow_direct_debit":false,"created_at":1,"taxability":"taxable","updated_at":1,"pii_cleared":"active","resource_version":1,"deleted":false,"preferred_currency_code":"USD","promotional_credits":0,"refundable_credits":0,"excess_payments":0,"unbilled_charges":0,"shop_order":"10482"}],"subscriptions":[],"invoices":[],"transactions":[],"comments":[]}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown customer field to fail")
	}
	raw, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"state"`, `"extra":true,"state"`, 1))
	if _, err := scenario.Parse(raw); err == nil {
		t.Fatal("expected unknown envelope field to fail")
	}
	doc = loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"customers":[{"id":"cus_inr","first_name":"A","last_name":"B","email":"a@acme.example","phone":"+1","company":"Acme","auto_collection":"on","net_term_days":0,"allow_direct_debit":false,"created_at":1,"taxability":"taxable","updated_at":1,"pii_cleared":"active","resource_version":1,"deleted":false,"preferred_currency_code":"INR","promotional_credits":0,"refundable_credits":0,"excess_payments":0,"unbilled_charges":0}],"subscriptions":[],"invoices":[],"transactions":[],"comments":[]}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected INR currency to fail")
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
		t.Fatalf("compiled operation count = %d, want 11", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if resource.Descriptor().DisplayName != "Chargebee" || resource.Descriptor().Version != "v2" {
		t.Fatalf("descriptor = %+v", resource.Descriptor())
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-billing.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/api/v2/subscriptions", "", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "api_authentication_failed") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	basic := httptest.NewRequest(http.MethodGet, "/api/v2/subscriptions", nil)
	basic.Header.Set("Authorization", "Basic dGVzdDo=")
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != http.StatusUnauthorized {
		t.Fatalf("basic auth = %d %s", basicRec.Code, basicRec.Body.String())
	}

	subscriptions := request(t, handler, http.MethodGet, "/api/v2/subscriptions", "", testToken, "")
	if subscriptions.Code != http.StatusOK || !strings.Contains(subscriptions.Body.String(), `"sub_northwind_checkout"`) || !strings.Contains(subscriptions.Body.String(), `"non_renewing"`) {
		t.Fatalf("subscriptions = %d %s", subscriptions.Code, subscriptions.Body.String())
	}
	nonRenewing := request(t, handler, http.MethodGet, "/api/v2/subscriptions?status[is]=non_renewing", "", testToken, "")
	if nonRenewing.Code != http.StatusOK || !strings.Contains(nonRenewing.Body.String(), `"sub_tinyshop_pro"`) || strings.Contains(nonRenewing.Body.String(), `"sub_northwind_checkout"`) {
		t.Fatalf("non_renewing filter = %d %s", nonRenewing.Code, nonRenewing.Body.String())
	}

	got := request(t, handler, http.MethodGet, "/api/v2/subscriptions/sub_northwind_checkout", "", testToken, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"cus_northwind"`) || !strings.Contains(got.Body.String(), `"active"`) {
		t.Fatalf("get subscription = %d %s", got.Code, got.Body.String())
	}

	invoices := request(t, handler, http.MethodGet, "/api/v2/invoices?id[is]=INV-4812", "", testToken, "")
	body := invoices.Body.String()
	if invoices.Code != http.StatusOK || !strings.Contains(body, `"total":124000`) || !strings.Contains(body, `"USD"`) || !strings.Contains(body, `"pay_saas_4812"`) || !strings.Contains(body, `"pay_saas_4812b"`) {
		t.Fatalf("invoices = %d %s", invoices.Code, body)
	}
	if strings.Contains(body, "10482") {
		t.Fatal("invoice list included a shop order")
	}

	transactions := request(t, handler, http.MethodGet, "/api/v2/transactions", "", testToken, "")
	if transactions.Code != http.StatusOK || !strings.Contains(transactions.Body.String(), `"pay_saas_4812b"`) {
		t.Fatalf("transactions = %d %s", transactions.Code, transactions.Body.String())
	}

	cancelled := request(t, handler, http.MethodPost, "/api/v2/subscriptions/sub_northwind_checkout/cancel_for_items", "cancel_option=end_of_term&cancel_reason_code=duplicate+charge", testToken, "")
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body.String(), `"non_renewing"`) || !strings.Contains(cancelled.Body.String(), `"duplicate charge"`) {
		t.Fatalf("cancel = %d %s", cancelled.Code, cancelled.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/api/v2/subscriptions/sub_northwind_checkout", "", testToken, "")
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"non_renewing"`) {
		t.Fatalf("subscription after cancel = %d %s", again.Code, again.Body.String())
	}

	comment := request(t, handler, http.MethodPost, "/api/v2/comments", "entity_type=subscription&entity_id=sub_northwind_checkout&notes=Duplicate+charges+pay_saas_4812+and+pay_saas_4812b+on+INV-4812.", testToken, "")
	if comment.Code != http.StatusOK || !strings.Contains(comment.Body.String(), `"cmt-0001"`) || !strings.Contains(comment.Body.String(), "pay_saas_4812b") {
		t.Fatalf("comment = %d %s", comment.Code, comment.Body.String())
	}
	comments := request(t, handler, http.MethodGet, "/api/v2/comments?entity_type=subscription&entity_id=sub_northwind_checkout", "", testToken, "")
	if comments.Code != http.StatusOK || !strings.Contains(comments.Body.String(), "pay_saas_4812b") {
		t.Fatalf("comments after write = %d %s", comments.Code, comments.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/api/v2/subscriptions/sub_missing", "", testToken, "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"resource_not_found"`) {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodGet, "/api/v2/subscriptions?limit=0", "", testToken, "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestTwoChargebeeInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-billing.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	response := request(t, first, http.MethodPost, "/api/v2/subscriptions/sub_fernworks_platform/cancel_for_items", "cancel_option=immediately", testToken, "")
	if response.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/api/v2/subscriptions/sub_fernworks_platform", "", testToken, "")
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"active"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
