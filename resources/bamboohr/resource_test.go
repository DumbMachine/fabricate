package bamboohr

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
	"github.com/dumbmachine/fabricate/resources/bamboohr/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_bamboohr_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "bamboohr.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func TestScenariosValidateAndRoundTrip(t *testing.T) {
	codec := scenarioCodec{}
	for _, name := range []string{"minimal.v1.json", "acme-people.v1.json"} {
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

	minimal := loadScenario(t, "minimal.v1.json")
	db := openTestDB(t, minimal)
	dumped, err := codec.Dump(context.Background(), db, minimal.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range []string{`"departments":[]`, `"employees":[]`, `"timeOffTypes":[]`, `"timeOffRequests":[]`} {
		if !bytes.Contains(dumped.State, []byte(snippet)) {
			t.Fatalf("minimal dump missing %s: %s", snippet, dumped.State)
		}
	}

	doc := loadScenario(t, "acme-people.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Employees) != 8 || len(state.Departments) != 6 || len(state.TimeOffRequests) != 1 {
		t.Fatalf("acme counts employees=%d departments=%d requests=%d", len(state.Employees), len(state.Departments), len(state.TimeOffRequests))
	}
}

func mutateState(t *testing.T, doc scenario.Document, mutate func(map[string]any)) scenario.Document {
	t.Helper()
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	mutate(state)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	return doc
}

func TestScenarioValidationRejectsUnknownAndInvalidFields(t *testing.T) {
	codec := scenarioCodec{}
	unknown := mutateState(t, loadScenario(t, "acme-people.v1.json"), func(state map[string]any) {
		state["extra"] = true
	})
	if err := codec.Validate(context.Background(), unknown); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	nested := mutateState(t, loadScenario(t, "acme-people.v1.json"), func(state map[string]any) {
		state["employees"].([]any)[0].(map[string]any)["nickname"] = "V"
	})
	if err := codec.Validate(context.Background(), nested); err == nil {
		t.Fatal("expected unknown employee field to fail")
	}

	badStatus := mutateState(t, loadScenario(t, "acme-people.v1.json"), func(state map[string]any) {
		state["employees"].([]any)[7].(map[string]any)["status"] = "Onboarding"
	})
	if err := codec.Validate(context.Background(), badStatus); err == nil {
		t.Fatal("expected status Onboarding to fail")
	}

	envelope, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	envelope = bytes.Replace(envelope, []byte(`"$contract"`), []byte(`"extra":true,"$contract"`), 1)
	if _, err := scenario.Parse(envelope); err == nil {
		t.Fatal("expected unknown envelope field to fail")
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
		t.Fatalf("compiled operation count = %d, want 10", len(seen))
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if resource.Descriptor().DisplayName != "BambooHR" || resource.Descriptor().ID != "bamboohr" {
		t.Fatalf("descriptor = %+v", resource.Descriptor())
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-people.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/api/v1/employees", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "invalid synthetic bearer token") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	roster := request(t, handler, http.MethodGet, "/api/v1/employees?fields=workEmail,departmentName,employmentStatus,hireDate", "", testToken)
	if roster.Code != http.StatusOK {
		t.Fatalf("employees = %d %s", roster.Code, roster.Body.String())
	}
	var listed struct {
		Data []struct {
			EmployeeID       string `json:"employeeId"`
			FirstName        string `json:"firstName"`
			LastName         string `json:"lastName"`
			EmploymentStatus string `json:"employmentStatus"`
			HireDate         string `json:"hireDate"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(roster.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Meta.Total != 8 || len(listed.Data) != 8 {
		t.Fatalf("roster count = %d/%d", listed.Meta.Total, len(listed.Data))
	}
	if listed.Data[7].EmployeeID != "190" || listed.Data[7].EmploymentStatus != "Onboarding" || listed.Data[7].HireDate != "2026-09-08" {
		t.Fatalf("jordan = %+v", listed.Data[7])
	}

	directory := request(t, handler, http.MethodGet, "/api/v1/employees/directory", "", testToken)
	if directory.Code != http.StatusOK || !strings.Contains(directory.Body.String(), `"location":"BLR-1"`) || !strings.Contains(directory.Body.String(), "ravi@acme.example") {
		t.Fatalf("directory = %d %s", directory.Code, directory.Body.String())
	}
	self := request(t, handler, http.MethodGet, "/api/v1/employees/0?fields=workEmail", "", testToken)
	if self.Code != http.StatusOK || !strings.Contains(self.Body.String(), "val@acme.example") {
		t.Fatalf("caller = %d %s", self.Code, self.Body.String())
	}

	jordan := request(t, handler, http.MethodGet, "/api/v1/employees/190?fields=firstName,lastName,employmentStatus,hireDate,department", "", testToken)
	if jordan.Code != http.StatusOK || !strings.Contains(jordan.Body.String(), `"employmentStatus":"Onboarding"`) || !strings.Contains(jordan.Body.String(), `"hireDate":"2026-09-08"`) || !strings.Contains(jordan.Body.String(), `"department":"Support"`) {
		t.Fatalf("jordan = %d %s", jordan.Code, jordan.Body.String())
	}

	departments := request(t, handler, http.MethodGet, "/api/v1/meta/lists", "", testToken)
	for _, name := range []string{"Finance", "People", "Support", "Legal", "Logistics", "Engineering"} {
		if !strings.Contains(departments.Body.String(), name) {
			t.Fatalf("departments missing %s: %s", name, departments.Body.String())
		}
	}

	before := request(t, handler, http.MethodGet, "/api/v1/time_off/requests?start=2026-08-01&end=2026-08-31", "", testToken)
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), "warehouse visit") || !strings.Contains(before.Body.String(), `"employeeId":"103"`) {
		t.Fatalf("time off = %d %s", before.Code, before.Body.String())
	}

	whosOut := request(t, handler, http.MethodGet, "/api/v1/time_off/whos_out", "", testToken)
	if whosOut.Code != http.StatusOK || !strings.Contains(whosOut.Body.String(), "Ravi Mehta") {
		t.Fatalf("whos out = %d %s", whosOut.Code, whosOut.Body.String())
	}

	created := request(t, handler, http.MethodPut, "/api/v1/employees/104/time_off/request", `{"status":"requested","start":"2026-09-03","end":"2026-09-03","timeOffTypeId":"1","notes":[{"from":"employee","note":"people ops coverage"}]}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"bamboohr.time-off-request-0001"`) || !strings.Contains(created.Body.String(), "people ops coverage") {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	if created.Header().Get("Location") == "" {
		t.Fatal("create response missing Location")
	}

	after := request(t, handler, http.MethodGet, "/api/v1/time_off/requests?start=2026-08-01&end=2026-09-30", "", testToken)
	if after.Code != http.StatusOK || !strings.Contains(after.Body.String(), "warehouse visit") || !strings.Contains(after.Body.String(), "people ops coverage") {
		t.Fatalf("time off after create = %d %s", after.Code, after.Body.String())
	}

	updated := request(t, handler, http.MethodPut, "/api/v1/time_off/requests/bamboohr.time-off-request-0001/status", `{"status":"approved","note":"approved by HR"}`, testToken)
	if updated.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", updated.Code, updated.Body.String())
	}
	approved := request(t, handler, http.MethodGet, "/api/v1/time_off/requests?start=2026-09-01&end=2026-09-30&status=approved", "", testToken)
	if !strings.Contains(approved.Body.String(), `"status":"approved"`) || !strings.Contains(approved.Body.String(), "approved by HR") {
		t.Fatalf("approved request = %d %s", approved.Code, approved.Body.String())
	}

	invalid := request(t, handler, http.MethodPut, "/api/v1/employees/104/time_off/request", `{"status":"requested","start":"2026-09-04","end":"2026-09-04","timeOffTypeId":"1","extra":true}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", invalid.Code, invalid.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/api/v1/employees/999", "", testToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing employee = %d %s", missing.Code, missing.Body.String())
	}
}

func TestTwoBambooHRInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-people.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPut, "/api/v1/employees/106/time_off/request", `{"status":"approved","start":"2026-09-10","end":"2026-09-10","timeOffTypeId":"1","notes":[{"from":"employee","note":"support coverage"}]}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/api/v1/time_off/requests?start=2026-09-01&end=2026-09-30", "", testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "support coverage") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
