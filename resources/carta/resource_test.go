package carta

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
	"github.com/dumbmachine/fabricate/resources/carta/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_carta_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "carta.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-captable.v1.json")
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
	if state.Issuer.LegalName != "Acme" || len(state.Stakeholders) != 2 || len(state.Certificates) != 1 || len(state.OptionGrants) != 1 || len(state.FairMarketValues) != 1 || len(state.DraftOptionGrants) != 0 {
		t.Fatalf("Acme counts issuer=%s stakeholders=%d certificates=%d optionGrants=%d fairMarketValues=%d drafts=%d",
			state.Issuer.LegalName, len(state.Stakeholders), len(state.Certificates), len(state.OptionGrants), len(state.FairMarketValues), len(state.DraftOptionGrants))
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
	for _, collection := range []string{"stakeholders", "certificates", "optionGrants", "fairMarketValues", "draftOptionGrants"} {
		if !strings.Contains(string(after), `"`+collection+`":[]`) {
			t.Fatalf("minimal dump missing empty %s: %s", collection, after)
		}
	}
}

func TestUnknownScenarioFieldsFail(t *testing.T) {
	doc := loadScenario(t, "acme-captable.v1.json")
	codec := scenarioCodec{}
	raw, err := os.ReadFile(filepath.Join("scenarios", "acme-captable.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["extra"] = true
	body, _ := json.Marshal(envelope)
	if _, err := scenario.Parse(body); err == nil {
		t.Fatal("unknown envelope field was accepted")
	}

	state := decodeStateMap(t, doc.State)
	state["valuationDocumentName"] = "409a-2026.pdf"
	doc.State = mustJSON(state)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("unknown state field was accepted")
	}

	doc = loadScenario(t, "acme-captable.v1.json")
	state = decodeStateMap(t, doc.State)
	stakeholders := state["stakeholders"].([]any)
	stakeholders[0].(map[string]any)["phone"] = "+91-80-4123-0101"
	doc.State = mustJSON(state)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("unknown stakeholder field was accepted")
	}

	doc = loadScenario(t, "acme-captable.v1.json")
	state = decodeStateMap(t, doc.State)
	certificates := state["certificates"].([]any)
	certificates[0].(map[string]any)["stakeholderId"] = "stk-missing"
	doc.State = mustJSON(state)
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("certificate with an unknown stakeholder was accepted")
	}
}

func decodeStateMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
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
		t.Fatalf("compiled operation count = %d, want 10: %#v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if got, want := resource.Descriptor().DisplayName, "Carta"; got != want {
		t.Fatalf("display name = %s", got)
	}
	if hosts := resource.Descriptor().ProviderHosts; len(hosts) != 1 || hosts[0] != "api.carta.com" {
		t.Fatalf("provider hosts = %#v", hosts)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-captable.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders", "", "other-token")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	issuer := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme", "", testToken)
	if issuer.Code != http.StatusOK || !strings.Contains(issuer.Body.String(), `"legalName":"Acme"`) {
		t.Fatalf("issuer = %d %s", issuer.Code, issuer.Body.String())
	}

	stakeholders := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders", "", testToken)
	if stakeholders.Code != http.StatusOK || !strings.Contains(stakeholders.Body.String(), `"val@acme.example"`) || !strings.Contains(stakeholders.Body.String(), `"Harbor Capital"`) {
		t.Fatalf("stakeholders = %d %s", stakeholders.Code, stakeholders.Body.String())
	}

	page := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders?pageSize=1", "", testToken)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"nextPageToken":"1"`) || strings.Contains(page.Body.String(), "Harbor Capital") {
		t.Fatalf("page = %d %s", page.Code, page.Body.String())
	}

	val := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders/stk-val", "", testToken)
	if val.Code != http.StatusOK || !strings.Contains(val.Body.String(), `"email":"val@acme.example"`) || !strings.Contains(val.Body.String(), `"EMPLOYEE"`) {
		t.Fatalf("stakeholder = %d %s", val.Code, val.Body.String())
	}

	certificates := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/certificates", "", testToken)
	if certificates.Code != http.StatusOK || !strings.Contains(certificates.Body.String(), `"shareClassName":"Preferred"`) || !strings.Contains(certificates.Body.String(), `"stk-harbor"`) {
		t.Fatalf("certificates = %d %s", certificates.Code, certificates.Body.String())
	}

	grants := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/optionGrants", "", testToken)
	if grants.Code != http.StatusOK || !strings.Contains(grants.Body.String(), `"stk-val"`) || !strings.Contains(grants.Body.String(), `"ISO"`) {
		t.Fatalf("option grants = %d %s", grants.Code, grants.Body.String())
	}

	fmv := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/fairMarketValues", "", testToken)
	if fmv.Code != http.StatusOK || !strings.Contains(fmv.Body.String(), `"fmv-2026"`) || strings.Contains(fmv.Body.String(), "409a-2026.pdf") {
		t.Fatalf("fair market values = %d %s", fmv.Code, fmv.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/LATEST/draftOptionGrants", `{
		"draftOptionGrant": {
			"stockOptionType": "STOCK_OPTION_TYPE_ISO",
			"grantReason": "GRANT_REASON_REFRESH",
			"quantity": {"value": "5000"},
			"exercisePrice": {"currencyCode": {"value": "USD"}, "amount": {"value": "0.42"}},
			"stakeholder": {
				"name": "Val Ortega",
				"email": "val@acme.example",
				"type": "STAKEHOLDER_TYPE_INDIVIDUAL",
				"relationship": "STAKEHOLDER_RELATIONSHIP_EMPLOYEE"
			},
			"notes": "Refresh grant pending board consent."
		}
	}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), "Refresh grant pending board consent.") || !strings.Contains(created.Body.String(), `"id":"carta.draftOptionGrant-0001"`) {
		t.Fatalf("create draft = %d %s", created.Code, created.Body.String())
	}

	got := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/carta.draftOptionGrantSet-0001/draftOptionGrants/carta.draftOptionGrant-0001", "", testToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Refresh grant pending board consent.") || !strings.Contains(got.Body.String(), `"val@acme.example"`) {
		t.Fatalf("get draft = %d %s", got.Code, got.Body.String())
	}

	again := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/optionGrants", "", testToken)
	if strings.Contains(again.Body.String(), "carta.draftOptionGrant-0001") || !strings.Contains(again.Body.String(), `"og-val"`) {
		t.Fatalf("issued grants changed after draft create: %s", again.Body.String())
	}

	codec := scenarioCodec{}
	dumped, err := codec.Dump(context.Background(), db, scenario.Metadata{ID: "carta.acme-captable.v1", Resource: "carta", ResourceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	reloaded := openTestDB(t, dumped)
	againHandler := newTestHandler(t, reloaded, &testIDs{})
	persisted := request(t, againHandler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/carta.draftOptionGrantSet-0001/draftOptionGrants/carta.draftOptionGrant-0001", "", testToken)
	if persisted.Code != http.StatusOK || !strings.Contains(persisted.Body.String(), "Refresh grant pending board consent.") {
		t.Fatalf("reloaded draft = %d %s", persisted.Code, persisted.Body.String())
	}
	second, err := codec.Dump(context.Background(), reloaded, dumped.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := scenario.CanonicalJSON(dumped)
	secondJSON, _ := scenario.CanonicalJSON(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("dump after create was not stable:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-captable.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	missing := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders/missing", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "stakeholder not found") {
		t.Fatalf("missing stakeholder = %d %s", missing.Code, missing.Body.String())
	}
	badPage := request(t, handler, http.MethodGet, "/v1alpha1/issuers/issuer-acme/stakeholders?pageToken=nope", "", testToken)
	if badPage.Code != http.StatusBadRequest || !strings.Contains(badPage.Body.String(), "invalid pageToken") {
		t.Fatalf("bad page = %d %s", badPage.Code, badPage.Body.String())
	}
	invalid := request(t, handler, http.MethodPost, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/LATEST/draftOptionGrants", `{"draftOptionGrant":{"notes":"missing grant"}}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d %s", invalid.Code, invalid.Body.String())
	}
	unknownIssuer := request(t, handler, http.MethodGet, "/v1alpha1/issuers/other", "", testToken)
	if unknownIssuer.Code != http.StatusNotFound {
		t.Fatalf("unknown issuer = %d %s", unknownIssuer.Code, unknownIssuer.Body.String())
	}
}

func TestTwoCartaInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-captable.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	created := request(t, first, http.MethodPost, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/LATEST/draftOptionGrants", `{
		"draftOptionGrant": {
			"stockOptionType": "STOCK_OPTION_TYPE_NSO",
			"quantity": {"value": "100"},
			"stakeholder": {"name": "Val Ortega", "email": "val@acme.example"},
			"notes": "isolated draft"
		}
	}`, testToken)
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	leaked := request(t, second, http.MethodGet, "/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/carta.draftOptionGrantSet-0001/draftOptionGrants/carta.draftOptionGrant-0001", "", testToken)
	if leaked.Code != http.StatusNotFound {
		t.Fatalf("second instance leaked state = %d %s", leaked.Code, leaked.Body.String())
	}
}
