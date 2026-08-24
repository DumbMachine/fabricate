package gmail

import (
	"context"
	"database/sql"
	"encoding/base64"
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
	"github.com/dumbmachine/fabricate/resources/gmail/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_gmail_test_token"

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
	n  int
}

func (ids *testIDs) Next(_ context.Context, _ string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.n++
	return fmt.Sprintf("msg-sent-%04d", ids.n), nil
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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "gmail.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
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
		DB: db, Clock: httpresource.FixedClock{Time: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)},
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
	doc := loadScenario(t, "acme-corp.v1.json")
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
		t.Fatalf("load/dump changed the baseline:\nbefore=%s\nafter=%s", before, after)
	}
	var state fixtureState
	if err := json.Unmarshal(dumped.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) != 12 {
		t.Fatalf("Acme scenario contains %d messages, want 12", len(state.Messages))
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
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-corp.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/gmail/v1/users/me/profile", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	profile := request(t, handler, http.MethodGet, "/gmail/v1/users/me/profile", "", testToken)
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), `"messagesTotal":12`) {
		t.Fatalf("profile = %d %s", profile.Code, profile.Body.String())
	}

	unread := request(t, handler, http.MethodGet, "/gmail/v1/users/me/messages?q=is:unread", "", testToken)
	if unread.Code != http.StatusOK || !strings.Contains(unread.Body.String(), `"resultSizeEstimate":6`) {
		t.Fatalf("unread = %d %s", unread.Code, unread.Body.String())
	}

	modified := request(t, handler, http.MethodPost, "/gmail/v1/users/me/messages/msg-0002/modify", `{"removeLabelIds":["UNREAD"],"addLabelIds":["STARRED"]}`, testToken)
	if modified.Code != http.StatusOK || strings.Contains(modified.Body.String(), `"UNREAD"`) || !strings.Contains(modified.Body.String(), `"STARRED"`) {
		t.Fatalf("modify = %d %s", modified.Code, modified.Body.String())
	}

	unread = request(t, handler, http.MethodGet, "/gmail/v1/users/me/messages?q=is:unread", "", testToken)
	if !strings.Contains(unread.Body.String(), `"resultSizeEstimate":5`) {
		t.Fatalf("unread after modify = %s", unread.Body.String())
	}

	raw := base64.RawURLEncoding.EncodeToString([]byte("To: dana@northwind.example\r\nSubject: Re: Charged twice for August invoice INV-4812\r\n\r\nThe duplicate refund has been issued as rf_4812."))
	sentBody, _ := json.Marshal(map[string]string{"raw": raw, "threadId": "thr-double-charge"})
	sent := request(t, handler, http.MethodPost, "/gmail/v1/users/me/messages/send", string(sentBody), testToken)
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"id":"msg-sent-0001"`) {
		t.Fatalf("send = %d %s", sent.Code, sent.Body.String())
	}

	thread := request(t, handler, http.MethodGet, "/gmail/v1/users/me/threads/thr-double-charge", "", testToken)
	var got struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(thread.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 || !strings.Contains(thread.Body.String(), "refund has been issued") {
		t.Fatalf("thread after send = %d %s", thread.Code, thread.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-corp.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	invalid := request(t, handler, http.MethodGet, "/gmail/v1/users/me/messages?maxResults=0", "", testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_ARGUMENT") {
		t.Fatalf("invalid request = %d %s", invalid.Code, invalid.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/gmail/v1/users/me/messages/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("missing message = %d %s", missing.Code, missing.Body.String())
	}
}

func TestTwoGmailInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-corp.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/gmail/v1/users/me/messages/msg-0001/trash", "", testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("trash = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/gmail/v1/users/me/messages/msg-0001", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"INBOX"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
