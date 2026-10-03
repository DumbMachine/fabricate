package okta

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
	"github.com/dumbmachine/fabricate/resources/okta/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_okta_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "okta.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func requestHeader(t *testing.T, handler http.Handler, method, path, body, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-org.v1.json")
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
	if len(state.Users) != 9 || len(state.Groups) != 2 || len(state.Apps) != 2 || len(state.AppUsers) != 2 {
		t.Fatalf("record counts users=%d groups=%d apps=%d appUsers=%d", len(state.Users), len(state.Groups), len(state.Apps), len(state.AppUsers))
	}
	active := 0
	for _, user := range state.Users {
		if user.Status == "ACTIVE" {
			active++
		}
	}
	if active != 7 || state.Users[7].Status != "STAGED" || state.Users[8].Status != "DEPROVISIONED" {
		t.Fatalf("user statuses = %+v", state.Users)
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
	for _, key := range []string{`"users":[]`, `"groups":[]`, `"apps":[]`, `"appUsers":[]`} {
		if !strings.Contains(string(after), key) {
			t.Fatalf("canonical dump missing %s: %s", key, after)
		}
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.Parse(bytesReplace(raw, `"$contract"`, `"extra":true,"$contract"`)); err == nil {
		t.Fatal("expected unknown envelope field to fail")
	}
	doc := loadScenario(t, "acme-org.v1.json")
	codec := scenarioCodec{}
	doc.State = bytesReplace(doc.State, `"users"`, `"extra":1,"users"`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}
	doc = loadScenario(t, "acme-org.v1.json")
	doc.State = bytesReplace(doc.State, `"firstName"`, `"nickname":"V","firstName"`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown profile field to fail")
	}
	doc = loadScenario(t, "acme-org.v1.json")
	doc.State = bytesReplace(doc.State, `"status": "ACTIVE"`, `"status": "FIRED"`)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected invalid status to fail")
	}
}

func bytesReplace(raw []byte, old, new string) []byte {
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
	want := []string{"listUsers", "getUser", "createUser", "listGroups", "listGroupUsers", "listApplications", "getApplication", "listApplicationUsers"}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing operationId %s", id)
		}
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "acme.okta.com" || resource.Descriptor().HostPrefixes != nil {
		t.Fatalf("descriptor hosts = %#v prefixes = %#v", hosts, resource.Descriptor().HostPrefixes)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-org.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/api/v1/users", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "E0000011") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	ssws := requestHeader(t, handler, http.MethodGet, "/api/v1/users", "", "SSWS "+testToken)
	if ssws.Code != http.StatusUnauthorized {
		t.Fatalf("SSWS token = %d %s", ssws.Code, ssws.Body.String())
	}

	users := request(t, handler, http.MethodGet, "/api/v1/users", "", testToken)
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"val@acme.example"`) || strings.Contains(users.Body.String(), "old.contractor@acme.example") {
		t.Fatalf("users = %d %s", users.Code, users.Body.String())
	}
	if !strings.Contains(users.Body.String(), `"STAGED"`) || strings.Count(users.Body.String(), `"id"`) != 8 {
		t.Fatalf("default user list = %s", users.Body.String())
	}

	deprovisioned := request(t, handler, http.MethodGet, "/api/v1/users?search="+url.QueryEscape(`status eq "DEPROVISIONED"`), "", testToken)
	if deprovisioned.Code != http.StatusOK || !strings.Contains(deprovisioned.Body.String(), "old.contractor@acme.example") {
		t.Fatalf("deprovisioned search = %d %s", deprovisioned.Code, deprovisioned.Body.String())
	}
	staged := request(t, handler, http.MethodGet, "/api/v1/users?filter="+url.QueryEscape(`status eq "STAGED"`), "", testToken)
	if staged.Code != http.StatusOK || !strings.Contains(staged.Body.String(), "jordan.hale@acme.example") || strings.Contains(staged.Body.String(), "val@acme.example") {
		t.Fatalf("staged filter = %d %s", staged.Code, staged.Body.String())
	}

	val := request(t, handler, http.MethodGet, "/api/v1/users/val", "", testToken)
	if val.Code != http.StatusOK || !strings.Contains(val.Body.String(), `"00u-val"`) || !strings.Contains(val.Body.String(), "Okta admin") {
		t.Fatalf("shortname = %d %s", val.Code, val.Body.String())
	}
	contractor := request(t, handler, http.MethodGet, "/api/v1/users/00u-contractor", "", testToken)
	if contractor.Code != http.StatusOK || !strings.Contains(contractor.Body.String(), `"DEPROVISIONED"`) || !strings.Contains(contractor.Body.String(), "old.contractor@acme.example") {
		t.Fatalf("contractor = %d %s", contractor.Code, contractor.Body.String())
	}

	finance := request(t, handler, http.MethodGet, "/api/v1/groups/00g-finance/users", "", testToken)
	if finance.Code != http.StatusOK || !strings.Contains(finance.Body.String(), "aisha@acme.example") || !strings.Contains(finance.Body.String(), "val@acme.example") || strings.Contains(finance.Body.String(), "ravi@acme.example") {
		t.Fatalf("finance members = %d %s", finance.Code, finance.Body.String())
	}
	groups := request(t, handler, http.MethodGet, "/api/v1/groups?q=Fin", "", testToken)
	if groups.Code != http.StatusOK || !strings.Contains(groups.Body.String(), `"Finance"`) || strings.Contains(groups.Body.String(), `"Support"`) {
		t.Fatalf("group query = %d %s", groups.Code, groups.Body.String())
	}

	checkout := request(t, handler, http.MethodGet, "/api/v1/apps/0oa-acme-checkout", "", testToken)
	if checkout.Code != http.StatusOK || !strings.Contains(checkout.Body.String(), "Acme Checkout") || !strings.Contains(checkout.Body.String(), "The SAML ACS for contoso-eu is misconfigured.") {
		t.Fatalf("checkout = %d %s", checkout.Code, checkout.Body.String())
	}
	assigned := request(t, handler, http.MethodGet, "/api/v1/apps/0oa-shopify-admin/users", "", testToken)
	if assigned.Code != http.StatusOK || !strings.Contains(assigned.Body.String(), `"00u-val"`) || !strings.Contains(assigned.Body.String(), `"00u-ravi"`) || strings.Contains(assigned.Body.String(), "00u-aisha") {
		t.Fatalf("shopify assignments = %d %s", assigned.Code, assigned.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/api/v1/users", `{
		"profile": {"firstName":"Casey","lastName":"Ng","email":"casey.ng@acme.example","login":"casey.ng@acme.example","primaryPhone":"+91-80-4123-0199"},
		"credentials": {"password": {"value": "not-a-real-secret"}},
		"groupIds": ["00g-support"]
	}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":"user-0001"`) || !strings.Contains(created.Body.String(), `"ACTIVE"`) || strings.Contains(created.Body.String(), "not-a-real-secret") {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/api/v1/users/user-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "casey.ng@acme.example") {
		t.Fatalf("created user = %d %s", again.Code, again.Body.String())
	}
	support := request(t, handler, http.MethodGet, "/api/v1/groups/00g-support/users", "", testToken)
	if support.Code != http.StatusOK || !strings.Contains(support.Body.String(), "casey.ng@acme.example") || !strings.Contains(support.Body.String(), "sam@acme.example") {
		t.Fatalf("support after create = %d %s", support.Code, support.Body.String())
	}
	listed := request(t, handler, http.MethodGet, "/api/v1/users", "", testToken)
	if !strings.Contains(listed.Body.String(), "casey.ng@acme.example") || strings.Count(listed.Body.String(), `"id"`) != 9 {
		t.Fatalf("list after create = %s", listed.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-org.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/api/v1/users/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"errorCode":"E0000007"`) {
		t.Fatalf("missing user = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/api/v1/users", `{"profile":{}}`, testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "E0000001") {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	extra := request(t, handler, http.MethodPost, "/api/v1/users", `{"profile":{"email":"casey.ng@acme.example","login":"casey.ng@acme.example"},"nope":true}`, testToken)
	if extra.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", extra.Code, extra.Body.String())
	}
	limit := request(t, handler, http.MethodGet, "/api/v1/users?limit=0", "", testToken)
	if limit.Code != http.StatusBadRequest {
		t.Fatalf("limit = %d %s", limit.Code, limit.Body.String())
	}
	duplicate := request(t, handler, http.MethodPost, "/api/v1/users", `{"profile":{"firstName":"Val","lastName":"Ortega","email":"val.other@acme.example","login":"val@acme.example"}}`, testToken)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), "already exists") {
		t.Fatalf("duplicate login = %d %s", duplicate.Code, duplicate.Body.String())
	}
	staged := request(t, handler, http.MethodPost, "/api/v1/users?activate=false", `{"profile":{"firstName":"Casey","lastName":"Ng","email":"casey.ng@acme.example","login":"casey.ng@acme.example"}}`, testToken)
	if staged.Code != http.StatusOK || !strings.Contains(staged.Body.String(), `"STAGED"`) {
		t.Fatalf("staged create = %d %s", staged.Code, staged.Body.String())
	}
}

func TestTwoOktaInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-org.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/api/v1/users", `{"profile":{"firstName":"Casey","lastName":"Ng","email":"casey.ng@acme.example","login":"casey.ng@acme.example"}}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/api/v1/users/user-0001", "", testToken)
	if untouched.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
