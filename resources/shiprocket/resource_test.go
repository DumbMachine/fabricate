package shiprocket

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
	"github.com/dumbmachine/fabricate/resources/shiprocket/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_shiprocket_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "shiprocket.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func TestScenariosValidateAndRoundTrip(t *testing.T) {
	codec := scenarioCodec{}
	cases := []struct {
		file     string
		orders   int
		returns  int
		couriers int
		pickups  int
	}{
		{file: "minimal.v1.json"},
		{file: "acme-shipments.v1.json", orders: 3, returns: 1, couriers: 2, pickups: 1},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			doc := loadScenario(t, tc.file)
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
			if strings.Contains(string(after), `"orders":null`) || strings.Contains(string(after), `"returns":null`) ||
				strings.Contains(string(after), `"couriers":null`) || strings.Contains(string(after), `"pickups":null`) {
				t.Fatalf("nil slice dumped as null: %s", after)
			}
			var state fixtureState
			if err := json.Unmarshal(dumped.State, &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Orders) != tc.orders || len(state.Returns) != tc.returns || len(state.Couriers) != tc.couriers || len(state.Pickups) != tc.pickups {
				t.Fatalf("counts orders=%d returns=%d couriers=%d pickups=%d", len(state.Orders), len(state.Returns), len(state.Couriers), len(state.Pickups))
			}
		})
	}

	doc := loadScenario(t, "acme-shipments.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	byID := map[string]fixtureOrder{}
	for _, order := range state.Orders {
		byID[order.ID] = order
	}
	if byID["10482"].Awb != "SR10482AWB" || byID["10482"].CourierName != "Delhivery" || byID["10482"].Status != "in_transit" || byID["10482"].ChannelName != "Acme Goods" {
		t.Fatalf("10482 = %+v", byID["10482"])
	}
	if byID["10483"].Awb != "SR10483AWB" || byID["10483"].CourierName != "Bluedart" || byID["10483"].Status != "ndr" || byID["10483"].NdrReason != "Customer not available" {
		t.Fatalf("10483 = %+v", byID["10483"])
	}
	if byID["10490"].Awb != "SR10490AWB" || byID["10490"].Status != "delivered" || byID["10490"].ChannelName != "Northwind Market" || byID["10490"].City != "" {
		t.Fatalf("10490 = %+v", byID["10490"])
	}
	if state.Returns[0].ID != "RET-10484" || state.Returns[0].OrderID != "10484" || state.Returns[0].ForwardShipmentStatus != "cancelled" {
		t.Fatalf("return = %+v", state.Returns[0])
	}
}

func TestScenarioRejectsUnknownAndInconsistentState(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "acme-shipments.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["warehouseCode"] = "BLR-1"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail validation")
	}

	doc = loadScenario(t, "acme-shipments.v1.json")
	var decoded fixtureState
	if err := json.Unmarshal(doc.State, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Orders[1].Awb = decoded.Orders[0].Awb
	raw, err = json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected duplicate AWB to fail validation")
	}

	doc = loadScenario(t, "minimal.v1.json")
	doc.ResourceVersion = "v2"
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected wrong resource version to fail validation")
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
	if len(seen) != 16 {
		t.Fatalf("compiled operation count = %d, want 16", len(seen))
	}
	if _, ok := seen["login"]; !ok {
		t.Fatal("login operation is missing")
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "apiv2.shiprocket.in" {
		t.Fatalf("provider hosts = %v", hosts)
	}
	if resource.Descriptor().HostPrefixes != nil {
		t.Fatalf("host prefixes = %v", resource.Descriptor().HostPrefixes)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-shipments.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/external/orders", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Unauthorized") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1/external/orders", "", "other-token")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	login := request(t, handler, http.MethodPost, "/v1/external/auth/login", `{"email":"api-user@acme.example","password":"synthetic"}`, "")
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"token":"`+testToken+`"`) || !strings.Contains(login.Body.String(), `"first_name":"Ravi"`) {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}

	orders := request(t, handler, http.MethodGet, "/v1/external/orders", "", testToken)
	if orders.Code != http.StatusOK || !strings.Contains(orders.Body.String(), `"SR10482AWB"`) || !strings.Contains(orders.Body.String(), `"SR10490AWB"`) || !strings.Contains(orders.Body.String(), `"total":3`) {
		t.Fatalf("orders = %d %s", orders.Code, orders.Body.String())
	}
	order := request(t, handler, http.MethodGet, "/v1/external/orders/show/10482", "", testToken)
	if order.Code != http.StatusOK || !strings.Contains(order.Body.String(), "Priya Nair") || !strings.Contains(order.Body.String(), "AG-TEE-02") || !strings.Contains(order.Body.String(), "AG-MUG-01") || !strings.Contains(order.Body.String(), "Acme Goods") {
		t.Fatalf("order 10482 = %d %s", order.Code, order.Body.String())
	}
	tracked := request(t, handler, http.MethodGet, "/v1/external/courier/track/awb/SR10482AWB", "", testToken)
	if tracked.Code != http.StatusOK || !strings.Contains(tracked.Body.String(), `"current_status":"in_transit"`) || !strings.Contains(tracked.Body.String(), `"courier_name":"Delhivery"`) || !strings.Contains(tracked.Body.String(), `"channel_name":"Acme Goods"`) {
		t.Fatalf("track 10482 = %d %s", tracked.Code, tracked.Body.String())
	}
	ndr := request(t, handler, http.MethodGet, "/v1/external/ndr/all", "", testToken)
	if ndr.Code != http.StatusOK || !strings.Contains(ndr.Body.String(), `"channel_order_id":"10483"`) || !strings.Contains(ndr.Body.String(), "Customer not available") || !strings.Contains(ndr.Body.String(), "Bluedart") {
		t.Fatalf("ndr = %d %s", ndr.Code, ndr.Body.String())
	}
	one := request(t, handler, http.MethodGet, "/v1/external/ndr/SR10483AWB", "", testToken)
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), `"awb_code":"SR10483AWB"`) {
		t.Fatalf("ndr get = %d %s", one.Code, one.Body.String())
	}
	returns := request(t, handler, http.MethodGet, "/v1/external/orders/processing/return", "", testToken)
	if returns.Code != http.StatusOK || !strings.Contains(returns.Body.String(), `"id":"RET-10484"`) || !strings.Contains(returns.Body.String(), `"order_id":"10484"`) || !strings.Contains(returns.Body.String(), `"forward_shipment_status":"cancelled"`) {
		t.Fatalf("returns = %d %s", returns.Code, returns.Body.String())
	}
	couriers := request(t, handler, http.MethodGet, "/v1/external/courier/courierListWithCounts", "", testToken)
	if couriers.Code != http.StatusOK || !strings.Contains(couriers.Body.String(), "Delhivery") || !strings.Contains(couriers.Body.String(), "Bluedart") {
		t.Fatalf("couriers = %d %s", couriers.Code, couriers.Body.String())
	}
	serviceable := request(t, handler, http.MethodGet, "/v1/external/courier/serviceability?pickup_postcode=560058&delivery_postcode=560025&weight=1&cod=0", "", testToken)
	if serviceable.Code != http.StatusOK || !strings.Contains(serviceable.Body.String(), "Delhivery") || !strings.Contains(serviceable.Body.String(), "Bluedart") {
		t.Fatalf("serviceability = %d %s", serviceable.Code, serviceable.Body.String())
	}
	pickups := request(t, handler, http.MethodGet, "/v1/external/settings/company/pickup", "", testToken)
	if pickups.Code != http.StatusOK || !strings.Contains(pickups.Body.String(), "BLR-1") || !strings.Contains(pickups.Body.String(), "18 Industrial Layout") {
		t.Fatalf("pickups = %d %s", pickups.Code, pickups.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1/external/orders/create/adhoc", createOrderBody("10499"), testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"channel_order_id":"10499"`) || !strings.Contains(created.Body.String(), `"status":"NEW"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		OrderID    string `json:"order_id"`
		ShipmentID string `json:"shipment_id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody.OrderID == "" || createdBody.ShipmentID == "" {
		t.Fatalf("create ids = %+v", createdBody)
	}
	follow := request(t, handler, http.MethodGet, "/v1/external/courier/track/shipment/"+createdBody.ShipmentID, "", testToken)
	if follow.Code != http.StatusOK || !strings.Contains(follow.Body.String(), `"channel_order_id":"10499"`) || !strings.Contains(follow.Body.String(), `"shipment_id":"`+createdBody.ShipmentID+`"`) || !strings.Contains(follow.Body.String(), `"weight":"1.00"`) {
		t.Fatalf("track created = %d %s", follow.Code, follow.Body.String())
	}
	assigned := request(t, handler, http.MethodPost, "/v1/external/courier/assign/awb", `{"shipment_id":"`+createdBody.ShipmentID+`","courier_id":10}`, testToken)
	if assigned.Code != http.StatusOK || !strings.Contains(assigned.Body.String(), `"courier_name":"Delhivery"`) || !strings.Contains(assigned.Body.String(), `"awb_code":"shiprocket.awb-0001"`) {
		t.Fatalf("assign = %d %s", assigned.Code, assigned.Body.String())
	}
	var assignedBody struct {
		Response struct {
			Data struct {
				Awb string `json:"awb_code"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(assigned.Body.Bytes(), &assignedBody); err != nil {
		t.Fatal(err)
	}
	afterAssign := request(t, handler, http.MethodGet, "/v1/external/courier/track/awb/"+assignedBody.Response.Data.Awb, "", testToken)
	if afterAssign.Code != http.StatusOK || !strings.Contains(afterAssign.Body.String(), `"current_status":"ready_to_ship"`) || !strings.Contains(afterAssign.Body.String(), `"channel_order_id":"10499"`) || !strings.Contains(afterAssign.Body.String(), "2026-08-26 12:00") {
		t.Fatalf("track assigned = %d %s", afterAssign.Code, afterAssign.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/v1/external/orders?search=10499", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"channel_order_id":"10499"`) || !strings.Contains(again.Body.String(), assignedBody.Response.Data.Awb) {
		t.Fatalf("orders after create = %d %s", again.Code, again.Body.String())
	}

	action := request(t, handler, http.MethodPost, "/v1/external/ndr/SR10483AWB/action", `{"action":"re-attempt","comments":"Evening delivery"}`, testToken)
	if action.Code != http.StatusOK || !strings.Contains(action.Body.String(), "Evening delivery") || !strings.Contains(action.Body.String(), `"attempts":2`) {
		t.Fatalf("ndr action = %d %s", action.Code, action.Body.String())
	}
	ndr = request(t, handler, http.MethodGet, "/v1/external/ndr/all", "", testToken)
	if !strings.Contains(ndr.Body.String(), "Evening delivery") || !strings.Contains(ndr.Body.String(), `"channel_order_id":"10483"`) {
		t.Fatalf("ndr after action = %s", ndr.Body.String())
	}

	createdReturn := request(t, handler, http.MethodPost, "/v1/external/orders/create/return", createOrderBody("10485"), testToken)
	if createdReturn.Code != http.StatusOK || !strings.Contains(createdReturn.Body.String(), `"id":"shiprocket.return-0001"`) || !strings.Contains(createdReturn.Body.String(), `"order_id":"10485"`) {
		t.Fatalf("create return = %d %s", createdReturn.Code, createdReturn.Body.String())
	}
	returns = request(t, handler, http.MethodGet, "/v1/external/orders/processing/return", "", testToken)
	if !strings.Contains(returns.Body.String(), "RET-10484") || !strings.Contains(returns.Body.String(), "shiprocket.return-0001") {
		t.Fatalf("returns after create = %s", returns.Body.String())
	}

	cancelled := request(t, handler, http.MethodPost, "/v1/external/orders/cancel", `{"ids":["10490"]}`, testToken)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", cancelled.Code, cancelled.Body.String())
	}
	cancelledOrder := request(t, handler, http.MethodGet, "/v1/external/orders/show/10490", "", testToken)
	if cancelledOrder.Code != http.StatusOK || !strings.Contains(cancelledOrder.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancelled order = %d %s", cancelledOrder.Code, cancelledOrder.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-shipments.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1/external/orders/show/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "order not found") {
		t.Fatalf("missing order = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/v1/external/orders/create/adhoc", `{}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	duplicate := request(t, handler, http.MethodPost, "/v1/external/orders/create/adhoc", createOrderBody("10482"), testToken)
	if duplicate.Code != http.StatusUnprocessableEntity || !strings.Contains(duplicate.Body.String(), "order_id already exists") {
		t.Fatalf("duplicate = %d %s", duplicate.Code, duplicate.Body.String())
	}
	page := request(t, handler, http.MethodGet, "/v1/external/orders?per_page=0", "", testToken)
	if page.Code != http.StatusBadRequest {
		t.Fatalf("per_page = %d %s", page.Code, page.Body.String())
	}
	missingAWB := request(t, handler, http.MethodGet, "/v1/external/courier/track/awb/missing", "", testToken)
	if missingAWB.Code != http.StatusNotFound {
		t.Fatalf("missing awb = %d %s", missingAWB.Code, missingAWB.Body.String())
	}
}

func TestTwoShiprocketInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-shipments.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	response := request(t, first, http.MethodPost, "/v1/external/orders/cancel", `{"ids":["10490"]}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/external/orders/show/10490", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"status":"delivered"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

func createOrderBody(orderID string) string {
	return `{
		"order_id": "` + orderID + `",
		"order_date": "2026-08-26 12:00",
		"pickup_location": "BLR-1",
		"billing_customer_name": "Priya",
		"billing_last_name": "Nair",
		"billing_address": "42 Residency Road",
		"billing_city": "Bengaluru",
		"billing_pincode": "560025",
		"billing_state": "KA",
		"billing_country": "IN",
		"billing_email": "priya@fernworks.example",
		"billing_phone": "+91-98450-11223",
		"shipping_is_billing": true,
		"order_items": [{"name": "Acme Field Tee", "sku": "AG-TEE-02", "units": 1, "selling_price": 1499}],
		"payment_method": "Prepaid",
		"sub_total": 1499,
		"length": 10,
		"breadth": 10,
		"height": 5,
		"weight": 1
	}`
}
