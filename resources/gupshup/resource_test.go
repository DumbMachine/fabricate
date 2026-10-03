package gupshup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/gupshup/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_gupshup_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "gupshup.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	return requestWith(t, handler, method, path, body, "bearer", token, "")
}

func requestWith(t *testing.T, handler http.Handler, method, path, body, auth, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	switch auth {
	case "bearer":
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "apikey":
		if token != "" {
			req.Header.Set("apikey", token)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func formRequest(t *testing.T, handler http.Handler, path string, form url.Values, auth string) *httptest.ResponseRecorder {
	t.Helper()
	return requestWith(t, handler, http.MethodPost, path, form.Encode(), auth, testToken, "application/x-www-form-urlencoded")
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-whatsapp.v1.json")
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
	if state.App.Name != "Acme Goods" {
		t.Fatalf("app name = %q", state.App.Name)
	}
	if len(state.Templates) != 1 || len(state.Messages) != 4 {
		t.Fatalf("templates=%d messages=%d", len(state.Templates), len(state.Messages))
	}
	byID := map[string]fixtureMessage{}
	for _, message := range state.Messages {
		byID[message.MessageID] = message
	}
	outbound := byID["msg_10482"]
	if outbound.Destination != "+919845011223" || outbound.Status != "delivered" || !strings.Contains(outbound.Text, "10482") || !strings.Contains(outbound.Text, "SR10482AWB") {
		t.Fatalf("msg_10482 = %+v", outbound)
	}
	failed := byID["msg_10483"]
	if failed.Destination != "+919811122008" || failed.Status != "failed" || !strings.Contains(failed.Text, "delivery window") || !strings.Contains(failed.Text, "10483") {
		t.Fatalf("msg_10483 = %+v", failed)
	}
	refund := byID["msg_10484"]
	if refund.Destination != "+919900048120" || refund.Status != "delivered" || !strings.Contains(refund.Text, "rfnd_Acme10484") {
		t.Fatalf("msg_10484 = %+v", refund)
	}
	inbound := byID["msg_10482_in"]
	if inbound.Direction != "inbound" || inbound.Text != "thanks, where is the tee?" || inbound.SenderName != "Priya Nair" || inbound.ContextGsID != "msg_10482" || inbound.Source != "+919845011223" {
		t.Fatalf("msg_10482_in = %+v", inbound)
	}

	resource := NewResource()
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scenario.IDs(docs), ","); got != "gupshup.acme-whatsapp.v1,gupshup.minimal.v1" {
		t.Fatalf("scenario ids = %s", got)
	}
	if _, err := resource.Scenario("gupshup.missing.v1"); err == nil {
		t.Fatal("expected unknown scenario to fail")
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
		t.Fatalf("load/dump changed minimal:\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.Contains(string(dumped.State), `"templates":[]`) || !strings.Contains(string(dumped.State), `"messages":[]`) {
		t.Fatalf("empty slices were not dumped as []: %s", dumped.State)
	}
	if strings.Contains(string(dumped.State), `"templates":null`) || strings.Contains(string(dumped.State), `"messages":null`) {
		t.Fatalf("empty slices dumped as null: %s", dumped.State)
	}
}

func TestUnknownScenarioFieldsFail(t *testing.T) {
	codec := scenarioCodec{}
	minimal := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(minimal.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	raw, _ := json.Marshal(state)
	minimal.State = raw
	if err := codec.Validate(context.Background(), minimal); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("top-level unknown field error = %v", err)
	}

	acme := loadScenario(t, "acme-whatsapp.v1.json")
	state = map[string]any{}
	if err := json.Unmarshal(acme.State, &state); err != nil {
		t.Fatal(err)
	}
	messages := state["messages"].([]any)
	message := messages[0].(map[string]any)
	message["nope"] = true
	raw, _ = json.Marshal(state)
	acme.State = raw
	if err := codec.Validate(context.Background(), acme); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("nested unknown field error = %v", err)
	}

	duplicate := loadScenario(t, "acme-whatsapp.v1.json")
	state = map[string]any{}
	if err := json.Unmarshal(duplicate.State, &state); err != nil {
		t.Fatal(err)
	}
	messages = state["messages"].([]any)
	messages[1].(map[string]any)["messageId"] = "msg_10484"
	raw, _ = json.Marshal(state)
	duplicate.State = raw
	if err := codec.Validate(context.Background(), duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate message error = %v", err)
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
		t.Fatalf("compiled operation count = %d, want 10: %v", len(seen), seen)
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "gupshup" || descriptor.DisplayName != "Gupshup" || descriptor.Version != "v1" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "api.gupshup.io" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-whatsapp.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/wa/api/v1/msg", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "Authentication Failed") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/wa/api/v1/msg", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer = %d %s", wrong.Code, wrong.Body.String())
	}

	withKey := requestWith(t, handler, http.MethodGet, "/wa/api/v1/msg/msg_10482", "", "apikey", testToken, "")
	if withKey.Code != http.StatusOK || !strings.Contains(withKey.Body.String(), "SR10482AWB") || !strings.Contains(withKey.Body.String(), `"status":"delivered"`) {
		t.Fatalf("apikey get = %d %s", withKey.Code, withKey.Body.String())
	}

	business := request(t, handler, http.MethodGet, "/wa/app/app_acme_goods/business", "", testToken)
	if business.Code != http.StatusOK || !strings.Contains(business.Body.String(), `"name":"Acme Goods"`) || !strings.Contains(business.Body.String(), "Ravi Mehta") {
		t.Fatalf("business = %d %s", business.Code, business.Body.String())
	}
	unknownApp := request(t, handler, http.MethodGet, "/wa/app/missing/business", "", testToken)
	if unknownApp.Code != http.StatusBadRequest || !strings.Contains(unknownApp.Body.String(), "Invalid app id provided") {
		t.Fatalf("unknown app = %d %s", unknownApp.Code, unknownApp.Body.String())
	}

	about := request(t, handler, http.MethodGet, "/wa/app/app_acme_goods/business/profile/about", "", testToken)
	if about.Code != http.StatusOK || !strings.Contains(about.Body.String(), "Acme Goods order updates.") {
		t.Fatalf("about = %d %s", about.Code, about.Body.String())
	}
	profile := request(t, handler, http.MethodGet, "/wa/app/app_acme_goods/business/profile", "", testToken)
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), "BLR-1") || !strings.Contains(profile.Body.String(), "560058") {
		t.Fatalf("profile = %d %s", profile.Code, profile.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/wa/api/v1/msg", "", testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"messageId":"msg_10482"`) || !strings.Contains(listed.Body.String(), "rfnd_Acme10484") || !strings.Contains(listed.Body.String(), "thanks, where is the tee?") {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	failed := request(t, handler, http.MethodGet, "/wa/api/v1/msg?status=failed", "", testToken)
	if failed.Code != http.StatusOK || !strings.Contains(failed.Body.String(), "msg_10483") || strings.Contains(failed.Body.String(), "msg_10482\"") {
		t.Fatalf("failed filter = %d %s", failed.Code, failed.Body.String())
	}
	priya := request(t, handler, http.MethodGet, "/wa/api/v1/msg?destination=919845011223", "", testToken)
	if priya.Code != http.StatusOK || !strings.Contains(priya.Body.String(), "msg_10482") || strings.Contains(priya.Body.String(), "msg_10483") {
		t.Fatalf("destination filter = %d %s", priya.Code, priya.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/wa/api/v1/msg/missing", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "Message not found") {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
	badQuery := request(t, handler, http.MethodGet, "/wa/api/v1/msg?direction=sideways", "", testToken)
	if badQuery.Code != http.StatusBadRequest {
		t.Fatalf("bad direction = %d %s", badQuery.Code, badQuery.Body.String())
	}

	templates := request(t, handler, http.MethodGet, "/wa/app/app_acme_goods/template", "", testToken)
	if templates.Code != http.StatusOK || !strings.Contains(templates.Body.String(), "tpl_order_status") || !strings.Contains(templates.Body.String(), "APPROVED") {
		t.Fatalf("templates = %d %s", templates.Code, templates.Body.String())
	}

	sent := formRequest(t, handler, "/wa/api/v1/msg", url.Values{
		"channel":     {"whatsapp"},
		"source":      {"+918041230101"},
		"destination": {"+919811122008"},
		"src.name":    {"Acme Goods"},
		"message":     {`{"type":"text","text":"Please share a delivery window for order 10483."}`},
	}, "bearer")
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"messageId":"gupshup.message-0001"`) || !strings.Contains(sent.Body.String(), `"status":"submitted"`) {
		t.Fatalf("send = %d %s", sent.Code, sent.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/wa/api/v1/msg/gupshup.message-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "delivery window for order 10483") || !strings.Contains(again.Body.String(), `"status":"submitted"`) {
		t.Fatalf("get sent = %d %s", again.Code, again.Body.String())
	}
	after := request(t, handler, http.MethodGet, "/wa/api/v1/msg", "", testToken)
	if !strings.Contains(after.Body.String(), "gupshup.message-0001") {
		t.Fatalf("list after send = %s", after.Body.String())
	}

	templated := formRequest(t, handler, "/wa/api/v1/template/msg", url.Values{
		"source":      {"918041230101"},
		"destination": {"919845011223"},
		"src.name":    {"Acme Goods"},
		"template":    {`{"id":"tpl_order_status","params":["10482","in transit AWB SR10482AWB"]}`},
	}, "apikey")
	if templated.Code != http.StatusOK || !strings.Contains(templated.Body.String(), `"messageId":"gupshup.message-0002"`) {
		t.Fatalf("template send = %d %s", templated.Code, templated.Body.String())
	}
	templatedGet := request(t, handler, http.MethodGet, "/wa/api/v1/msg/gupshup.message-0002", "", testToken)
	if templatedGet.Code != http.StatusOK || !strings.Contains(templatedGet.Body.String(), "Order 10482 update: in transit AWB SR10482AWB") {
		t.Fatalf("template get = %d %s", templatedGet.Code, templatedGet.Body.String())
	}

	invalid := formRequest(t, handler, "/wa/api/v1/msg", url.Values{
		"source":      {"918041230101"},
		"destination": {"not-a-phone"},
		"src.name":    {"Acme Goods"},
		"message":     {`{"type":"text","text":"hello"}`},
	}, "bearer")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "Invalid Destination") {
		t.Fatalf("invalid destination = %d %s", invalid.Code, invalid.Body.String())
	}

	marked := requestWith(t, handler, http.MethodPut, "/wa/app/app_acme_goods/msg/msg_10482_in/read", "", "bearer", testToken, "")
	if marked.Code != http.StatusAccepted {
		t.Fatalf("mark read = %d %s", marked.Code, marked.Body.String())
	}
	inbound := request(t, handler, http.MethodGet, "/wa/api/v1/msg/msg_10482_in", "", testToken)
	if inbound.Code != http.StatusOK || !strings.Contains(inbound.Body.String(), `"read":true`) || !strings.Contains(inbound.Body.String(), "Priya Nair") {
		t.Fatalf("inbound after read = %d %s", inbound.Code, inbound.Body.String())
	}
	outboundRead := requestWith(t, handler, http.MethodPut, "/wa/app/app_acme_goods/msg/msg_10482/read", "", "bearer", testToken, "")
	if outboundRead.Code != http.StatusBadRequest {
		t.Fatalf("outbound mark read = %d %s", outboundRead.Code, outboundRead.Body.String())
	}
}

func TestMinimalListsAreEmptyArrays(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "minimal.v1.json")), &testIDs{})
	messages := request(t, handler, http.MethodGet, "/wa/api/v1/msg", "", testToken)
	if messages.Code != http.StatusOK || !strings.Contains(messages.Body.String(), `"messages":[]`) {
		t.Fatalf("minimal messages = %d %s", messages.Code, messages.Body.String())
	}
	templates := request(t, handler, http.MethodGet, "/wa/app/app_acme_goods/template", "", testToken)
	if templates.Code != http.StatusOK || !strings.Contains(templates.Body.String(), `"templates":[]`) {
		t.Fatalf("minimal templates = %d %s", templates.Code, templates.Body.String())
	}
}

func TestTwoGupshupInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-whatsapp.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	sent := formRequest(t, first, "/wa/api/v1/msg", url.Values{
		"source":      {"918041230101"},
		"destination": {"+919900048120"},
		"src.name":    {"Acme Goods"},
		"message":     {`{"type":"text","text":"Refund rfnd_Acme10484 is on its way."}`},
	}, "bearer")
	if sent.Code != http.StatusOK {
		t.Fatalf("send = %d %s", sent.Code, sent.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/wa/api/v1/msg/gupshup.message-0001", "", testToken)
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
