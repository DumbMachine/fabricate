package zohobooks

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
	"github.com/dumbmachine/fabricate/resources/zohobooks/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_zohobooks_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "zohobooks.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-ledger.v1.json")
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
	if len(state.Contacts) != 27 || len(state.Invoices) != 7 || len(state.CreditNotes) != 1 || len(state.CustomerPayments) != 5 {
		t.Fatalf("counts contacts=%d invoices=%d creditNotes=%d payments=%d", len(state.Contacts), len(state.Invoices), len(state.CreditNotes), len(state.CustomerPayments))
	}
	if state.CreditNotes[0].ReferenceNumber != "rfnd_Acme10484" || state.CreditNotes[0].InvoiceID != "INV-10484" {
		t.Fatalf("credit note = %+v", state.CreditNotes[0])
	}
	for _, payment := range state.CustomerPayments {
		if payment.PaymentID == "rfnd_Acme10484" || strings.Contains(payment.Description, "rfnd_Acme10484") {
			t.Fatalf("shop refund was stored as a customer payment: %+v", payment)
		}
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
	if bytes.Contains(dumped.State, []byte("null")) {
		t.Fatalf("empty slices dumped as null: %s", dumped.State)
	}
	for _, want := range []string{`"contacts":[]`, `"invoices":[]`, `"creditNotes":[]`, `"customerPayments":[]`} {
		if !bytes.Contains(dumped.State, []byte(want)) {
			t.Fatalf("dump missing %s: %s", want, dumped.State)
		}
	}
}

func TestUnknownScenarioFieldsFail(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["refundId"] = "rfnd_Acme10484"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown field to fail")
	}

	doc = loadScenario(t, "minimal.v1.json")
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["contacts"] = []map[string]any{{
		"contactId": "contact-extra", "contactName": "Extra", "companyName": "Acme",
		"contactType": "customer", "customerSubType": "individual", "email": "extra@acme.example",
		"phone": "+91-80-4123-0101", "currencyCode": "INR", "status": "active", "unknown": true,
	}}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown contact field to fail")
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
		if !strings.HasPrefix(path, "/books/v3/") {
			t.Fatalf("path %s is outside /books/v3/", path)
		}
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
		t.Fatalf("compiled operation count = %d, want 13: %v", len(seen), seen)
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "zohobooks" || descriptor.DisplayName != "Zoho Books" || descriptor.Version != "v3" {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "www.zohoapis.com" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	if descriptor.HostPrefixes["www.zohoapis.com"] != "/books/" || len(descriptor.HostPrefixes) != 1 {
		t.Fatalf("host prefixes = %v", descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-ledger.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	const org = "organization_id=org-acme"

	unauthorized := request(t, handler, http.MethodGet, "/books/v3/invoices?"+org, "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), `"code":57`) {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	wrongOrg := request(t, handler, http.MethodGet, "/books/v3/invoices?organization_id=other", "", testToken)
	if wrongOrg.Code != http.StatusNotFound || !strings.Contains(wrongOrg.Body.String(), "Organization does not exist") {
		t.Fatalf("wrong org = %d %s", wrongOrg.Code, wrongOrg.Body.String())
	}

	orgs := request(t, handler, http.MethodGet, "/books/v3/organizations", "", testToken)
	if orgs.Code != http.StatusOK || !strings.Contains(orgs.Body.String(), `"organization_id":"org-acme"`) || !strings.Contains(orgs.Body.String(), "Aisha Rahman") {
		t.Fatalf("organizations = %d %s", orgs.Code, orgs.Body.String())
	}

	contacts := request(t, handler, http.MethodGet, "/books/v3/contacts?"+org, "", testToken)
	if contacts.Code != http.StatusOK || !strings.Contains(contacts.Body.String(), `"contact_id":"contact-priya"`) || !strings.Contains(contacts.Body.String(), "Northwind Traders") {
		t.Fatalf("contacts = %d %s", contacts.Code, contacts.Body.String())
	}
	if strings.Count(contacts.Body.String(), `"contact_id"`) != 27 {
		t.Fatalf("contact count marker = %d body %s", strings.Count(contacts.Body.String(), `"contact_id"`), contacts.Body.String())
	}

	priya := request(t, handler, http.MethodGet, "/books/v3/contacts/contact-priya?"+org, "", testToken)
	if priya.Code != http.StatusOK || !strings.Contains(priya.Body.String(), "42 Residency Road") || !strings.Contains(priya.Body.String(), "priya@fernworks.example") {
		t.Fatalf("priya = %d %s", priya.Code, priya.Body.String())
	}

	invoices := request(t, handler, http.MethodGet, "/books/v3/invoices?"+org, "", testToken)
	for _, number := range []string{"INV-10482", "INV-10483", "INV-10484", "INV-10490", "INV-1188", "INV-2207", "INV-4812"} {
		if invoices.Code != http.StatusOK || !strings.Contains(invoices.Body.String(), number) {
			t.Fatalf("invoices missing %s = %d %s", number, invoices.Code, invoices.Body.String())
		}
	}
	paid := request(t, handler, http.MethodGet, "/books/v3/invoices?"+org+"&status=paid", "", testToken)
	if strings.Count(paid.Body.String(), `"invoice_id"`) != 4 {
		t.Fatalf("paid invoices = %s", paid.Body.String())
	}

	duplicate := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-4812?"+org, "", testToken)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"status":"sent"`) || !strings.Contains(duplicate.Body.String(), `"total":1240`) || !strings.Contains(duplicate.Body.String(), `"currency_code":"USD"`) || !strings.Contains(duplicate.Body.String(), "pay_saas_4812b") {
		t.Fatalf("INV-4812 = %d %s", duplicate.Code, duplicate.Body.String())
	}
	if strings.Contains(duplicate.Body.String(), "rfnd_Acme10484") {
		t.Fatalf("INV-4812 mentions the shop refund: %s", duplicate.Body.String())
	}

	shop := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-10482?"+org, "", testToken)
	if shop.Code != http.StatusOK || !strings.Contains(shop.Body.String(), `"status":"paid"`) || !strings.Contains(shop.Body.String(), `"total":3097`) || !strings.Contains(shop.Body.String(), "AG-TEE-02") || !strings.Contains(shop.Body.String(), "AG-MUG-01") {
		t.Fatalf("INV-10482 = %d %s", shop.Code, shop.Body.String())
	}

	voided := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-10484?"+org, "", testToken)
	if voided.Code != http.StatusOK || !strings.Contains(voided.Body.String(), `"status":"void"`) {
		t.Fatalf("INV-10484 = %d %s", voided.Code, voided.Body.String())
	}

	cancel := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-1188?"+org, "", testToken)
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), `"status":"sent"`) || !strings.Contains(cancel.Body.String(), `"balance":1188`) || !strings.Contains(cancel.Body.String(), "Cancellation requested") {
		t.Fatalf("INV-1188 = %d %s", cancel.Code, cancel.Body.String())
	}

	note := request(t, handler, http.MethodGet, "/books/v3/creditnotes/CN-10484?"+org, "", testToken)
	if note.Code != http.StatusOK || !strings.Contains(note.Body.String(), `"reference_number":"rfnd_Acme10484"`) || !strings.Contains(note.Body.String(), `"invoice_id":"INV-10484"`) || !strings.Contains(note.Body.String(), "Distinct from SaaS invoice INV-4812") {
		t.Fatalf("credit note = %d %s", note.Code, note.Body.String())
	}

	payments := request(t, handler, http.MethodGet, "/books/v3/customerpayments?"+org+"&reference_number=INV-4812", "", testToken)
	if payments.Code != http.StatusOK || !strings.Contains(payments.Body.String(), `"payment_id":"pay_saas_4812"`) || !strings.Contains(payments.Body.String(), `"payment_id":"pay_saas_4812b"`) || strings.Contains(payments.Body.String(), "pay_Acme10482") {
		t.Fatalf("duplicate payments = %d %s", payments.Code, payments.Body.String())
	}

	invalid := request(t, handler, http.MethodPost, "/books/v3/invoices?"+org, `{"customer_id":"contact-priya"}`, testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":4`) {
		t.Fatalf("invalid invoice = %d %s", invalid.Code, invalid.Body.String())
	}
	missingCustomer := request(t, handler, http.MethodPost, "/books/v3/invoices?"+org, `{"customer_id":"missing","line_items":[{"name":"Mug","rate":799,"quantity":1}]}`, testToken)
	if missingCustomer.Code != http.StatusNotFound || !strings.Contains(missingCustomer.Body.String(), "Contact does not exist") {
		t.Fatalf("missing customer = %d %s", missingCustomer.Code, missingCustomer.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/books/v3/invoices?"+org, `{"customer_id":"contact-priya","invoice_number":"INV-TEST","currency_code":"INR","date":"2026-08-26","reference_number":"test","notes":"Created during the test.","line_items":[{"name":"Replacement mug","description":"AG-MUG-01","rate":799,"quantity":1}]}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"invoice_id":"INV-TEST"`) || !strings.Contains(created.Body.String(), `"total":799`) {
		t.Fatalf("create invoice = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-TEST?"+org, "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "Replacement mug") || !strings.Contains(again.Body.String(), `"status":"draft"`) {
		t.Fatalf("read created invoice = %d %s", again.Code, again.Body.String())
	}

	paidCreate := request(t, handler, http.MethodPost, "/books/v3/customerpayments?"+org, `{"customer_id":"contact-priya","payment_mode":"cash","amount":799,"date":"2026-08-26","reference_number":"pay_test","description":"Test payment","invoices":[{"invoice_id":"INV-TEST","amount_applied":799}]}`, testToken)
	if paidCreate.Code != http.StatusCreated || !strings.Contains(paidCreate.Body.String(), `"payment_id":"zohobooks.payment-0001"`) {
		t.Fatalf("create payment = %d %s", paidCreate.Code, paidCreate.Body.String())
	}
	payment := request(t, handler, http.MethodGet, "/books/v3/customerpayments/zohobooks.payment-0001?"+org, "", testToken)
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), `"invoice_id":"INV-TEST"`) {
		t.Fatalf("read payment = %d %s", payment.Code, payment.Body.String())
	}
	settled := request(t, handler, http.MethodGet, "/books/v3/invoices/INV-TEST?"+org, "", testToken)
	if settled.Code != http.StatusOK || !strings.Contains(settled.Body.String(), `"status":"paid"`) || !strings.Contains(settled.Body.String(), `"balance":0`) {
		t.Fatalf("invoice after payment = %d %s", settled.Code, settled.Body.String())
	}
}

func TestTwoZohoBooksInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-ledger.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	const org = "organization_id=org-acme"
	response := request(t, first, http.MethodPost, "/books/v3/invoices?"+org, `{"customer_id":"contact-acme","invoice_number":"INV-ISOLATED","currency_code":"INR","line_items":[{"name":"Isolation","rate":10,"quantity":1}]}`, testToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/books/v3/invoices/INV-ISOLATED?"+org, "", testToken)
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
