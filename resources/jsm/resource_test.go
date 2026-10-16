package jsm

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
	"github.com/dumbmachine/fabricate/resources/jsm/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_jsm_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "jsm.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-desk.v1.json")
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
	if len(state.ServiceDesks) != 1 || state.ServiceDesks[0].ProjectKey != "SUP" || state.ServiceDesks[0].ID != "1" {
		t.Fatalf("service desk = %+v", state.ServiceDesks)
	}
	if len(state.RequestTypes) != 2 || len(state.Users) != 4 || len(state.Requests) != 4 {
		t.Fatalf("counts types=%d users=%d requests=%d", len(state.RequestTypes), len(state.Users), len(state.Requests))
	}
	if len(state.Comments) != 0 || !bytes.Contains(after, []byte(`"comments":[]`)) {
		t.Fatalf("empty comments were not dumped as []: %s", after)
	}
	byKey := map[string]fixtureRequest{}
	for _, request := range state.Requests {
		byKey[request.IssueKey] = request
	}
	if byKey["ITSM-4812"].Summary != "Duplicate charge INV-4812" || byKey["ITSM-4812"].ReporterAccountID != "user-dana" {
		t.Fatalf("ITSM-4812 = %+v", byKey["ITSM-4812"])
	}
	if byKey["ITSM-10483"].Summary != "NDR on Acme Goods #10483" || byKey["ITSM-10483"].ReporterAccountID != "user-sam" {
		t.Fatalf("ITSM-10483 = %+v", byKey["ITSM-10483"])
	}
	if !strings.Contains(byKey["ITSM-SSO"].Description, "contoso-eu") || byKey["ITSM-SSO"].ReporterAccountID != "user-mei" {
		t.Fatalf("ITSM-SSO = %+v", byKey["ITSM-SSO"])
	}
	if byKey["ITSM-1188"].Summary != "Cancel TinyShop annual Pro" || !strings.Contains(byKey["ITSM-1188"].Description, "INV-1188") || byKey["ITSM-1188"].ReporterAccountID != "user-marco" {
		t.Fatalf("ITSM-1188 = %+v", byKey["ITSM-1188"])
	}
	if len(byKey["ITSM-10483"].ParticipantAccountIDs) != 0 || !bytes.Contains(after, []byte(`"participantAccountIds":[]`)) {
		t.Fatalf("empty participants were not dumped as []: %s", after)
	}
}

func TestMinimalScenarioRoundTripsEmptySlices(t *testing.T) {
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
	for _, key := range []string{"serviceDesks", "requestTypes", "requests", "comments"} {
		if !bytes.Contains(after, []byte(`"`+key+`":[]`)) {
			t.Fatalf("minimal %s was not dumped as []: %s", key, after)
		}
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	codec := scenarioCodec{}
	doc := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["queue"] = "extra"
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("unknown state field was accepted")
	}

	doc = loadScenario(t, "acme-desk.v1.json")
	state = map[string]any{}
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	requests := state["requests"].([]any)
	request := requests[0].(map[string]any)
	request["priority"] = "high"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("unknown request field was accepted")
	}

	doc = loadScenario(t, "minimal.v1.json")
	doc.ResourceVersion = "2"
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("wrong resource version was accepted")
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
	want := []string{
		"getServiceDesks", "getServiceDeskById", "getRequestTypes", "getRequestTypeById",
		"getCustomerRequests", "createCustomerRequest", "getCustomerRequestByIdOrKey",
		"getRequestComments", "createRequestComment", "getRequestCommentById",
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
	if descriptor.ID != "jsm" || descriptor.DisplayName != "Jira Service Management" || descriptor.Version != "3" {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "acme.atlassian.net" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	if descriptor.HostPrefixes["acme.atlassian.net"] != "/rest/servicedeskapi/" {
		t.Fatalf("host prefixes = %v", descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-desk.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "errorMessage") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	basic := httptest.NewRequest(http.MethodGet, "/rest/servicedeskapi/request", nil)
	basic.Header.Set("Authorization", "Basic dXNlcjp0b2tlbg==")
	basicRecorder := httptest.NewRecorder()
	handler.ServeHTTP(basicRecorder, basic)
	if basicRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("basic auth = %d %s", basicRecorder.Code, basicRecorder.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request", "", testToken)
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	for _, key := range []string{"ITSM-4812", "ITSM-10483", "ITSM-SSO", "ITSM-1188"} {
		if !strings.Contains(listed.Body.String(), key) {
			t.Fatalf("list missing %s: %s", key, listed.Body.String())
		}
	}
	if !strings.Contains(listed.Body.String(), `"size":4`) || !strings.Contains(listed.Body.String(), "dana@northwind.example") {
		t.Fatalf("list = %s", listed.Body.String())
	}

	sso := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request/ITSM-SSO", "", testToken)
	if sso.Code != http.StatusOK || !strings.Contains(sso.Body.String(), "contoso-eu") || !strings.Contains(sso.Body.String(), "mei.chen@contoso.example") {
		t.Fatalf("sso = %d %s", sso.Code, sso.Body.String())
	}
	cancel := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request/ITSM-1188", "", testToken)
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), "INV-1188") || !strings.Contains(cancel.Body.String(), "marco@tinyshop.example") {
		t.Fatalf("cancel = %d %s", cancel.Code, cancel.Body.String())
	}
	charge := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request/4812", "", testToken)
	if charge.Code != http.StatusOK || !strings.Contains(charge.Body.String(), "Duplicate charge INV-4812") || !strings.Contains(charge.Body.String(), "dana@northwind.example") {
		t.Fatalf("charge = %d %s", charge.Code, charge.Body.String())
	}
	ndr := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request?searchTerm=NDR", "", testToken)
	if ndr.Code != http.StatusOK || !strings.Contains(ndr.Body.String(), "ITSM-10483") || strings.Contains(ndr.Body.String(), "ITSM-4812") {
		t.Fatalf("ndr search = %d %s", ndr.Code, ndr.Body.String())
	}

	desk := request(t, handler, http.MethodGet, "/rest/servicedeskapi/servicedesk/SUP", "", testToken)
	if desk.Code != http.StatusOK || !strings.Contains(desk.Body.String(), `"projectKey":"SUP"`) || !strings.Contains(desk.Body.String(), `"id":"1"`) {
		t.Fatalf("desk = %d %s", desk.Code, desk.Body.String())
	}

	comment := request(t, handler, http.MethodPost, "/rest/servicedeskapi/request/ITSM-4812/comment", `{"body":"Refund queued for INV-4812.","public":true}`, testToken)
	if comment.Code != http.StatusCreated || !strings.Contains(comment.Body.String(), "Refund queued for INV-4812.") || !strings.Contains(comment.Body.String(), "sam@acme.example") {
		t.Fatalf("comment = %d %s", comment.Code, comment.Body.String())
	}
	comments := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request/ITSM-4812/comment", "", testToken)
	if comments.Code != http.StatusOK || !strings.Contains(comments.Body.String(), "Refund queued for INV-4812.") || !strings.Contains(comments.Body.String(), `"size":1`) {
		t.Fatalf("comments = %d %s", comments.Code, comments.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"1","requestTypeId":"1","requestFieldValues":{"summary":"Follow up Northwind","description":"Check INV-4812"}}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"issueKey":"SUP-jsm.request-0001"`) || !strings.Contains(created.Body.String(), "sam@acme.example") {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "SUP-jsm.request-0001") || !strings.Contains(again.Body.String(), `"size":5`) {
		t.Fatalf("list after create = %d %s", again.Code, again.Body.String())
	}

	dumped, err := (scenarioCodec{}).Dump(context.Background(), db, scenario.Metadata{ID: "jsm.acme-desk.v1", Resource: "jsm", ResourceVersion: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dumped.State, []byte("Refund queued for INV-4812.")) || !bytes.Contains(dumped.State, []byte("Follow up Northwind")) {
		t.Fatalf("dump lost the write: %s", dumped.State)
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-desk.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/rest/servicedeskapi/request/ITSM-404", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"errorMessage"`) || !strings.Contains(missing.Body.String(), `"parameters":[]`) {
		t.Fatalf("missing request = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"1","requestTypeId":"1","requestFieldValues":{"description":"no summary"}}`, testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "summary is required") {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	unknown := request(t, handler, http.MethodPost, "/rest/servicedeskapi/request/ITSM-4812/comment", `{"body":"Hello","public":true,"extra":1}`, testToken)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown comment field = %d %s", unknown.Code, unknown.Body.String())
	}
}

func TestTwoJSMInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-desk.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/rest/servicedeskapi/request/ITSM-SSO/comment", `{"body":"Checked the contoso-eu ACS."}`, testToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("comment = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/rest/servicedeskapi/request/ITSM-SSO/comment", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"size":0`) || strings.Contains(untouched.Body.String(), "contoso-eu ACS") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
