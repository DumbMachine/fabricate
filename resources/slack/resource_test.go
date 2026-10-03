package slack

import (
	"bytes"
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
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/slack/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_slack_test_token"

type testSecrets map[string]string

func (s testSecrets) Get(_ context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", fmt.Errorf("missing secret %s", key)
	}
	return value, nil
}

type testIDs struct{}

func (testIDs) Next(_ context.Context, kind string) (string, error) {
	return "slack-" + kind, nil
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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "slack.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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

func newTestHandler(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	resource := NewResource()
	server, err := resource.NewServer(context.Background(), httpresource.ServerDependencies{
		DB: db, Clock: httpresource.FixedClock{Time: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)},
		IDs: testIDs{}, Secrets: testSecrets{"token": testToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	return server.Handler()
}

func request(t *testing.T, handler http.Handler, method, path, body, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func compactJSON(t *testing.T, raw string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(raw)); err != nil {
		t.Fatalf("compact json: %v\n%s", err, raw)
	}
	return buf.String()
}

func TestAcmeScenarioValidatesAndRoundTrips(t *testing.T) {
	doc := loadScenario(t, "acme-workspace.v1.json")
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
	if len(state.Users) != 7 || len(state.Channels) != 5 || len(state.Messages) != 4 {
		t.Fatalf("counts users=%d channels=%d messages=%d", len(state.Users), len(state.Channels), len(state.Messages))
	}
	byChannel := map[string][]fixtureMessage{}
	for _, message := range state.Messages {
		byChannel[message.Channel] = append(byChannel[message.Channel], message)
	}
	fulfillment := byChannel["C-fulfillment"]
	if len(fulfillment) != 2 || fulfillment[0].User != "U-sam" || !strings.Contains(fulfillment[0].Text, "#10483") {
		t.Fatalf("fulfillment sam message = %#v", fulfillment)
	}
	if fulfillment[1].User != "U-ravi" || !strings.Contains(fulfillment[1].Text, "#10482") || !strings.Contains(fulfillment[1].Text, "SR10482AWB") {
		t.Fatalf("fulfillment ravi message = %#v", fulfillment[1])
	}
	finance := byChannel["C-finance"]
	if len(finance) != 1 || finance[0].User != "U-aisha" || !strings.Contains(finance[0].Text, "rfnd_Acme10484") || !strings.Contains(finance[0].Text, "INV-4812") || !strings.Contains(finance[0].Text, "different") {
		t.Fatalf("finance message = %#v", finance)
	}
	incidents := byChannel["C-incidents"]
	if len(incidents) != 1 || !strings.Contains(incidents[0].Text, "contoso-eu") {
		t.Fatalf("incidents message = %#v", incidents)
	}
	if state.Users[0].Email != "aisha@acme.example" || !state.Users[6].IsOwner || state.Users[6].Email != "val@acme.example" {
		t.Fatalf("users = %#v", state.Users)
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
		t.Fatalf("minimal round trip changed:\nbefore=%s\nafter=%s", before, after)
	}
	if !bytes.Contains(after, []byte(`"channels":[]`)) || !bytes.Contains(after, []byte(`"messages":[]`)) {
		t.Fatalf("empty slices were not dumped as []: %s", after)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["extra"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	doc = loadScenario(t, "minimal.v1.json")
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	users := state["users"].([]any)
	user := users[0].(map[string]any)
	user["timezone"] = "Asia/Kolkata"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown user field to fail")
	}
}

func TestCompiledOpenAPIContractIsValid(t *testing.T) {
	resource := NewResource()
	spec, err := generated.GetSpec()
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
		"auth.test":             "GET /api/auth.test",
		"users.list":            "GET /api/users.list",
		"users.info":            "GET /api/users.info",
		"conversations.list":    "GET /api/conversations.list",
		"conversations.info":    "GET /api/conversations.info",
		"conversations.members": "GET /api/conversations.members",
		"conversations.history": "GET /api/conversations.history",
		"conversations.replies": "GET /api/conversations.replies",
		"chat.postMessage":      "POST /api/chat.postMessage",
		"chat.update":           "POST /api/chat.update",
		"chat.delete":           "POST /api/chat.delete",
	}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for id, route := range want {
		if seen[id] != route {
			t.Fatalf("operation %s = %q, want %q", id, seen[id], route)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "slack" || descriptor.DisplayName != "Slack" || descriptor.Version != "v2" {
		t.Fatalf("descriptor identity = %#v", descriptor)
	}
	if descriptor.HostPrefixes["slack.com"] != "/api/" || len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "slack.com" {
		t.Fatalf("descriptor routing = hosts %v prefixes %v", descriptor.ProviderHosts, descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if ids := scenario.IDs(docs); len(ids) != 2 || ids[0] != "slack.acme-workspace.v1" || ids[1] != "slack.minimal.v1" {
		t.Fatalf("scenario ids = %v", ids)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-workspace.v1.json"))
	handler := newTestHandler(t, db)

	unauthorized := request(t, handler, http.MethodGet, "/api/auth.test", "", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), `"not_authed"`) {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	rejected := request(t, handler, http.MethodGet, "/api/auth.test", "", "wrong-token", "")
	if rejected.Code != http.StatusUnauthorized || !strings.Contains(rejected.Body.String(), `"invalid_auth"`) {
		t.Fatalf("rejected = %d %s", rejected.Code, rejected.Body.String())
	}

	authTest := request(t, handler, http.MethodGet, "/api/auth.test", "", testToken, "")
	if authTest.Code != http.StatusOK || !strings.Contains(authTest.Body.String(), `"user":"val"`) || !strings.Contains(authTest.Body.String(), `"user_id":"U-val"`) {
		t.Fatalf("auth.test = %d %s", authTest.Code, authTest.Body.String())
	}

	users := request(t, handler, http.MethodGet, "/api/users.list", "", testToken, "")
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"aisha@acme.example"`) || !strings.Contains(users.Body.String(), `"+91-80-4123-0102"`) || !strings.Contains(users.Body.String(), `"is_owner":true`) {
		t.Fatalf("users.list = %d %s", users.Code, users.Body.String())
	}

	channels := request(t, handler, http.MethodGet, "/api/conversations.list", "", testToken, "")
	for _, id := range []string{"C-fulfillment", "C-finance", "C-support", "C-incidents", "C-hiring"} {
		if channels.Code != http.StatusOK || !strings.Contains(channels.Body.String(), id) {
			t.Fatalf("conversations.list missing %s: %d %s", id, channels.Code, channels.Body.String())
		}
	}

	history := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-fulfillment", "", testToken, "")
	const wantHistory = `{
	  "has_more": false,
	  "messages": [
	    {"text": "Acme Goods #10482 is in transit with Delhivery. AWB SR10482AWB.", "ts": "1787652300.000000", "type": "message", "user": "U-ravi"},
	    {"text": "NDR on Acme Goods #10483. Customer was not available.", "ts": "1787592600.000000", "type": "message", "user": "U-sam"}
	  ],
	  "ok": true,
	  "pin_count": 0
	}`
	if history.Code != http.StatusOK || compactJSON(t, history.Body.String()) != compactJSON(t, wantHistory) {
		t.Fatalf("history = %d %s", history.Code, history.Body.String())
	}

	blocked := request(t, handler, http.MethodPost, "/api/chat.update", `{"channel":"C-fulfillment","ts":"1787652300.000000","text":"rewritten"}`, testToken, "")
	if blocked.Code != http.StatusOK || !strings.Contains(blocked.Body.String(), `"cant_update_message"`) {
		t.Fatalf("update other author = %d %s", blocked.Code, blocked.Body.String())
	}

	posted := request(t, handler, http.MethodPost, "/api/chat.postMessage", `{"channel":"C-fulfillment","text":"Dock check for #10482."}`, testToken, "")
	if posted.Code != http.StatusOK || !strings.Contains(posted.Body.String(), `"ts":"1787745600.000000"`) || !strings.Contains(posted.Body.String(), `"ok":true`) {
		t.Fatalf("post = %d %s", posted.Code, posted.Body.String())
	}
	after := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-fulfillment", "", testToken, "")
	if !strings.Contains(after.Body.String(), "Dock check for #10482.") || !strings.Contains(after.Body.String(), `"user":"U-val"`) {
		t.Fatalf("history after post = %s", after.Body.String())
	}

	reply := request(t, handler, http.MethodPost, "/api/chat.postMessage", `{"channel":"C-fulfillment","text":"Reply on #10482.","thread_ts":"1787745600.000000"}`, testToken, "")
	if reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), `"thread_ts":"1787745600.000000"`) {
		t.Fatalf("reply = %d %s", reply.Code, reply.Body.String())
	}
	parentHistory := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-fulfillment", "", testToken, "")
	if strings.Contains(parentHistory.Body.String(), "Reply on #10482.") || !strings.Contains(parentHistory.Body.String(), `"reply_count":1`) {
		t.Fatalf("history should keep the reply on the thread: %s", parentHistory.Body.String())
	}
	replies := request(t, handler, http.MethodGet, "/api/conversations.replies?channel=C-fulfillment&ts=1787745600.000000", "", testToken, "")
	if replies.Code != http.StatusOK || !strings.Contains(replies.Body.String(), "Reply on #10482.") || !strings.Contains(replies.Body.String(), "Dock check for #10482.") {
		t.Fatalf("replies = %d %s", replies.Code, replies.Body.String())
	}

	updated := request(t, handler, http.MethodPost, "/api/chat.update", `{"channel":"C-fulfillment","ts":"1787745600.000000","text":"Dock check for #10482 done."}`, testToken, "")
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "done.") {
		t.Fatalf("update = %d %s", updated.Code, updated.Body.String())
	}
	confirmed := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-fulfillment", "", testToken, "")
	if !strings.Contains(confirmed.Body.String(), "Dock check for #10482 done.") {
		t.Fatalf("history after update = %s", confirmed.Body.String())
	}

	deleted := request(t, handler, http.MethodPost, "/api/chat.delete", `{"channel":"C-fulfillment","ts":"1787745600.000000"}`, testToken, "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"ok":true`) {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	gone := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-fulfillment", "", testToken, "")
	if strings.Contains(gone.Body.String(), "Dock check") {
		t.Fatalf("deleted message still in history: %s", gone.Body.String())
	}
}

func TestTokenFieldAccepted(t *testing.T) {
	handler := newTestHandler(t, openTestDB(t, loadScenario(t, "acme-workspace.v1.json")))

	query := request(t, handler, http.MethodGet, "/api/auth.test?token="+url.QueryEscape(testToken), "", "", "")
	if query.Code != http.StatusOK || !strings.Contains(query.Body.String(), `"user_id":"U-val"`) {
		t.Fatalf("query token = %d %s", query.Code, query.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat.postMessage", strings.NewReader("token="+url.QueryEscape(testToken)+"&channel=C-support&text=Form+ping"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"channel":"C-support"`) {
		t.Fatalf("form token = %d %s", recorder.Code, recorder.Body.String())
	}
	history := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-support", "", testToken, "")
	if !strings.Contains(history.Body.String(), "Form ping") {
		t.Fatalf("form post was not stored: %s", history.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-workspace.v1.json"))
	handler := newTestHandler(t, db)

	invalid := request(t, handler, http.MethodGet, "/api/users.list?limit=0", "", testToken, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"invalid_arguments"`) {
		t.Fatalf("invalid limit = %d %s", invalid.Code, invalid.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/api/conversations.info?channel=C-missing", "", testToken, "")
	if missing.Code != http.StatusOK || !strings.Contains(missing.Body.String(), `"channel_not_found"`) {
		t.Fatalf("missing channel = %d %s", missing.Code, missing.Body.String())
	}
	unknownUser := request(t, handler, http.MethodGet, "/api/users.info?user=U-missing", "", testToken, "")
	if unknownUser.Code != http.StatusOK || !strings.Contains(unknownUser.Body.String(), `"user_not_found"`) {
		t.Fatalf("missing user = %d %s", unknownUser.Code, unknownUser.Body.String())
	}
	cursor := request(t, handler, http.MethodGet, "/api/conversations.history?channel=C-hiring&cursor=nope", "", testToken, "")
	if cursor.Code != http.StatusOK || !strings.Contains(cursor.Body.String(), `"invalid_cursor"`) {
		t.Fatalf("cursor = %d %s", cursor.Code, cursor.Body.String())
	}
	if _, err := db.Exec(`DELETE FROM channel_members WHERE channel_id='C-hiring' AND user_id='U-val'`); err != nil {
		t.Fatal(err)
	}
	outside := request(t, handler, http.MethodPost, "/api/chat.postMessage", `{"channel":"C-hiring","text":"hello"}`, testToken, "")
	if outside.Code != http.StatusOK || !strings.Contains(outside.Body.String(), `"not_in_channel"`) {
		t.Fatalf("not in channel = %d %s", outside.Code, outside.Body.String())
	}
	if _, err := db.Exec(`UPDATE channels SET is_archived=1 WHERE id='C-support'`); err != nil {
		t.Fatal(err)
	}
	archived := request(t, handler, http.MethodPost, "/api/chat.postMessage", `{"channel":"C-support","text":"hello"}`, testToken, "")
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"is_archived"`) {
		t.Fatalf("archived = %d %s", archived.Code, archived.Body.String())
	}
}

func TestTwoSlackInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-workspace.v1.json")
	first := newTestHandler(t, openTestDB(t, doc))
	second := newTestHandler(t, openTestDB(t, doc))
	response := request(t, first, http.MethodPost, "/api/chat.postMessage", `{"channel":"C-finance","text":"Only the first workspace."}`, testToken, "")
	if response.Code != http.StatusOK {
		t.Fatalf("post = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/api/conversations.history?channel=C-finance", "", testToken, "")
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Only the first workspace") {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
