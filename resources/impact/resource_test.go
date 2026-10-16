package impact

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
	"github.com/dumbmachine/fabricate/resources/impact/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_impact_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "impact.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	for _, name := range []string{"minimal.v1.json", "acme-partners.v1.json"} {
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
			if strings.Contains(string(dumped.State), "null") {
				t.Fatalf("dump contains null: %s", dumped.State)
			}
		})
	}
}

func TestEmptySlicesDumpAsArrays(t *testing.T) {
	doc := loadScenario(t, "minimal.v1.json")
	db := openTestDB(t, doc)
	dumped, err := (scenarioCodec{}).Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"programs", "partners", "actions", "notes"} {
		if !strings.Contains(string(dumped.State), `"`+key+`":[]`) {
			t.Fatalf("empty %s was not dumped as []: %s", key, dumped.State)
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
	state["extra"] = true
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	doc.State = raw
	if err := codec.Validate(context.Background(), doc); err == nil {
		t.Fatal("expected unknown state field to fail")
	}

	acme := loadScenario(t, "acme-partners.v1.json")
	var populated map[string]any
	if err := json.Unmarshal(acme.State, &populated); err != nil {
		t.Fatal(err)
	}
	partners := populated["partners"].([]any)
	partner := partners[0].(map[string]any)
	partner["extra"] = "nope"
	raw, err = json.Marshal(populated)
	if err != nil {
		t.Fatal(err)
	}
	acme.State = raw
	if err := codec.Validate(context.Background(), acme); err == nil {
		t.Fatal("expected unknown partner field to fail")
	}
}

func TestAcmeScenarioRecords(t *testing.T) {
	doc := loadScenario(t, "acme-partners.v1.json")
	if err := (scenarioCodec{}).Validate(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(doc.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Programs) != 1 || state.Programs[0].Name != "Acme Goods affiliates" {
		t.Fatalf("programs = %+v", state.Programs)
	}
	if len(state.Partners) != 1 || state.Partners[0].ID != "partner_northwind" || state.Partners[0].Name != "Northwind Creators" {
		t.Fatalf("partners = %+v", state.Partners)
	}
	if len(state.Actions) != 1 {
		t.Fatalf("actions = %d", len(state.Actions))
	}
	action := state.Actions[0]
	if action.Oid != "10482" || action.State != "PENDING" || action.PayoutPaise != 15000 || action.AmountPaise != 309700 || action.Currency != "INR" {
		t.Fatalf("action = %+v", action)
	}
	if len(action.Items) != 2 || action.Items[0].SKU != "AG-TEE-02" || action.Items[1].SKU != "AG-MUG-01" {
		t.Fatalf("items = %+v", action.Items)
	}
	if len(state.Notes) != 0 {
		t.Fatalf("notes = %d", len(state.Notes))
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
		t.Fatalf("compiled operation count = %d, want 11: %v", len(seen), seen)
	}
	contract := resource.Contract()
	if got, want := resource.Descriptor().OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	if resource.Descriptor().DisplayName != "impact.com" {
		t.Fatalf("display name = %s", resource.Descriptor().DisplayName)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-partners.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/MediaPartners", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/MediaPartners", "", "other")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	missingAccount := request(t, handler, http.MethodGet, "/Advertisers/other/MediaPartners", "", testToken)
	if missingAccount.Code != http.StatusNotFound || !strings.Contains(missingAccount.Body.String(), "account not found") {
		t.Fatalf("unknown account = %d %s", missingAccount.Code, missingAccount.Body.String())
	}

	partners := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/MediaPartners", "", testToken)
	var partnerList generated.ListPartnersResponse
	if err := json.Unmarshal(partners.Body.Bytes(), &partnerList); err != nil {
		t.Fatal(err)
	}
	if partners.Code != http.StatusOK || len(partnerList.Partners) != 1 || partnerList.Partners[0].Id != "partner_northwind" || partnerList.Partners[0].Name != "Northwind Creators" {
		t.Fatalf("partners = %d %s", partners.Code, partners.Body.String())
	}

	programs := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Campaigns", "", testToken)
	if programs.Code != http.StatusOK || !strings.Contains(programs.Body.String(), "Acme Goods affiliates") {
		t.Fatalf("programs = %d %s", programs.Code, programs.Body.String())
	}

	missingCampaign := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions", "", testToken)
	if missingCampaign.Code != http.StatusBadRequest {
		t.Fatalf("actions without campaign = %d %s", missingCampaign.Code, missingCampaign.Body.String())
	}

	listed := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=10482", "", testToken)
	var actions generated.ListActionsResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &actions); err != nil {
		t.Fatal(err)
	}
	if listed.Code != http.StatusOK || len(actions.Actions) != 1 {
		t.Fatalf("list actions = %d %s", listed.Code, listed.Body.String())
	}
	action := actions.Actions[0]
	if action.Oid != "10482" || action.State != "PENDING" || action.Payout != 150 || action.Amount != 3097 || action.Currency != "INR" || action.MediaPartnerId != "partner_northwind" || action.CampaignName != "Acme Goods affiliates" {
		t.Fatalf("action = %+v", action)
	}
	if actions.Total != "1" || actions.Uri != "/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=10482&Page=1&PageSize=100" {
		t.Fatalf("page = %+v", actions)
	}

	approved := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions?CampaignId=1001&State=APPROVED", "", testToken)
	var none generated.ListActionsResponse
	if err := json.Unmarshal(approved.Body.Bytes(), &none); err != nil {
		t.Fatal(err)
	}
	if approved.Code != http.StatusOK || none.Actions == nil || len(none.Actions) != 0 || none.Total != "0" {
		t.Fatalf("approved = %d %s", approved.Code, approved.Body.String())
	}

	byID := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions/act_10482", "", testToken)
	byOrder := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions/10482", "", testToken)
	if byID.Code != http.StatusOK || byOrder.Code != http.StatusOK || !strings.Contains(byOrder.Body.String(), `"Oid":"10482"`) || !strings.Contains(byID.Body.String(), `"Payout":150`) {
		t.Fatalf("get action id=%d %s order=%d %s", byID.Code, byID.Body.String(), byOrder.Code, byOrder.Body.String())
	}

	items := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions/10482/Items", "", testToken)
	if items.Code != http.StatusOK || !strings.Contains(items.Body.String(), "AG-TEE-02") || !strings.Contains(items.Body.String(), "AG-MUG-01") || !strings.Contains(items.Body.String(), "1499.00") || !strings.Contains(items.Body.String(), "1598.00") {
		t.Fatalf("items = %d %s", items.Code, items.Body.String())
	}

	missing := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions/missing", "", testToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing action = %d %s", missing.Code, missing.Body.String())
	}

	note := request(t, handler, http.MethodPost, "/Advertisers/acct-acme/Campaigns/1001/Notes", `{"MediaId":"partner_northwind","Content":"Payout for order 10482 is still pending.","Type":"NONE"}`, testToken)
	if note.Code != http.StatusOK || !strings.Contains(note.Body.String(), `"Status":"OK"`) || !strings.Contains(note.Body.String(), "impact.note-0001") {
		t.Fatalf("create note = %d %s", note.Code, note.Body.String())
	}
	notes := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Campaigns/1001/Notes", "", testToken)
	gotNote := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Campaigns/1001/Notes/impact.note-0001", "", testToken)
	if notes.Code != http.StatusOK || gotNote.Code != http.StatusOK || !strings.Contains(notes.Body.String(), "still pending") || !strings.Contains(gotNote.Body.String(), "Val Ortega") {
		t.Fatalf("notes = %d %s get = %d %s", notes.Code, notes.Body.String(), gotNote.Code, gotNote.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/Advertisers/acct-acme/Conversions", `{"CampaignId":1001,"OrderId":"ord-write","MediaPartnerId":"partner_northwind","CurrencyCode":"INR","Amount":499,"Payout":20}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"Status":"QUEUED"`) || !strings.Contains(created.Body.String(), "impact.action-0001") {
		t.Fatalf("conversion = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=ord-write", "", testToken)
	var written generated.ListActionsResponse
	if err := json.Unmarshal(again.Body.Bytes(), &written); err != nil {
		t.Fatal(err)
	}
	if again.Code != http.StatusOK || len(written.Actions) != 1 || written.Actions[0].State != "PENDING" || written.Actions[0].Payout != 20 || written.Actions[0].Amount != 499 || written.Actions[0].EventDate != "2026-08-26T12:00:00Z" {
		t.Fatalf("written action = %d %s", again.Code, again.Body.String())
	}
	dumped, err := (scenarioCodec{}).Dump(context.Background(), db, loadScenario(t, "acme-partners.v1.json").Metadata())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dumped.State), `"items":[]`) {
		t.Fatalf("created action items were not dumped as []: %s", dumped.State)
	}

	invalid := request(t, handler, http.MethodPost, "/Advertisers/acct-acme/Conversions", `{"CampaignId":1001,"OrderId":"ord-write","MediaPartnerId":"partner_northwind","CurrencyCode":"INR","Amount":1,"Nope":true}`, testToken)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown conversion field = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestTwoImpactInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-partners.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPost, "/Advertisers/acct-acme/Campaigns/1001/Notes", `{"MediaId":"partner_northwind","Content":"Only on the first instance."}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/Advertisers/acct-acme/Campaigns/1001/Notes", "", testToken)
	var notes generated.ListNotesResponse
	if err := json.Unmarshal(untouched.Body.Bytes(), &notes); err != nil {
		t.Fatal(err)
	}
	if untouched.Code != http.StatusOK || len(notes.Notes) != 0 {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}
