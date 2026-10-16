package gainsight

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
	"github.com/dumbmachine/fabricate/resources/gainsight/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_gainsight_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "gainsight.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-success.v1.json")
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
	if len(state.Companies) != 4 || len(state.CTAs) != 4 || len(state.SuccessPlans) != 4 || len(state.Activities) != 0 {
		t.Fatalf("counts companies=%d ctas=%d plans=%d activities=%d", len(state.Companies), len(state.CTAs), len(state.SuccessPlans), len(state.Activities))
	}
	if !strings.Contains(string(dumped.State), `"activities":[]`) && !strings.Contains(string(after), `"activities":[]`) {
		t.Fatalf("empty activities were not dumped as an array: %s", after)
	}
}

func TestMinimalScenarioRoundTrips(t *testing.T) {
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
	if !strings.Contains(string(after), `"companies":[]`) || !strings.Contains(string(after), `"ctas":[]`) || !strings.Contains(string(after), `"activities":[]`) {
		t.Fatalf("empty slices were not preserved: %s", after)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	doc.State = []byte(`{"companies":[],"ctas":[],"successPlans":[],"activities":[],"extra":true}`)
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	populated := loadScenario(t, "acme-success.v1.json")
	populated.State = bytesReplace(t, populated.State, `"gsid": "company-contoso"`, `"gsid": "company-contoso", "nope": true`)
	if err := (scenarioCodec{}).Validate(context.Background(), populated); err == nil {
		t.Fatal("expected unknown company field to fail")
	}
}

func TestScenarioValidation(t *testing.T) {
	doc := loadScenario(t, "acme-success.v1.json")
	doc.State = bytesReplace(t, doc.State, `"health": "Red"`, `"health": "Blue"`)
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected invalid health to fail")
	}

	doc = loadScenario(t, "acme-success.v1.json")
	doc.State = bytesReplace(t, doc.State, `"companyId": "company-northwind"`, `"companyId": "company-missing"`)
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected dangling company reference to fail")
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
	if got, want := resource.Descriptor().ID, "gainsight"; got != want {
		t.Fatalf("id = %s", got)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "acme.gainsightcloud.com" {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-success.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodPost, "/v1/data/objects/query/Company", `{"select":["Name"]}`, "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodPost, "/v1/data/objects/query/Company", `{"select":["Name"]}`, "other")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	listed := request(t, handler, http.MethodPost, "/v1/data/objects/query/Company", `{"select":["Name","Gsid","Health__gc","Status"],"limit":100,"offset":0}`, testToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"result":true`) {
		t.Fatalf("list companies = %d %s", listed.Code, listed.Body.String())
	}
	for _, want := range []string{`"Name":"Northwind Traders"`, `"Health__gc":"Red"`, `"Name":"TinyShop"`, `"Health__gc":"Yellow"`, `"Name":"Fernworks"`, `"Health__gc":"Green"`, `"Name":"Contoso"`} {
		if !strings.Contains(listed.Body.String(), want) {
			t.Fatalf("list companies missing %s in %s", want, listed.Body.String())
		}
	}

	got := request(t, handler, http.MethodPost, "/v1/data/objects/query/Company", `{"select":["Name","Gsid","ARR","Domain__gc"],"where":{"conditions":[{"name":"Gsid","alias":"A","value":["company-northwind"],"operator":"EQ"}],"expression":"A"}}`, testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"Name":"Northwind Traders"`) || !strings.Contains(got.Body.String(), `"ARR":1240`) || !strings.Contains(got.Body.String(), `"Domain__gc":"northwind.example"`) {
		t.Fatalf("get company = %d %s", got.Code, got.Body.String())
	}

	ctas := request(t, handler, http.MethodPost, "/v2/cockpit/cta/list", `{"select":["Name","Gsid","Comments","CompanyId"],"pageSize":100,"pageNumber":1}`, testToken)
	if ctas.Code != http.StatusOK {
		t.Fatalf("list ctas = %d %s", ctas.Code, ctas.Body.String())
	}
	for _, want := range []string{"INV-4812", "INV-1188", "30-day", "INV-2207", "contoso-eu"} {
		if !strings.Contains(ctas.Body.String(), want) {
			t.Fatalf("list ctas missing %s in %s", want, ctas.Body.String())
		}
	}

	plans := request(t, handler, http.MethodPost, "/v2/successPlan/list/", `{"select":["Name","ActionPlan","CompanyId"],"pageSize":100,"pageNumber":1}`, testToken)
	if plans.Code != http.StatusOK || !strings.Contains(plans.Body.String(), "INV-4812") || !strings.Contains(plans.Body.String(), "contoso-eu") {
		t.Fatalf("list success plans = %d %s", plans.Code, plans.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1/ant/es/activity", `{
		"records":[{
			"ExternalId":"ext-conformance-4812",
			"ContextName":"CTA",
			"ContextId":"cta-inv-4812",
			"GsCompanyId":"company-northwind",
			"Author":"sam@acme.example",
			"TypeName":"Update",
			"Subject":"Finance note on INV-4812",
			"Notes":"Duplicate charge INV-4812 is still open."
		}]
	}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"activityId":"gainsight.activity-0001"`) {
		t.Fatalf("create activity = %d %s", created.Code, created.Body.String())
	}

	persisted := request(t, handler, http.MethodPost, "/v1/data/objects/query/activity_timeline", `{"select":["ExternalId","Notes","ContextId","ActivityDate"],"where":{"conditions":[{"name":"ExternalId","alias":"A","value":["ext-conformance-4812"],"operator":"EQ"}],"expression":"A"}}`, testToken)
	if persisted.Code != http.StatusOK || !strings.Contains(persisted.Body.String(), "still open") || !strings.Contains(persisted.Body.String(), "cta-inv-4812") || !strings.Contains(persisted.Body.String(), "2026-08-26T12:00:00.000Z") {
		t.Fatalf("read activity = %d %s", persisted.Code, persisted.Body.String())
	}

	task := request(t, handler, http.MethodPost, "/v2/cockpit/cta/", `{"requests":[{"record":{"referenceId":"1","Name":"Follow up INV-4812","CompanyId":"company-northwind","OwnerEmail":"sam@acme.example","type":"Risk","status":"New","priority":"High","Comments":"Second look at INV-4812."}}]}`, testToken)
	if task.Code != http.StatusOK || !strings.Contains(task.Body.String(), `"1":"gainsight.cta-0001"`) {
		t.Fatalf("create cta = %d %s", task.Code, task.Body.String())
	}
	tasks := request(t, handler, http.MethodPost, "/v2/cockpit/cta/list", `{"select":["Name","Comments"],"where":{"conditions":[{"fieldName":"Name","alias":"A","value":["Follow up INV-4812"],"operator":"EQ"}],"expression":"A"}}`, testToken)
	if tasks.Code != http.StatusOK || !strings.Contains(tasks.Body.String(), "Second look at INV-4812") {
		t.Fatalf("read created cta = %d %s", tasks.Code, tasks.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-success.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	invalid := request(t, handler, http.MethodPost, "/v1/data/objects/query/Company", `{"select":["NotAField"]}`, testToken)
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `"result":false`) || !strings.Contains(invalid.Body.String(), "Invalid fields") {
		t.Fatalf("invalid select = %d %s", invalid.Code, invalid.Body.String())
	}

	missing := request(t, handler, http.MethodDelete, "/v1/data/objects/Company/missing", "", testToken)
	if missing.Code != http.StatusOK || !strings.Contains(missing.Body.String(), "No data found") {
		t.Fatalf("missing company = %d %s", missing.Code, missing.Body.String())
	}

	bad := request(t, handler, http.MethodPost, "/v1/data/objects/Company", `{"records":[{"Name":"Helix Bio","Health__gc":"Purple"}]}`, testToken)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid health = %d %s", bad.Code, bad.Body.String())
	}
}

func TestTwoGainsightInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-success.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/v1/ant/es/activity", `{"records":[{"ExternalId":"ext-iso","ContextName":"Company","GsCompanyId":"company-fernworks","Author":"sam@acme.example","TypeName":"Update","Subject":"Expansion note","Notes":"INV-2207 stays paid."}]}`, testToken)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":true`) {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodPost, "/v1/data/objects/query/activity_timeline", `{"select":["Notes"]}`, testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "INV-2207 stays paid") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

func bytesReplace(t *testing.T, raw []byte, old, new string) []byte {
	t.Helper()
	if !strings.Contains(string(raw), old) {
		t.Fatalf("scenario fixture missing %s", old)
	}
	return []byte(strings.Replace(string(raw), old, new, 1))
}
