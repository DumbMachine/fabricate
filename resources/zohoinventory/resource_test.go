package zohoinventory

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
	"github.com/dumbmachine/fabricate/resources/zohoinventory/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_zohoinventory_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "zohoinventory.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-stock.v1.json")
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
	if scenario.Digest(before) != scenario.Digest(after) {
		t.Fatal("canonical digest changed")
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 3 || len(state.SalesOrders) != 2 || len(state.PurchaseOrders) != 1 || len(state.Warehouses) != 1 {
		t.Fatalf("counts items=%d sales=%d purchase=%d warehouses=%d", len(state.Items), len(state.SalesOrders), len(state.PurchaseOrders), len(state.Warehouses))
	}
	if state.Items[0].SKU != "AG-MUG-01" || state.Items[0].StockOnHand != 120 {
		t.Fatalf("first item = %+v", state.Items[0])
	}
	if state.PurchaseOrders[0].Vendor.Name != "Clay & Co" || state.PurchaseOrders[0].Status != "pending_approval" || state.PurchaseOrders[0].ApproverEmail != "aisha@acme.example" {
		t.Fatalf("purchase order = %+v", state.PurchaseOrders[0])
	}
	if state.PurchaseOrders[0].Comments == nil || len(state.PurchaseOrders[0].Comments) != 0 {
		t.Fatalf("comments = %#v", state.PurchaseOrders[0].Comments)
	}
	if !bytes.Contains(after, []byte(`"comments":[]`)) {
		t.Fatalf("empty comments were not dumped as []: %s", after)
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
		t.Fatalf("minimal load/dump changed:\nbefore=%s\nafter=%s", before, after)
	}
	for _, key := range []string{`"warehouses":[]`, `"items":[]`, `"salesOrders":[]`, `"purchaseOrders":[]`} {
		if !bytes.Contains(after, []byte(key)) {
			t.Fatalf("missing %s in %s", key, after)
		}
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	envelope := bytes.Replace(raw, []byte(`"$resource"`), []byte(`"nope":true,"$resource"`), 1)
	if _, err := scenario.Parse(envelope); err == nil {
		t.Fatal("envelope unknown field accepted")
	}
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = bytes.Replace(doc.State, []byte(`"warehouses"`), []byte(`"extra":1,"warehouses"`), 1)
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("state unknown field accepted")
	}
	doc = loadScenario(t, "acme-stock.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state.PurchaseOrders[0].Total = 1
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = encoded
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("inconsistent purchase order total accepted")
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
		if !strings.HasPrefix(path, "/inventory/v1/") {
			t.Fatalf("path %s is outside /inventory/v1/", path)
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
	if len(seen) != 11 {
		t.Fatalf("compiled operation count = %d, want 11: %v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if got := resource.Descriptor().HostPrefixes["www.zohoapis.com"]; got != "/inventory/" {
		t.Fatalf("host prefix = %q", got)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("embedded scenarios = %d", len(docs))
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-stock.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/inventory/v1/items?organization_id=org-acme", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "not authorized") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	missingOrg := request(t, handler, http.MethodGet, "/inventory/v1/items", "", testToken)
	if missingOrg.Code != http.StatusBadRequest {
		t.Fatalf("missing organization_id = %d %s", missingOrg.Code, missingOrg.Body.String())
	}
	wrongOrg := request(t, handler, http.MethodGet, "/inventory/v1/items?organization_id=other", "", testToken)
	if wrongOrg.Code != http.StatusBadRequest || !strings.Contains(wrongOrg.Body.String(), "Invalid organization id") {
		t.Fatalf("wrong org = %d %s", wrongOrg.Code, wrongOrg.Body.String())
	}

	items := request(t, handler, http.MethodGet, "/inventory/v1/items?organization_id=org-acme", "", testToken)
	if items.Code != http.StatusOK {
		t.Fatalf("items = %d %s", items.Code, items.Body.String())
	}
	var itemList struct {
		Items []struct {
			SKU         string  `json:"sku"`
			StockOnHand float64 `json:"stock_on_hand"`
			Name        string  `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(items.Body.Bytes(), &itemList); err != nil {
		t.Fatal(err)
	}
	wantStock := map[string]float64{"AG-MUG-01": 120, "AG-TEE-02": 48, "AG-NOTE-03": 200}
	if len(itemList.Items) != 3 {
		t.Fatalf("items = %s", items.Body.String())
	}
	for _, item := range itemList.Items {
		if wantStock[item.SKU] != item.StockOnHand {
			t.Fatalf("stock %s = %v", item.SKU, item.StockOnHand)
		}
	}

	orders := request(t, handler, http.MethodGet, "/inventory/v1/salesorders?organization_id=org-acme&reference_number=10482", "", testToken)
	if orders.Code != http.StatusOK || !strings.Contains(orders.Body.String(), `"SO-10482"`) || strings.Contains(orders.Body.String(), `"SO-10483"`) {
		t.Fatalf("sales orders = %d %s", orders.Code, orders.Body.String())
	}
	detail := request(t, handler, http.MethodGet, "/inventory/v1/salesorders/so-10482?organization_id=org-acme", "", testToken)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "priya@fernworks.example") || !strings.Contains(detail.Body.String(), "42 Residency Road") || !strings.Contains(detail.Body.String(), `"total":3097`) {
		t.Fatalf("sales order = %d %s", detail.Code, detail.Body.String())
	}

	pos := request(t, handler, http.MethodGet, "/inventory/v1/purchaseorders?organization_id=org-acme", "", testToken)
	var poList struct {
		PurchaseOrders []struct {
			Number string `json:"purchaseorder_number"`
			Status string `json:"status"`
			Vendor string `json:"vendor_name"`
			Fields []struct {
				Value string `json:"value"`
			} `json:"custom_fields"`
		} `json:"purchaseorders"`
	}
	if err := json.Unmarshal(pos.Body.Bytes(), &poList); err != nil {
		t.Fatal(err)
	}
	if len(poList.PurchaseOrders) != 1 || poList.PurchaseOrders[0].Number != "PO-7721" || poList.PurchaseOrders[0].Status != "pending_approval" || poList.PurchaseOrders[0].Vendor != "Clay & Co" {
		t.Fatalf("purchase orders = %s", pos.Body.String())
	}
	foundApprover := false
	for _, field := range poList.PurchaseOrders[0].Fields {
		if field.Value == "aisha@acme.example" {
			foundApprover = true
		}
	}
	if !foundApprover {
		t.Fatalf("approver missing: %s", pos.Body.String())
	}

	approved := request(t, handler, http.MethodPost, "/inventory/v1/purchaseorders/po-7721/approve?organization_id=org-acme", "", testToken)
	if approved.Code != http.StatusOK || !strings.Contains(approved.Body.String(), `"code":0`) {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/inventory/v1/purchaseorders/po-7721?organization_id=org-acme", "", testToken)
	var got struct {
		PurchaseOrder struct {
			Status   string `json:"status"`
			Comments []struct {
				CommentedBy   string `json:"commented_by"`
				OperationType string `json:"operation_type"`
			} `json:"comments"`
			Lines []struct {
				SKU string `json:"sku"`
			} `json:"line_items"`
		} `json:"purchase_order"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PurchaseOrder.Status != "approved" || len(got.PurchaseOrder.Comments) != 1 || got.PurchaseOrder.Comments[0].CommentedBy != "Aisha Rahman" || got.PurchaseOrder.Comments[0].OperationType != "approved" {
		t.Fatalf("after approve = %s", again.Body.String())
	}
	if len(got.PurchaseOrder.Lines) != 1 || got.PurchaseOrder.Lines[0].SKU != "AG-MUG-01" {
		t.Fatalf("lines = %s", again.Body.String())
	}
	repeat := request(t, handler, http.MethodPost, "/inventory/v1/purchaseorders/po-7721/approve?organization_id=org-acme", "", testToken)
	if repeat.Code != http.StatusBadRequest {
		t.Fatalf("second approve = %d %s", repeat.Code, repeat.Body.String())
	}
	persisted := request(t, handler, http.MethodGet, "/inventory/v1/purchaseorders/po-7721?organization_id=org-acme", "", testToken)
	if !strings.Contains(persisted.Body.String(), `"status":"approved"`) {
		t.Fatalf("persistence = %s", persisted.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-stock.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/inventory/v1/items/not-real?organization_id=org-acme", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":1002`) {
		t.Fatalf("missing item = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodGet, "/inventory/v1/items?organization_id=org-acme&page=0", "", testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid page = %d %s", invalid.Code, invalid.Body.String())
	}
	warehouse := request(t, handler, http.MethodGet, "/inventory/v1/settings/warehouses/wh-blr-1?organization_id=org-acme", "", testToken)
	if warehouse.Code != http.StatusOK || !strings.Contains(warehouse.Body.String(), "18 Industrial Layout") || !strings.Contains(warehouse.Body.String(), `"zip":"560058"`) {
		t.Fatalf("warehouse = %d %s", warehouse.Code, warehouse.Body.String())
	}
}

func TestTwoZohoInventoryInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-stock.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	response := request(t, first, http.MethodPost, "/inventory/v1/purchaseorders/po-7721/approve?organization_id=org-acme", "", testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/inventory/v1/purchaseorders/po-7721?organization_id=org-acme", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"pending_approval"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
