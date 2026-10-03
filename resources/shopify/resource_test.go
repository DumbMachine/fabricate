package shopify

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
	"github.com/dumbmachine/fabricate/resources/shopify/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_shopify_test_token"

type testSecrets map[string]string

func (s testSecrets) Get(_ context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", fmt.Errorf("missing secret %s", key)
	}
	return value, nil
}

type testIDs struct {
	mu sync.Mutex
	n  map[string]int
}

func (ids *testIDs) Next(_ context.Context, kind string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if ids.n == nil {
		ids.n = map[string]int{}
	}
	ids.n[kind]++
	return fmt.Sprintf("%s-%04d", kind, ids.n[kind]), nil
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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "shopify.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func request(t *testing.T, handler http.Handler, method, path, body, host string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if host != "" {
		req.Host = host
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func bearer() http.Header {
	return http.Header{"Authorization": []string{"Bearer " + testToken}}
}

func roundTrip(t *testing.T, name string) fixtureState {
	t.Helper()
	doc := loadScenario(t, name)
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
		t.Fatalf("load/dump changed %s:\nbefore=%s\nafter=%s", name, before, after)
	}
	if strings.Contains(string(after), "null") {
		t.Fatalf("%s dump used null: %s", name, after)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestScenariosRoundTrip(t *testing.T) {
	minimal := roundTrip(t, "minimal.v1.json")
	if len(minimal.Shops) != 1 || len(minimal.Products) != 0 || len(minimal.Customers) != 0 || len(minimal.Orders) != 0 || len(minimal.Fulfillments) != 0 || len(minimal.Refunds) != 0 {
		t.Fatalf("minimal counts = shops %d products %d customers %d orders %d fulfillments %d refunds %d", len(minimal.Shops), len(minimal.Products), len(minimal.Customers), len(minimal.Orders), len(minimal.Fulfillments), len(minimal.Refunds))
	}
	goods := roundTrip(t, "acme-goods.v1.json")
	if len(goods.Shops) != 1 || len(goods.Products) != 3 || len(goods.Customers) != 3 || len(goods.Orders) != 3 || len(goods.Fulfillments) != 3 || len(goods.Refunds) != 1 {
		t.Fatalf("acme-goods counts = shops %d products %d customers %d orders %d fulfillments %d refunds %d", len(goods.Shops), len(goods.Products), len(goods.Customers), len(goods.Orders), len(goods.Fulfillments), len(goods.Refunds))
	}
	agency := roundTrip(t, "acme-agency.v1.json")
	if len(agency.Shops) != 4 || len(agency.Products) != 7 || len(agency.Customers) != 5 || len(agency.Orders) != 5 || len(agency.Fulfillments) != 4 || len(agency.Refunds) != 1 {
		t.Fatalf("acme-agency counts = shops %d products %d customers %d orders %d fulfillments %d refunds %d", len(agency.Shops), len(agency.Products), len(agency.Customers), len(agency.Orders), len(agency.Fulfillments), len(agency.Refunds))
	}
}

func TestUnknownStateRejected(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"currentShop":"acme-goods.myshopify.com","shops":[],"products":[],"customers":[],"orders":[],"fulfillments":[],"refunds":[],"extra":true}`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	doc = loadScenario(t, "minimal.v1.json")
	state := map[string]any{}
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	shops := state["shops"].([]any)
	shop := shops[0].(map[string]any)
	shop["plan"] = "plus"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown shop field to fail")
	}
}

func TestOrderTotalMustMatchLines(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "acme-goods.v1.json")
	state := map[string]any{}
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	orders := state["orders"].([]any)
	orders[0].(map[string]any)["totalPrice"] = "1.00"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected mismatched order total to fail")
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
	descriptor := resource.Descriptor()
	if descriptor.Version != "2024-10" || descriptor.HostPrefixes != nil {
		t.Fatalf("descriptor version=%s prefixes=%v", descriptor.Version, descriptor.HostPrefixes)
	}
	wantHosts := []string{
		"acme-goods.myshopify.com",
		"northwind-market.myshopify.com",
		"tinyshop.myshopify.com",
		"fernworks-studio.myshopify.com",
	}
	if len(descriptor.ProviderHosts) != len(wantHosts) {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	for i, host := range wantHosts {
		if descriptor.ProviderHosts[i] != host {
			t.Fatalf("hosts = %v", descriptor.ProviderHosts)
		}
	}
}

func TestUnauthorizedAndReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-goods.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10482.json", "", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	badBearer := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10482.json", "", "", http.Header{"Authorization": []string{"Bearer nope"}})
	if badBearer.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer = %d %s", badBearer.Code, badBearer.Body.String())
	}
	badToken := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10482.json", "", "", http.Header{"X-Shopify-Access-Token": []string{"nope"}})
	if badToken.Code != http.StatusUnauthorized {
		t.Fatalf("bad access token = %d %s", badToken.Code, badToken.Body.String())
	}

	order := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10482.json", "", "", bearer())
	if order.Code != http.StatusOK || !strings.Contains(order.Body.String(), `"email":"priya@fernworks.example"`) || !strings.Contains(order.Body.String(), `"total_price":"3097.00"`) || !strings.Contains(order.Body.String(), `"sku":"AG-TEE-02"`) || !strings.Contains(order.Body.String(), `"sku":"AG-MUG-01"`) {
		t.Fatalf("order 10482 = %d %s", order.Code, order.Body.String())
	}
	access := request(t, handler, http.MethodGet, "/admin/api/2024-10/shop.json", "", "", http.Header{"X-Shopify-Access-Token": []string{testToken}})
	if access.Code != http.StatusOK || !strings.Contains(access.Body.String(), `"name":"Acme Goods"`) {
		t.Fatalf("access token shop = %d %s", access.Code, access.Body.String())
	}

	seeded := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10484/refunds.json", "", "", bearer())
	if seeded.Code != http.StatusOK || !strings.Contains(seeded.Body.String(), `"authorization":"rfnd_Acme10484"`) {
		t.Fatalf("seeded refund = %d %s", seeded.Code, seeded.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/admin/api/2024-10/orders.json", `{"order":{"email":"priya@fernworks.example","line_items":[{"variant_id":2001,"quantity":1}]}}`, "", bearer())
	if created.Code != http.StatusCreated {
		t.Fatalf("create order = %d %s", created.Code, created.Body.String())
	}
	var createdOrder struct {
		Order struct {
			ID        int64  `json:"id"`
			CreatedAt string `json:"created_at"`
			LineItems []struct {
				ID int64 `json:"id"`
			} `json:"line_items"`
			TotalPrice string `json:"total_price"`
		} `json:"order"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdOrder); err != nil {
		t.Fatal(err)
	}
	if createdOrder.Order.ID != 100001 || createdOrder.Order.CreatedAt != "2026-08-26T12:00:00Z" || createdOrder.Order.TotalPrice != "799.00" || len(createdOrder.Order.LineItems) != 1 {
		t.Fatalf("created order = %s", created.Body.String())
	}
	reread := request(t, handler, http.MethodGet, fmt.Sprintf("/admin/api/2024-10/orders/%d.json", createdOrder.Order.ID), "", "", bearer())
	if reread.Code != http.StatusOK || !strings.Contains(reread.Body.String(), `"financial_status":"paid"`) || !strings.Contains(reread.Body.String(), `"sku":"AG-MUG-01"`) {
		t.Fatalf("reread order = %d %s", reread.Code, reread.Body.String())
	}

	fulfillment := request(t, handler, http.MethodPost, "/admin/api/2024-10/fulfillments.json", fmt.Sprintf(`{"fulfillment":{"line_items_by_fulfillment_order":[{"fulfillment_order_id":%d}],"tracking_info":{"company":"Delhivery","number":"SRNEW1"}}}`, createdOrder.Order.ID), "", bearer())
	if fulfillment.Code != http.StatusCreated || !strings.Contains(fulfillment.Body.String(), `"tracking_number":"SRNEW1"`) || !strings.Contains(fulfillment.Body.String(), `"status":"success"`) {
		t.Fatalf("create fulfillment = %d %s", fulfillment.Code, fulfillment.Body.String())
	}
	fulfilled := request(t, handler, http.MethodGet, fmt.Sprintf("/admin/api/2024-10/orders/%d.json", createdOrder.Order.ID), "", "", bearer())
	if !strings.Contains(fulfilled.Body.String(), `"fulfillment_status":"fulfilled"`) {
		t.Fatalf("fulfilled order = %s", fulfilled.Body.String())
	}

	lineID := createdOrder.Order.LineItems[0].ID
	refund := request(t, handler, http.MethodPost, fmt.Sprintf("/admin/api/2024-10/orders/%d/refunds.json", createdOrder.Order.ID), fmt.Sprintf(`{"refund":{"refund_line_items":[{"line_item_id":%d,"quantity":1,"restock_type":"return"}]}}`, lineID), "", bearer())
	if refund.Code != http.StatusCreated || !strings.Contains(refund.Body.String(), `"amount":"799.00"`) || !strings.Contains(refund.Body.String(), `"kind":"refund"`) {
		t.Fatalf("create refund = %d %s", refund.Code, refund.Body.String())
	}
	refunded := request(t, handler, http.MethodGet, fmt.Sprintf("/admin/api/2024-10/orders/%d.json", createdOrder.Order.ID), "", "", bearer())
	if refunded.Code != http.StatusOK || !strings.Contains(refunded.Body.String(), `"financial_status":"refunded"`) || !strings.Contains(refunded.Body.String(), `"current_total_price":"0.00"`) {
		t.Fatalf("refunded order = %d %s", refunded.Code, refunded.Body.String())
	}
	refunds := request(t, handler, http.MethodGet, fmt.Sprintf("/admin/api/2024-10/orders/%d/refunds.json", createdOrder.Order.ID), "", "", bearer())
	if refunds.Code != http.StatusOK || !strings.Contains(refunds.Body.String(), `"subtotal":"799.00"`) {
		t.Fatalf("refund list = %d %s", refunds.Code, refunds.Body.String())
	}
	variant := request(t, handler, http.MethodGet, "/admin/api/2024-10/variants/2001.json", "", "", bearer())
	if variant.Code != http.StatusOK || !strings.Contains(variant.Body.String(), `"inventory_quantity":121`) {
		t.Fatalf("restocked variant = %d %s", variant.Code, variant.Body.String())
	}
	again := request(t, handler, http.MethodPost, fmt.Sprintf("/admin/api/2024-10/orders/%d/refunds.json", createdOrder.Order.ID), fmt.Sprintf(`{"refund":{"refund_line_items":[{"line_item_id":%d,"quantity":1}]}}`, lineID), "", bearer())
	if again.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second refund = %d %s", again.Code, again.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/999999.json", "", "", bearer())
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"errors":"Not Found"`) {
		t.Fatalf("missing order = %d %s", missing.Code, missing.Body.String())
	}
}

func TestHostSelectsShop(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "acme-agency.v1.json")), &testIDs{})

	direct := request(t, handler, http.MethodGet, "/admin/api/2024-10/shop.json", "", "", bearer())
	if direct.Code != http.StatusOK || !strings.Contains(direct.Body.String(), `"myshopify_domain":"acme-goods.myshopify.com"`) {
		t.Fatalf("direct shop = %d %s", direct.Code, direct.Body.String())
	}
	hidden := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10490.json", "", "acme-goods.myshopify.com", bearer())
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("cross-shop order = %d %s", hidden.Code, hidden.Body.String())
	}
	northwind := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10490.json", "", "northwind-market.myshopify.com:443", bearer())
	if northwind.Code != http.StatusOK || !strings.Contains(northwind.Body.String(), `"sku":"NW-KETTLE-01"`) || !strings.Contains(northwind.Body.String(), `"total_price":"2499.00"`) {
		t.Fatalf("northwind order = %d %s", northwind.Code, northwind.Body.String())
	}
	tinyshop := request(t, handler, http.MethodGet, "/admin/api/2024-10/orders/10491.json", "", "TinyShop.myshopify.com", bearer())
	if tinyshop.Code != http.StatusOK || !strings.Contains(tinyshop.Body.String(), "anita.desai@consumer.example") || !strings.Contains(tinyshop.Body.String(), `"sku":"TS-CANDLE-01"`) {
		t.Fatalf("tinyshop order = %d %s", tinyshop.Code, tinyshop.Body.String())
	}
	fernworks := request(t, handler, http.MethodGet, "/admin/api/2024-10/products.json", "", "fernworks-studio.myshopify.com", bearer())
	if fernworks.Code != http.StatusOK || !strings.Contains(fernworks.Body.String(), `"sku":"FW-PRINT-01"`) || strings.Contains(fernworks.Body.String(), "AG-MUG-01") {
		t.Fatalf("fernworks products = %d %s", fernworks.Code, fernworks.Body.String())
	}
	missingShop := request(t, handler, http.MethodGet, "/admin/api/2024-10/shop.json", "", "northwind-market.myshopify.com", bearer())
	if missingShop.Code != http.StatusOK || !strings.Contains(missingShop.Body.String(), `"name":"Northwind Market"`) {
		t.Fatalf("northwind shop = %d %s", missingShop.Code, missingShop.Body.String())
	}
}

func TestTwoInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-goods.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/admin/api/2024-10/orders.json", `{"order":{"email":"priya@fernworks.example","line_items":[{"variant_id":2001,"quantity":1}]}}`, "", bearer())
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/admin/api/2024-10/orders/100001.json", "", "", bearer())
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
