package greenhouse

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
	"github.com/dumbmachine/fabricate/resources/greenhouse/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_greenhouse_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "greenhouse.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	return requestHeaders(t, handler, method, path, body, token, nil)
}

func requestHeaders(t *testing.T, handler http.Handler, method, path, body, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestScenariosValidateAndRoundTrip(t *testing.T) {
	codec := scenarioCodec{}
	for _, name := range []string{"minimal.v1.json", "acme-pipeline.v1.json"} {
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
			if !bytes.Equal(before, after) {
				t.Fatalf("load/dump changed the baseline:\nbefore=%s\nafter=%s", before, after)
			}
		})
	}

	doc := loadScenario(t, "minimal.v1.json")
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := scenario.CanonicalJSON(dumped)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"users":[]`, `"jobs":[]`, `"candidates":[]`, `"applications":[]`, `"offers":[]`, `"notes":[]`, `"scorecards":[]`} {
		if !bytes.Contains(canonical, []byte(needle)) {
			t.Fatalf("canonical dump missing %s: %s", needle, canonical)
		}
	}

	doc = loadScenario(t, "acme-pipeline.v1.json")
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Jobs) != 1 || state.Jobs[0].ID != "job-support" || state.Jobs[0].Name != "Support Engineer" {
		t.Fatalf("job = %+v", state.Jobs)
	}
	if len(state.Candidates) != 2 || len(state.Applications) != 2 || len(state.Offers) != 1 || len(state.Notes) != 0 || len(state.Scorecards) != 1 {
		t.Fatalf("counts candidates=%d applications=%d offers=%d notes=%d scorecards=%d", len(state.Candidates), len(state.Applications), len(state.Offers), len(state.Notes), len(state.Scorecards))
	}
	stages := map[string]string{}
	for _, stage := range state.JobStages {
		stages[stage.ID] = stage.Name
	}
	for _, app := range state.Applications {
		switch app.ID {
		case "app-jordan":
			if stages[app.StageID] != "Offer" || app.CandidateID != "cand-jordan" {
				t.Fatalf("app-jordan = %+v stage %q", app, stages[app.StageID])
			}
		case "app-sasha":
			if stages[app.StageID] != "On-site" || app.CandidateID != "cand-sasha" {
				t.Fatalf("app-sasha = %+v stage %q", app, stages[app.StageID])
			}
		default:
			t.Fatalf("unexpected application %s", app.ID)
		}
	}
	if state.Offers[0].ID != "offer-jordan" || state.Offers[0].ApplicationID != "app-jordan" || state.Offers[0].Status != "accepted" || state.Offers[0].StartsAt != "2026-09-08" {
		t.Fatalf("offer = %+v", state.Offers[0])
	}
	if state.Candidates[0].Emails[0].Value != "jordan.hale@acme.example" || state.Candidates[1].Emails[0].Value != "sasha.iqbal@example.com" {
		t.Fatalf("candidate emails = %+v %+v", state.Candidates[0].Emails, state.Candidates[1].Emails)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["extra"] = true
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.Parse(encoded); err == nil {
		t.Fatal("expected unknown envelope field to fail")
	}

	doc = map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	state := doc["state"].(map[string]any)
	state["extra"] = true
	encoded, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := scenario.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := (scenarioCodec{}).Validate(context.Background(), parsed); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	acme := loadScenario(t, "acme-pipeline.v1.json")
	var body map[string]any
	if err := json.Unmarshal(acme.State, &body); err != nil {
		t.Fatal(err)
	}
	users := body["users"].([]any)
	user := users[0].(map[string]any)
	user["nickname"] = "LP"
	rawState, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	acme.State = rawState
	if err := (scenarioCodec{}).Validate(context.Background(), acme); err == nil {
		t.Fatal("expected unknown user field to fail")
	}

	if err := json.Unmarshal(loadScenario(t, "acme-pipeline.v1.json").State, &body); err != nil {
		t.Fatal(err)
	}
	apps := body["applications"].([]any)
	apps[0].(map[string]any)["stageId"] = "missing-stage"
	rawState, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	broken := loadScenario(t, "acme-pipeline.v1.json")
	broken.State = rawState
	if err := (scenarioCodec{}).Validate(context.Background(), broken); err == nil {
		t.Fatal("expected unknown stage to fail")
	}

	if err := json.Unmarshal(loadScenario(t, "acme-pipeline.v1.json").State, &body); err != nil {
		t.Fatal(err)
	}
	body["users"].([]any)[0].(map[string]any)["email"] = "not-an-email"
	rawState, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	broken = loadScenario(t, "acme-pipeline.v1.json")
	broken.State = rawState
	if err := (scenarioCodec{}).Validate(context.Background(), broken); err == nil {
		t.Fatal("expected invalid email to fail")
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
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if got, want := resource.Descriptor().DisplayName, "Greenhouse"; got != want {
		t.Fatalf("display name = %s", got)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "harvest.greenhouse.io" {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-pipeline.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1/jobs", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	basic := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	basic.Header.Set("Authorization", "Basic "+testToken)
	basicRecorder := httptest.NewRecorder()
	handler.ServeHTTP(basicRecorder, basic)
	if basicRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("basic auth = %d %s", basicRecorder.Code, basicRecorder.Body.String())
	}

	jobs := request(t, handler, http.MethodGet, "/v1/jobs", "", testToken)
	if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), `"id":"job-support"`) || !strings.Contains(jobs.Body.String(), `"name":"Support Engineer"`) {
		t.Fatalf("jobs = %d %s", jobs.Code, jobs.Body.String())
	}

	candidates := request(t, handler, http.MethodGet, "/v1/candidates", "", testToken)
	if candidates.Code != http.StatusOK || !strings.Contains(candidates.Body.String(), "jordan.hale@acme.example") || !strings.Contains(candidates.Body.String(), "sasha.iqbal@example.com") {
		t.Fatalf("candidates = %d %s", candidates.Code, candidates.Body.String())
	}

	jordan := request(t, handler, http.MethodGet, "/v1/candidates/cand-jordan", "", testToken)
	if jordan.Code != http.StatusOK || !strings.Contains(jordan.Body.String(), `"id":"app-jordan"`) || !strings.Contains(jordan.Body.String(), `"name":"Offer"`) || !strings.Contains(jordan.Body.String(), "offer-letter-jordan-hale.pdf") {
		t.Fatalf("jordan = %d %s", jordan.Code, jordan.Body.String())
	}

	sasha := request(t, handler, http.MethodGet, "/v1/candidates/cand-sasha", "", testToken)
	if sasha.Code != http.StatusOK || !strings.Contains(sasha.Body.String(), `"id":"app-sasha"`) || !strings.Contains(sasha.Body.String(), `"name":"On-site"`) {
		t.Fatalf("sasha = %d %s", sasha.Code, sasha.Body.String())
	}

	offers := request(t, handler, http.MethodGet, "/v1/offers", "", testToken)
	if offers.Code != http.StatusOK || !strings.Contains(offers.Body.String(), `"id":"offer-jordan"`) || !strings.Contains(offers.Body.String(), `"status":"accepted"`) || !strings.Contains(offers.Body.String(), `"starts_at":"2026-09-08"`) || !strings.Contains(offers.Body.String(), "env-offer-jordan") {
		t.Fatalf("offers = %d %s", offers.Code, offers.Body.String())
	}

	app := request(t, handler, http.MethodGet, "/v1/applications/app-jordan", "", testToken)
	if app.Code != http.StatusOK || !strings.Contains(app.Body.String(), `"candidate_id":"cand-jordan"`) || !strings.Contains(app.Body.String(), `"name":"Offer"`) {
		t.Fatalf("application = %d %s", app.Code, app.Body.String())
	}

	appOffers := request(t, handler, http.MethodGet, "/v1/applications/app-jordan/offers", "", testToken)
	if appOffers.Code != http.StatusOK || !strings.Contains(appOffers.Body.String(), `"id":"offer-jordan"`) {
		t.Fatalf("application offers = %d %s", appOffers.Code, appOffers.Body.String())
	}

	note := requestHeaders(t, handler, http.MethodPost, "/v1/candidates/cand-jordan/activity_feed/notes", `{"user_id":"user-leo","body":"Offer letter filed for app-jordan.","visibility":"public"}`, testToken, map[string]string{"On-Behalf-Of": "user-leo"})
	if note.Code != http.StatusCreated || !strings.Contains(note.Body.String(), "app-jordan") || !strings.Contains(note.Body.String(), `"id":"greenhouse.note-0001"`) {
		t.Fatalf("note = %d %s", note.Code, note.Body.String())
	}

	feed := request(t, handler, http.MethodGet, "/v1/candidates/cand-jordan/activity_feed", "", testToken)
	if feed.Code != http.StatusOK || !strings.Contains(feed.Body.String(), "Offer letter filed for app-jordan.") {
		t.Fatalf("feed = %d %s", feed.Code, feed.Body.String())
	}

	appAfter := request(t, handler, http.MethodGet, "/v1/applications/app-jordan", "", testToken)
	if appAfter.Code != http.StatusOK || !strings.Contains(appAfter.Body.String(), `"last_activity_at":"2026-08-26T12:00:00Z"`) {
		t.Fatalf("application after note = %d %s", appAfter.Code, appAfter.Body.String())
	}

	again := request(t, handler, http.MethodGet, "/v1/candidates/cand-jordan", "", testToken)
	if !strings.Contains(again.Body.String(), `"last_activity":"2026-08-26T12:00:00Z"`) {
		t.Fatalf("candidate after note = %s", again.Body.String())
	}

	sashaApp := request(t, handler, http.MethodGet, "/v1/applications/app-sasha", "", testToken)
	if !strings.Contains(sashaApp.Body.String(), `"last_activity_at":"2026-08-24T10:30:00Z"`) {
		t.Fatalf("sasha application changed = %s", sashaApp.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-pipeline.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1/candidates/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "candidate not found") {
		t.Fatalf("missing candidate = %d %s", missing.Code, missing.Body.String())
	}

	invalid := request(t, handler, http.MethodGet, "/v1/jobs?per_page=0", "", testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid page size = %d %s", invalid.Code, invalid.Body.String())
	}

	unknownUser := requestHeaders(t, handler, http.MethodPost, "/v1/candidates/cand-jordan/activity_feed/notes", `{"user_id":"missing","body":"hello","visibility":"public"}`, testToken, map[string]string{"On-Behalf-Of": "user-leo"})
	if unknownUser.Code != http.StatusUnprocessableEntity || !strings.Contains(unknownUser.Body.String(), "user_id") {
		t.Fatalf("unknown user = %d %s", unknownUser.Code, unknownUser.Body.String())
	}

	filtered := request(t, handler, http.MethodGet, "/v1/candidates?email=sasha.iqbal@example.com", "", testToken)
	if filtered.Code != http.StatusOK || strings.Contains(filtered.Body.String(), "jordan.hale@acme.example") || !strings.Contains(filtered.Body.String(), "sasha.iqbal@example.com") {
		t.Fatalf("email filter = %d %s", filtered.Code, filtered.Body.String())
	}
}

func TestTwoGreenhouseInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-pipeline.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	note := requestHeaders(t, first, http.MethodPost, "/v1/candidates/cand-jordan/activity_feed/notes", `{"user_id":"user-leo","body":"Only on the first instance.","visibility":"private"}`, testToken, map[string]string{"On-Behalf-Of": "user-leo"})
	if note.Code != http.StatusCreated {
		t.Fatalf("note = %d %s", note.Code, note.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/v1/candidates/cand-jordan/activity_feed", "", testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Only on the first instance") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
