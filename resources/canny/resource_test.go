package canny

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
	"github.com/dumbmachine/fabricate/resources/canny/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_canny_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "canny.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func TestScenarioValidation(t *testing.T) {
	codec := scenarioCodec{}
	minimal := loadScenario(t, "minimal.v1.json")
	if err := codec.Validate(context.Background(), minimal); err != nil {
		t.Fatal(err)
	}
	acme := loadScenario(t, "acme-feedback.v1.json")
	if err := codec.Validate(context.Background(), acme); err != nil {
		t.Fatal(err)
	}

	unknown := acme
	var state map[string]any
	if err := json.Unmarshal(acme.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	unknown.State = raw
	if err := codec.Validate(context.Background(), unknown); err == nil {
		t.Fatal("expected unknown state field to fail validation")
	}

	var envelope map[string]any
	encoded, err := json.Marshal(acme)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["extra"] = true
	envelopeRaw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.Parse(envelopeRaw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown envelope field to fail, got %v", err)
	}
}

func TestLoadDumpCanonicalRoundTrip(t *testing.T) {
	codec := scenarioCodec{}
	for _, name := range []string{"minimal.v1.json", "acme-feedback.v1.json"} {
		t.Run(name, func(t *testing.T) {
			doc := loadScenario(t, name)
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

	doc := loadScenario(t, "minimal.v1.json")
	db := openTestDB(t, doc)
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"boards", "users", "posts", "votes"} {
		if !strings.Contains(string(dumped.State), `"`+key+`":[]`) {
			t.Fatalf("empty %s dumped as %s", key, dumped.State)
		}
	}

	db = openTestDB(t, loadScenario(t, "acme-feedback.v1.json"))
	dumped, err = codec.Dump(context.Background(), db, scenario.Metadata{ID: "canny.acme-feedback.v1", Resource: "canny", ResourceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Boards) != 1 || len(state.Users) != 4 || len(state.Posts) != 2 || len(state.Votes) != 1 {
		t.Fatalf("record counts boards=%d users=%d posts=%d votes=%d", len(state.Boards), len(state.Users), len(state.Posts), len(state.Votes))
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
	want := map[string]string{
		"CannyBoardsList":          "POST /api/v1/boards/list",
		"CannyBoardsRetrieve":      "POST /api/v1/boards/retrieve",
		"CannyPostsList":           "POST /api/v1/posts/list",
		"CannyPostsRetrieve":       "POST /api/v1/posts/retrieve",
		"CannyPostsCreate":         "POST /api/v1/posts/create",
		"CannyPostsChangeStatus":   "POST /api/v1/posts/change_status",
		"CannyUsersRetrieve":       "POST /api/v1/users/retrieve",
		"CannyUsersCreateOrUpdate": "POST /api/v1/users/create_or_update",
		"CannyVotesCreate":         "POST /api/v1/votes/create",
		"CannyVotesRetrieve":       "POST /api/v1/votes/retrieve",
	}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for id, path := range want {
		if got, ok := seen[id]; !ok || got != path {
			t.Fatalf("operationId %s = %q, want %s", id, got, path)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "canny" || descriptor.DisplayName != "Canny" || descriptor.Version != "v1" {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "canny.io" || descriptor.HostPrefixes["canny.io"] != "/api/v1/" {
		t.Fatalf("descriptor hosts = %+v prefixes = %+v", descriptor.ProviderHosts, descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-feedback.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodPost, "/api/v1/posts/list", `{}`, "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), `"error":"invalid api key"`) {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	apiKeyOnly := request(t, handler, http.MethodPost, "/api/v1/posts/list", `{"apiKey":"`+testToken+`"}`, "")
	if apiKeyOnly.Code != http.StatusUnauthorized {
		t.Fatalf("body apiKey authenticated without bearer = %d %s", apiKeyOnly.Code, apiKeyOnly.Body.String())
	}

	listed := request(t, handler, http.MethodPost, "/api/v1/posts/list", `{"apiKey":"ignored","boardID":"board-acme-app"}`, testToken)
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	var page struct {
		HasMore bool `json:"hasMore"`
		Posts   []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Details string `json:"details"`
			Status  string `json:"status"`
			Score   int    `json:"score"`
			Author  struct {
				Email string `json:"email"`
				Name  string `json:"name"`
			} `json:"author"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.HasMore || len(page.Posts) != 2 {
		t.Fatalf("list page = %+v", page)
	}
	if page.Posts[0].Title != "Refund duplicate charge" || page.Posts[0].Status != "in progress" || page.Posts[0].Score != 1 ||
		!strings.Contains(page.Posts[0].Details, "INV-4812") || page.Posts[0].Author.Email != "dana@northwind.example" {
		t.Fatalf("duplicate-charge post = %+v", page.Posts[0])
	}
	if page.Posts[1].Title != "CSV export downloads an empty file" || page.Posts[1].Author.Name != "Priya Nair" || page.Posts[1].Score != 0 {
		t.Fatalf("csv post = %+v", page.Posts[1])
	}

	got := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"id":"post-duplicate-charge"}`, testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "INV-4812") || !strings.Contains(got.Body.String(), `"status":"in progress"`) {
		t.Fatalf("retrieve = %d %s", got.Code, got.Body.String())
	}
	byName := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"boardID":"board-acme-app","urlName":"csv-export-downloads-an-empty-file"}`, testToken)
	if byName.Code != http.StatusOK || !strings.Contains(byName.Body.String(), `"id":"post-csv-export"`) {
		t.Fatalf("retrieve by urlName = %d %s", byName.Code, byName.Body.String())
	}

	vote := request(t, handler, http.MethodPost, "/api/v1/votes/retrieve", `{"id":"vote-jules-inv-4812"}`, testToken)
	if vote.Code != http.StatusOK || !strings.Contains(vote.Body.String(), "jules@northwind.example") || !strings.Contains(vote.Body.String(), "Refund duplicate charge") {
		t.Fatalf("vote = %d %s", vote.Code, vote.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/api/v1/posts/create", `{"authorID":"user-priya","boardID":"board-acme-app","title":"Show invoice numbers on export","details":"Include INV-4812."}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":"canny.post-0001"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodPost, "/api/v1/posts/list", `{"boardID":"board-acme-app","limit":10}`, testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"id":"canny.post-0001"`) || !strings.Contains(again.Body.String(), "Show invoice numbers on export") {
		t.Fatalf("list after create = %d %s", again.Code, again.Body.String())
	}
	var after struct {
		Posts []struct {
			ID string `json:"id"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Posts) != 3 {
		t.Fatalf("posts after create = %d %s", len(after.Posts), again.Body.String())
	}

	voted := request(t, handler, http.MethodPost, "/api/v1/votes/create", `{"postID":"post-csv-export","voterID":"user-jules"}`, testToken)
	if voted.Code != http.StatusOK || strings.TrimSpace(voted.Body.String()) != `"success"` {
		t.Fatalf("vote create = %d %s", voted.Code, voted.Body.String())
	}
	scored := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"id":"post-csv-export"}`, testToken)
	if !strings.Contains(scored.Body.String(), `"score":1`) {
		t.Fatalf("score after vote = %s", scored.Body.String())
	}
	duplicate := request(t, handler, http.MethodPost, "/api/v1/votes/create", `{"postID":"post-duplicate-charge","voterID":"user-jules"}`, testToken)
	if duplicate.Code != http.StatusOK || strings.TrimSpace(duplicate.Body.String()) != `"success"` {
		t.Fatalf("duplicate vote = %d %s", duplicate.Code, duplicate.Body.String())
	}
	unchanged := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"id":"post-duplicate-charge"}`, testToken)
	if !strings.Contains(unchanged.Body.String(), `"score":1`) {
		t.Fatalf("duplicate vote changed score = %s", unchanged.Body.String())
	}

	status := request(t, handler, http.MethodPost, "/api/v1/posts/change_status", `{"postID":"canny.post-0001","changerID":"user-val","status":"planned","shouldNotifyVoters":false}`, testToken)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"planned"`) {
		t.Fatalf("change status = %d %s", status.Code, status.Body.String())
	}
	confirmed := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"id":"canny.post-0001"}`, testToken)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"planned"`) {
		t.Fatalf("status after change = %d %s", confirmed.Code, confirmed.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-feedback.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodPost, "/api/v1/posts/retrieve", `{"id":"missing"}`, testToken)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), `"error":"invalid post id"`) {
		t.Fatalf("missing post = %d %s", missing.Code, missing.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/api/v1/posts/create", `{}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	limit := request(t, handler, http.MethodPost, "/api/v1/posts/list", `{"limit":0}`, testToken)
	if limit.Code != http.StatusBadRequest {
		t.Fatalf("limit = %d %s", limit.Code, limit.Body.String())
	}
	changer := request(t, handler, http.MethodPost, "/api/v1/posts/change_status", `{"postID":"post-csv-export","changerID":"user-priya","status":"closed"}`, testToken)
	if changer.Code != http.StatusBadRequest || !strings.Contains(changer.Body.String(), "invalid changer id") {
		t.Fatalf("non-admin changer = %d %s", changer.Code, changer.Body.String())
	}
}

func TestTwoCannyInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-feedback.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	created := request(t, first, http.MethodPost, "/api/v1/posts/create", `{"authorID":"user-dana","boardID":"board-acme-app","title":"Only on the first board"}`, testToken)
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodPost, "/api/v1/posts/list", `{"boardID":"board-acme-app"}`, testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Only on the first board") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
	var page struct {
		Posts []json.RawMessage `json:"posts"`
	}
	if err := json.Unmarshal(untouched.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) != 2 {
		t.Fatalf("second list length = %d", len(page.Posts))
	}
}
