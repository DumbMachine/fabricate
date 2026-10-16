package razorpay

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
	"github.com/dumbmachine/fabricate/resources/razorpay/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_razorpay_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "razorpay.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-payments.v1.json")
	codec := scenarioCodec{}
	if err := codec.Validate(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	embedded, err := NewResource().Scenario("razorpay.acme-payments.v1")
	if err != nil {
		t.Fatal(err)
	}
	beforeFile, _ := scenario.CanonicalJSON(doc)
	beforeEmbedded, _ := scenario.CanonicalJSON(embedded)
	if string(beforeFile) != string(beforeEmbedded) {
		t.Fatal("embedded scenario differs from the file")
	}
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := scenario.CanonicalJSON(dumped)
	if string(beforeFile) != string(after) {
		t.Fatalf("load/dump changed the baseline:\nbefore=%s\nafter=%s", beforeFile, after)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Orders) != 5 || len(state.Payments) != 5 || len(state.Refunds) != 1 || len(state.PaymentLinks) != 0 {
		t.Fatalf("record counts orders=%d payments=%d refunds=%d links=%d", len(state.Orders), len(state.Payments), len(state.Refunds), len(state.PaymentLinks))
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
		t.Fatalf("load/dump changed the minimal baseline:\nbefore=%s\nafter=%s", before, after)
	}
	if strings.Contains(string(after), "null") {
		t.Fatalf("dump used null: %s", after)
	}
	for _, collection := range []string{`"orders":[]`, `"payments":[]`, `"refunds":[]`, `"paymentLinks":[]`} {
		if !strings.Contains(string(after), collection) {
			t.Fatalf("missing %s in %s", collection, after)
		}
	}
}

func TestUnknownStateFieldsRejected(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"orders":[],"payments":[],"refunds":[],"paymentLinks":[],"extra":true}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	doc = loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"orders":[{"id":"order_x","amount":100,"amountPaid":0,"amountDue":100,"currency":"INR","receipt":"","status":"created","attempts":0,"notes":{},"createdAt":1,"extra":true}],"payments":[],"refunds":[],"paymentLinks":[]}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown order field to fail")
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
	if len(seen) != 14 {
		t.Fatalf("compiled operation count = %d, want 14", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "api.razorpay.com" {
		t.Fatalf("provider hosts = %v", hosts)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-payments.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/payments", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Authentication failed") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1/payments", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/v1/payments", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"count":5`) || !strings.Contains(listed.Body.String(), `"pay_Acme10482"`) {
		t.Fatalf("list payments = %d %s", listed.Code, listed.Body.String())
	}

	payment := request(t, handler, http.MethodGet, "/v1/payments/pay_Acme10482", "", testToken)
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), `"amount":309700`) || !strings.Contains(payment.Body.String(), `"method":"upi"`) || !strings.Contains(payment.Body.String(), `"shop":"acme-goods"`) || !strings.Contains(payment.Body.String(), `"order_id":"10482"`) {
		t.Fatalf("payment 10482 = %d %s", payment.Code, payment.Body.String())
	}

	refunded := request(t, handler, http.MethodGet, "/v1/payments/pay_Acme10484", "", testToken)
	if refunded.Code != http.StatusOK || !strings.Contains(refunded.Body.String(), `"status":"refunded"`) || !strings.Contains(refunded.Body.String(), `"refund_status":"full"`) || !strings.Contains(refunded.Body.String(), `"amount_refunded":149900`) {
		t.Fatalf("payment 10484 = %d %s", refunded.Code, refunded.Body.String())
	}
	original := request(t, handler, http.MethodGet, "/v1/refunds/rfnd_Acme10484", "", testToken)
	if original.Code != http.StatusOK || !strings.Contains(original.Body.String(), `"payment_id":"pay_Acme10484"`) || !strings.Contains(original.Body.String(), `"amount":149900`) {
		t.Fatalf("refund 10484 = %d %s", original.Code, original.Body.String())
	}

	orderPayments := request(t, handler, http.MethodGet, "/v1/orders/order_Acme10482/payments", "", testToken)
	if orderPayments.Code != http.StatusOK || !strings.Contains(orderPayments.Body.String(), `"pay_Acme10482"`) {
		t.Fatalf("order payments = %d %s", orderPayments.Code, orderPayments.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1/payments/pay_Acme10483/refund", `{"amount":49900}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":"rfnd_razorpay.refund-0001"`) || !strings.Contains(created.Body.String(), `"payment_id":"pay_Acme10483"`) || !strings.Contains(created.Body.String(), `"entity":"refund"`) {
		t.Fatalf("create refund = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/v1/refunds/rfnd_razorpay.refund-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"amount":49900`) || !strings.Contains(again.Body.String(), `"status":"processed"`) {
		t.Fatalf("refund after create = %d %s", again.Code, again.Body.String())
	}
	updated := request(t, handler, http.MethodGet, "/v1/payments/pay_Acme10483", "", testToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"status":"refunded"`) || !strings.Contains(updated.Body.String(), `"refund_status":"full"`) || !strings.Contains(updated.Body.String(), `"amount_refunded":49900`) {
		t.Fatalf("payment after refund = %d %s", updated.Code, updated.Body.String())
	}
	nested := request(t, handler, http.MethodGet, "/v1/payments/pay_Acme10483/refunds", "", testToken)
	if nested.Code != http.StatusOK || !strings.Contains(nested.Body.String(), `"rfnd_razorpay.refund-0001"`) {
		t.Fatalf("nested refunds = %d %s", nested.Code, nested.Body.String())
	}

	partial := request(t, handler, http.MethodPost, "/v1/payments/pay_TS10491/refund", `{"amount":10000,"notes":{"shop":"tinyshop"}}`, testToken)
	if partial.Code != http.StatusOK || !strings.Contains(partial.Body.String(), `"amount":10000`) {
		t.Fatalf("partial refund = %d %s", partial.Code, partial.Body.String())
	}
	partialPayment := request(t, handler, http.MethodGet, "/v1/payments/pay_TS10491", "", testToken)
	if partialPayment.Code != http.StatusOK || !strings.Contains(partialPayment.Body.String(), `"status":"captured"`) || !strings.Contains(partialPayment.Body.String(), `"refund_status":"partial"`) || !strings.Contains(partialPayment.Body.String(), `"amount_refunded":10000`) {
		t.Fatalf("payment after partial = %d %s", partialPayment.Code, partialPayment.Body.String())
	}

	link := request(t, handler, http.MethodPost, "/v1/payment_links", `{"amount":79900,"currency":"INR","description":"Acme Ceramic Mug","reference_id":"AG-MUG-01","customer":{"name":"Priya Nair","email":"priya@fernworks.example","contact":"+91-98450-11223"},"notes":{"shop":"acme-goods"}}`, testToken)
	if link.Code != http.StatusOK || !strings.Contains(link.Body.String(), `"id":"plink_razorpay.payment_link-0001"`) || !strings.Contains(link.Body.String(), `"status":"created"`) {
		t.Fatalf("create link = %d %s", link.Code, link.Body.String())
	}
	cancelled := request(t, handler, http.MethodPost, "/v1/payment_links/plink_razorpay.payment_link-0001/cancel", "", testToken)
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel link = %d %s", cancelled.Code, cancelled.Body.String())
	}
	fetched := request(t, handler, http.MethodGet, "/v1/payment_links/plink_razorpay.payment_link-0001", "", testToken)
	if fetched.Code != http.StatusOK || !strings.Contains(fetched.Body.String(), `"status":"cancelled"`) || !strings.Contains(fetched.Body.String(), `"AG-MUG-01"`) {
		t.Fatalf("link after cancel = %d %s", fetched.Code, fetched.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-payments.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1/payments/pay_missing", "", testToken)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "The id provided does not exist") {
		t.Fatalf("missing payment = %d %s", missing.Code, missing.Body.String())
	}
	small := request(t, handler, http.MethodPost, "/v1/orders", `{"amount":50,"currency":"INR"}`, testToken)
	if small.Code != http.StatusBadRequest || !strings.Contains(small.Body.String(), "The amount must be at least INR 1.00") {
		t.Fatalf("small order = %d %s", small.Code, small.Body.String())
	}
	extra := request(t, handler, http.MethodPost, "/v1/orders", `{"amount":10000,"currency":"INR","extra":true}`, testToken)
	if extra.Code != http.StatusBadRequest {
		t.Fatalf("extra field = %d %s", extra.Code, extra.Body.String())
	}
	again := request(t, handler, http.MethodPost, "/v1/payments/pay_Acme10484/refund", `{"amount":149900}`, testToken)
	if again.Code != http.StatusBadRequest || !strings.Contains(again.Body.String(), "fully refunded") {
		t.Fatalf("second full refund = %d %s", again.Code, again.Body.String())
	}
	created := request(t, handler, http.MethodPost, "/v1/orders", `{"amount":79900,"currency":"INR","receipt":"mug","notes":{"shop":"acme-goods","order_id":"10492"}}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":"order_razorpay.order-0001"`) || !strings.Contains(created.Body.String(), `"status":"created"`) {
		t.Fatalf("create order = %d %s", created.Code, created.Body.String())
	}
	fetched := request(t, handler, http.MethodGet, "/v1/orders/order_razorpay.order-0001", "", testToken)
	if fetched.Code != http.StatusOK || !strings.Contains(fetched.Body.String(), `"receipt":"mug"`) || !strings.Contains(fetched.Body.String(), `"amount_due":79900`) {
		t.Fatalf("created order = %d %s", fetched.Code, fetched.Body.String())
	}
}

func TestTwoRazorpayInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-payments.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	response := request(t, first, http.MethodPost, "/v1/payments/pay_NW10490/refund", `{"amount":249900}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("refund = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/payments/pay_NW10490", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"status":"captured"`) || strings.Contains(untouched.Body.String(), `"refund_status"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
