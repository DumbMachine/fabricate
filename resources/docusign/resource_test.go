package docusign

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
	"github.com/dumbmachine/fabricate/resources/docusign/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_docusign_test_token"

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
	if kind == "docusign.envelope" {
		return fmt.Sprintf("env-%04d", ids.counts[kind]), nil
	}
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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "docusign.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-envelopes.v1.json")
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
	if len(state.Envelopes) != 3 {
		t.Fatalf("Acme scenario contains %d envelopes, want 3", len(state.Envelopes))
	}
	if len(state.Users) != 1 {
		t.Fatalf("Acme scenario contains %d users, want 1", len(state.Users))
	}
	signers, documents := 0, 0
	for _, envelope := range state.Envelopes {
		signers += len(envelope.Signers)
		documents += len(envelope.Documents)
	}
	if signers != 4 || documents != 3 {
		t.Fatalf("signers=%d documents=%d, want 4 and 3", signers, documents)
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
	if !strings.Contains(string(after), `"envelopes":[]`) || !strings.Contains(string(after), `"users":[]`) {
		t.Fatalf("empty slices were not dumped as []: %s", after)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	doc := loadScenario(t, "acme-envelopes.v1.json")
	codec := scenarioCodec{}
	var state map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	state["notAField"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail validation")
	}

	doc = loadScenario(t, "acme-envelopes.v1.json")
	state = map[string]any{}
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	envelopes := state["envelopes"].([]any)
	envelopes[0].(map[string]any)["notAField"] = "x"
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown envelope field to fail validation")
	}

	raw, err = os.ReadFile(filepath.Join("scenarios", "acme-envelopes.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["$extra"] = true
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.Parse(body); err == nil {
		t.Fatal("expected unknown scenario envelope field to fail")
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
		"Accounts_GetAccount",
		"Users_GetUsers",
		"User_GetUser",
		"Envelopes_GetEnvelopes",
		"Envelopes_PostEnvelopes",
		"Envelopes_GetEnvelope",
		"Envelopes_PutEnvelope",
		"Recipients_GetRecipients",
		"Recipients_PutRecipients",
		"Documents_GetDocuments",
		"Documents_GetDocument",
		"Views_PostEnvelopeRecipientView",
	}
	if len(seen) != len(want) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(want), seen)
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing operationId %s", id)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "docusign" || descriptor.DisplayName != "DocuSign" || descriptor.Version != "v2.1" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if descriptor.ProviderHosts[0] != "demo.docusign.net" || descriptor.HostPrefixes["demo.docusign.net"] != "/restapi/" {
		t.Fatalf("hosts = %v prefixes = %v", descriptor.ProviderHosts, descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if formatAPITime(time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)) != "2026-08-26T12:00:00.0000000Z" {
		t.Fatal("clock format drifted from DocuSign timestamps")
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-envelopes.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	listPath := "/restapi/v2.1/accounts/acct-acme/envelopes"

	unauthorized := request(t, handler, http.MethodGet, listPath, "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "USER_AUTHENTICATION_FAILED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, listPath, "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	list := request(t, handler, http.MethodGet, listPath, "", testToken)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"totalSetSize":"3"`) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	for _, id := range []string{"env-fernworks-msa", "env-tinyshop-refund", "env-offer-jordan"} {
		if !strings.Contains(list.Body.String(), id) {
			t.Fatalf("list missing %s: %s", id, list.Body.String())
		}
	}
	waiting := request(t, handler, http.MethodGet, listPath+"?status=sent", "", testToken)
	if waiting.Code != http.StatusOK || !strings.Contains(waiting.Body.String(), "env-tinyshop-refund") || strings.Contains(waiting.Body.String(), "env-fernworks-msa") {
		t.Fatalf("sent filter = %d %s", waiting.Code, waiting.Body.String())
	}

	fernworks := request(t, handler, http.MethodGet, listPath+"/env-fernworks-msa", "", testToken)
	if fernworks.Code != http.StatusOK || !strings.Contains(fernworks.Body.String(), `"status":"completed"`) ||
		!strings.Contains(fernworks.Body.String(), "priya@fernworks.example") || !strings.Contains(fernworks.Body.String(), "iris@acme.example") ||
		!strings.Contains(fernworks.Body.String(), "Fernworks-MSA.pdf") {
		t.Fatalf("fernworks = %d %s", fernworks.Code, fernworks.Body.String())
	}

	tinyshop := request(t, handler, http.MethodGet, listPath+"/env-tinyshop-refund", "", testToken)
	if tinyshop.Code != http.StatusOK || !strings.Contains(tinyshop.Body.String(), `"status":"sent"`) || !strings.Contains(tinyshop.Body.String(), "marco@tinyshop.example") || !strings.Contains(tinyshop.Body.String(), "TinyShop-cancellation.pdf") {
		t.Fatalf("tinyshop = %d %s", tinyshop.Code, tinyshop.Body.String())
	}

	offer := request(t, handler, http.MethodGet, listPath+"/env-offer-jordan", "", testToken)
	if offer.Code != http.StatusOK || !strings.Contains(offer.Body.String(), `"status":"delivered"`) || !strings.Contains(offer.Body.String(), "jordan.hale@acme.example") || !strings.Contains(offer.Body.String(), "offer-letter-jordan-hale.pdf") {
		t.Fatalf("offer = %d %s", offer.Code, offer.Body.String())
	}

	document := request(t, handler, http.MethodGet, listPath+"/env-fernworks-msa/documents/1", "", testToken)
	if document.Code != http.StatusOK || document.Body.String() != "Fernworks-MSA.pdf\n" {
		t.Fatalf("document = %d %q", document.Code, document.Body.String())
	}

	created := request(t, handler, http.MethodPost, listPath, `{
		"emailSubject":"Please sign the Northwind expansion addendum",
		"emailBlurb":"Dana Whitfield signs for Northwind.",
		"status":"sent",
		"documents":[{"name":"Northwind-addendum.pdf","fileExtension":"pdf","documentBase64":"Tm9ydGh3aW5kLWFkZGVuZHVtLnBkZgo="}],
		"recipients":{"signers":[{"name":"Dana Whitfield","email":"dana@northwind.example","routingOrder":"1"}]}
	}`, testToken)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"envelopeId":"env-0001"`) || !strings.Contains(created.Body.String(), `"statusDateTime":"2026-08-26T12:00:00.0000000Z"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}

	again := request(t, handler, http.MethodGet, listPath+"/env-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "dana@northwind.example") || !strings.Contains(again.Body.String(), "Northwind-addendum.pdf") || !strings.Contains(again.Body.String(), `"status":"sent"`) {
		t.Fatalf("created envelope = %d %s", again.Code, again.Body.String())
	}
	persisted := request(t, handler, http.MethodGet, listPath+"/env-0001/documents/1", "", testToken)
	if persisted.Code != http.StatusOK || persisted.Body.String() != "Northwind-addendum.pdf\n" {
		t.Fatalf("created document = %d %q", persisted.Code, persisted.Body.String())
	}
	after := request(t, handler, http.MethodGet, listPath, "", testToken)
	if !strings.Contains(after.Body.String(), `"totalSetSize":"4"`) || !strings.Contains(after.Body.String(), "env-0001") {
		t.Fatalf("list after create = %s", after.Body.String())
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-envelopes.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/restapi/v2.1/accounts/acct-acme/envelopes/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "ENVELOPE_DOES_NOT_EXIST") {
		t.Fatalf("missing envelope = %d %s", missing.Code, missing.Body.String())
	}
	other := request(t, handler, http.MethodGet, "/restapi/v2.1/accounts/other/envelopes", "", testToken)
	if other.Code != http.StatusNotFound || !strings.Contains(other.Body.String(), "ACCOUNT_DOES_NOT_EXIST") {
		t.Fatalf("other account = %d %s", other.Code, other.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/restapi/v2.1/accounts/acct-acme/envelopes", `{"status":"sent","emailSubject":"Missing parties"}`, testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_REQUEST_BODY") {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	badCount := request(t, handler, http.MethodGet, "/restapi/v2.1/accounts/acct-acme/envelopes?count=0", "", testToken)
	if badCount.Code != http.StatusBadRequest {
		t.Fatalf("count = %d %s", badCount.Code, badCount.Body.String())
	}
}

func TestTwoDocusignInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-envelopes.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/restapi/v2.1/accounts/acct-acme/envelopes", `{
		"emailSubject":"Isolated envelope","status":"sent",
		"documents":[{"name":"note.pdf","documentBase64":"bm90ZQo="}],
		"recipients":{"signers":[{"name":"Iris Berg","email":"iris@acme.example"}]}
	}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/restapi/v2.1/accounts/acct-acme/envelopes", "", testToken)
	if untouched.Code != http.StatusOK || strings.Contains(untouched.Body.String(), "Isolated envelope") || !strings.Contains(untouched.Body.String(), `"totalSetSize":"3"`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
