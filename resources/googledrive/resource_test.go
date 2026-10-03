package googledrive

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
	"github.com/dumbmachine/fabricate/resources/googledrive/generated"
	"github.com/dumbmachine/fabricate/scenario"
	_ "modernc.org/sqlite"
)

const testToken = "fab_googledrive_test_token"

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
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "googledrive.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	doc := loadScenario(t, "acme-drive.v1.json")
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
	folders := 0
	names := map[string]fixtureFile{}
	for _, file := range state.Files {
		names[file.Name] = file
		if file.MimeType == folderMimeType {
			folders++
		}
	}
	if len(state.Files) != 13 || folders != 5 || len(state.Permissions) != 13 {
		t.Fatalf("files=%d folders=%d permissions=%d", len(state.Files), folders, len(state.Permissions))
	}
	settlement, ok := names["razorpay-settlement-2026-08-25.csv"]
	if !ok || settlement.Parents[0] != "folder-finance-august-2026" {
		t.Fatalf("settlement file = %+v", settlement)
	}
	for _, id := range []string{"pay_Acme10482", "pay_Acme10483", "pay_Acme10484", "rfnd_Acme10484"} {
		if !strings.Contains(settlement.Body, id) || !strings.Contains(settlement.Description, id) {
			t.Fatalf("settlement missing %s", id)
		}
	}
	august := names["August 2026"]
	if len(august.Parents) != 1 || august.Parents[0] != "folder-finance" {
		t.Fatalf("August folder parents = %v", august.Parents)
	}
	for _, name := range []string{"INV-4812-northwind.pdf", "shiprocket-ndr-10483.pdf", "Fernworks-MSA.pdf", "TinyShop-cancellation.pdf", "offer-letter-jordan-hale.pdf", "access-review-2026-08.pdf", "409a-2026.pdf"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("missing file %s", name)
		}
	}
}

func TestMinimalScenarioDumpsEmptySlices(t *testing.T) {
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
		t.Fatalf("load/dump changed minimal:\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.Contains(string(dumped.State), `"files":[]`) || !strings.Contains(string(dumped.State), `"permissions":[]`) {
		t.Fatalf("empty slices were not dumped as []: %s", dumped.State)
	}
	if strings.Contains(string(dumped.State), `"files":null`) || strings.Contains(string(dumped.State), `"permissions":null`) {
		t.Fatalf("empty slices dumped as null: %s", dumped.State)
	}
}

func TestUnknownScenarioFieldsRejected(t *testing.T) {
	codec := scenarioCodec{}
	base := loadScenario(t, "minimal.v1.json")
	cases := []string{
		`{"user":{"displayName":"Val Ortega","emailAddress":"val@acme.example","permissionId":"user-val"},"files":[],"permissions":[],"extra":true}`,
		`{"user":{"displayName":"Val Ortega","emailAddress":"val@acme.example","permissionId":"user-val","photoLink":"x"},"files":[],"permissions":[]}`,
		`{"user":{"displayName":"Val Ortega","emailAddress":"val@acme.example","permissionId":"user-val"},"files":[{"id":"file-1","name":"note.txt","mimeType":"text/plain","description":"","body":"","parents":[],"starred":false,"trashed":false,"createdTime":"2026-08-26T12:00:00Z","modifiedTime":"2026-08-26T12:00:00Z","nope":1}],"permissions":[{"id":"user-val","fileId":"file-1","type":"user","role":"owner","emailAddress":"val@acme.example","displayName":"Val Ortega"}]}`,
	}
	for _, state := range cases {
		doc := base
		doc.State = []byte(state)
		if err := codec.Validate(context.Background(), doc); err == nil {
			t.Fatalf("expected unknown field to fail: %s", state)
		}
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
		if !strings.HasPrefix(path, "/drive/v3/") {
			t.Fatalf("path %s is not a public Drive path", path)
		}
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
	compiled := []string{
		"DriveAboutGet",
		"DriveFilesList",
		"DriveFilesCreate",
		"DriveFilesGet",
		"DriveFilesUpdate",
		"DriveFilesDelete",
		"DriveFilesCopy",
		"DrivePermissionsList",
		"DrivePermissionsCreate",
		"DrivePermissionsGet",
	}
	if len(seen) != len(compiled) {
		t.Fatalf("compiled operation count = %d, want %d (%v)", len(seen), len(compiled), seen)
	}
	for _, id := range compiled {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing compiled operationId %s in %v", id, seen)
		}
	}
	source, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"drive.about.get",
		"drive.files.list",
		"drive.files.create",
		"drive.files.get",
		"drive.files.update",
		"drive.files.delete",
		"drive.files.copy",
		"drive.permissions.list",
		"drive.permissions.create",
		"drive.permissions.get",
	} {
		if !strings.Contains(string(source), "operationId: "+id) {
			t.Fatalf("openapi.yaml missing operationId %s", id)
		}
	}
	descriptor := resource.Descriptor()
	if descriptor.ID != "googledrive" || descriptor.DisplayName != "Google Drive" || descriptor.Version != "v3" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if len(descriptor.ProviderHosts) != 1 || descriptor.ProviderHosts[0] != "www.googleapis.com" {
		t.Fatalf("hosts = %v", descriptor.ProviderHosts)
	}
	if descriptor.HostPrefixes["www.googleapis.com"] != "/drive/" {
		t.Fatalf("host prefixes = %v", descriptor.HostPrefixes)
	}
	contract := resource.Contract()
	if got, want := descriptor.OpenAPIDigest, scenarioDigest(contract.OpenAPIJSON); got != want {
		t.Fatalf("descriptor digest = %s, want %s", got, want)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.IDs(docs); len(got) != 2 || got[0] != "googledrive.acme-drive.v1" || got[1] != "googledrive.minimal.v1" {
		t.Fatalf("scenario ids = %v", got)
	}
}

func TestAcmeScenarioReadWriteRead(t *testing.T) {
	doc := loadScenario(t, "acme-drive.v1.json")
	db := openTestDB(t, doc)
	handler := newTestHandler(t, db, &testIDs{})

	unauthorized := request(t, handler, http.MethodGet, "/drive/v3/about", "", "")
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	wrong := request(t, handler, http.MethodGet, "/drive/v3/about", "", "nope")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s", wrong.Code, wrong.Body.String())
	}

	about := request(t, handler, http.MethodGet, "/drive/v3/about", "", testToken)
	if about.Code != http.StatusOK || !strings.Contains(about.Body.String(), `"emailAddress":"val@acme.example"`) {
		t.Fatalf("about = %d %s", about.Code, about.Body.String())
	}

	settlement := request(t, handler, http.MethodGet, "/drive/v3/files/file-razorpay-settlement-2026-08-25", "", testToken)
	if settlement.Code != http.StatusOK || !strings.Contains(settlement.Body.String(), `"name":"razorpay-settlement-2026-08-25.csv"`) {
		t.Fatalf("settlement = %d %s", settlement.Code, settlement.Body.String())
	}
	for _, id := range []string{"pay_Acme10482", "pay_Acme10483", "pay_Acme10484", "rfnd_Acme10484"} {
		if !strings.Contains(settlement.Body.String(), id) {
			t.Fatalf("settlement metadata missing %s: %s", id, settlement.Body.String())
		}
	}
	query := url.QueryEscape("'folder-finance-august-2026' in parents and trashed = false")
	children := request(t, handler, http.MethodGet, "/drive/v3/files?q="+query, "", testToken)
	if children.Code != http.StatusOK {
		t.Fatalf("children = %d %s", children.Code, children.Body.String())
	}
	for _, name := range []string{"INV-4812-northwind.pdf", "razorpay-settlement-2026-08-25.csv", "shiprocket-ndr-10483.pdf"} {
		if !strings.Contains(children.Body.String(), name) {
			t.Fatalf("August folder missing %s: %s", name, children.Body.String())
		}
	}
	var listed struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(children.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Files) != 3 {
		t.Fatalf("August folder length = %d %s", len(listed.Files), children.Body.String())
	}

	fullText := url.QueryEscape("fullText contains 'pay_Acme10482'")
	found := request(t, handler, http.MethodGet, "/drive/v3/files?q="+fullText, "", testToken)
	if found.Code != http.StatusOK || !strings.Contains(found.Body.String(), "file-razorpay-settlement-2026-08-25") {
		t.Fatalf("fullText = %d %s", found.Code, found.Body.String())
	}

	created := request(t, handler, http.MethodPost, "/drive/v3/files", `{"name":"acme-drive-note.txt","mimeType":"text/plain","parents":["folder-finance"],"description":"note for rfnd_Acme10484"}`, testToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"id":"file-0001"`) || !strings.Contains(created.Body.String(), `"modifiedTime":"2026-08-26T12:00:00Z"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	again := request(t, handler, http.MethodGet, "/drive/v3/files/file-0001", "", testToken)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"name":"acme-drive-note.txt"`) || !strings.Contains(again.Body.String(), `"folder-finance"`) {
		t.Fatalf("created get = %d %s", again.Code, again.Body.String())
	}

	copied := request(t, handler, http.MethodPost, "/drive/v3/files/file-razorpay-settlement-2026-08-25/copy", `{"name":"razorpay-settlement-copy.csv","parents":["folder-finance"]}`, testToken)
	if copied.Code != http.StatusOK || !strings.Contains(copied.Body.String(), `"id":"file-0002"`) || !strings.Contains(copied.Body.String(), "rfnd_Acme10484") {
		t.Fatalf("copy = %d %s", copied.Code, copied.Body.String())
	}
	var body string
	if err := db.QueryRow("SELECT body FROM files WHERE id='file-0002'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pay_Acme10482", "pay_Acme10483", "pay_Acme10484", "rfnd_Acme10484"} {
		if !strings.Contains(body, id) {
			t.Fatalf("copied body missing %s: %s", id, body)
		}
	}

	shared := request(t, handler, http.MethodPost, "/drive/v3/files/file-0001/permissions", `{"type":"user","role":"writer","emailAddress":"aisha@acme.example","displayName":"Aisha Rahman"}`, testToken)
	if shared.Code != http.StatusOK || !strings.Contains(shared.Body.String(), `"id":"permission-0001"`) {
		t.Fatalf("share = %d %s", shared.Code, shared.Body.String())
	}
	perms := request(t, handler, http.MethodGet, "/drive/v3/files/file-0001/permissions", "", testToken)
	if perms.Code != http.StatusOK || !strings.Contains(perms.Body.String(), "aisha@acme.example") || !strings.Contains(perms.Body.String(), "user-val") {
		t.Fatalf("permissions = %d %s", perms.Code, perms.Body.String())
	}
	sharedFile := request(t, handler, http.MethodGet, "/drive/v3/files/file-0001", "", testToken)
	if !strings.Contains(sharedFile.Body.String(), `"shared":true`) {
		t.Fatalf("shared file = %s", sharedFile.Body.String())
	}

	all := request(t, handler, http.MethodGet, "/drive/v3/files?pageSize=100", "", testToken)
	if err := json.Unmarshal(all.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Files) != 15 {
		t.Fatalf("list after writes = %d", len(listed.Files))
	}

	codec := scenarioCodec{}
	dumped, err := codec.Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dumped.State), "acme-drive-note.txt") || !strings.Contains(string(dumped.State), "aisha@acme.example") {
		t.Fatalf("dump missed writes: %s", dumped.State)
	}
}

func TestValidationAndProviderErrorShapes(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-drive.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})

	invalid := request(t, handler, http.MethodGet, "/drive/v3/files?pageSize=0", "", testToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "INVALID_ARGUMENT") {
		t.Fatalf("invalid page = %d %s", invalid.Code, invalid.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/drive/v3/files/not-real", "", testToken)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("missing file = %d %s", missing.Code, missing.Body.String())
	}
	nameless := request(t, handler, http.MethodPost, "/drive/v3/files", `{}`, testToken)
	if nameless.Code != http.StatusBadRequest || !strings.Contains(nameless.Body.String(), "name is required") {
		t.Fatalf("nameless create = %d %s", nameless.Code, nameless.Body.String())
	}
	unknown := request(t, handler, http.MethodPost, "/drive/v3/files", `{"name":"x","nope":1}`, testToken)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", unknown.Code, unknown.Body.String())
	}
	badQuery := request(t, handler, http.MethodGet, "/drive/v3/files?q="+url.QueryEscape("name ="), "", testToken)
	if badQuery.Code != http.StatusBadRequest {
		t.Fatalf("bad query = %d %s", badQuery.Code, badQuery.Body.String())
	}
	folderCopy := request(t, handler, http.MethodPost, "/drive/v3/files/folder-finance/copy", `{}`, testToken)
	if folderCopy.Code != http.StatusBadRequest || !strings.Contains(folderCopy.Body.String(), "Copying folders is not supported") {
		t.Fatalf("copy folder = %d %s", folderCopy.Code, folderCopy.Body.String())
	}
}

func TestTwoDriveInstancesAreIsolated(t *testing.T) {
	doc := loadScenario(t, "acme-drive.v1.json")
	first := newTestHandler(t, openTestDB(t, doc), &testIDs{})
	second := newTestHandler(t, openTestDB(t, doc), &testIDs{})

	response := request(t, first, http.MethodPatch, "/drive/v3/files/file-409a-2026", `{"trashed":true}`, testToken)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"trashed":true`) {
		t.Fatalf("trash = %d %s", response.Code, response.Body.String())
	}
	untouched := request(t, second, http.MethodGet, "/drive/v3/files/file-409a-2026", "", testToken)
	if untouched.Code != http.StatusOK || !strings.Contains(untouched.Body.String(), `"trashed":false`) {
		t.Fatalf("second instance leaked state = %d %s", untouched.Code, untouched.Body.String())
	}
}

func TestDeleteRemovesDescendants(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "minimal.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	folder := request(t, handler, http.MethodPost, "/drive/v3/files", `{"name":"Close","mimeType":"application/vnd.google-apps.folder"}`, testToken)
	if folder.Code != http.StatusOK || !strings.Contains(folder.Body.String(), `"id":"file-0001"`) {
		t.Fatalf("folder = %d %s", folder.Code, folder.Body.String())
	}
	child := request(t, handler, http.MethodPost, "/drive/v3/files", `{"name":"note.txt","mimeType":"text/plain","parents":["file-0001"]}`, testToken)
	if child.Code != http.StatusOK || !strings.Contains(child.Body.String(), `"id":"file-0002"`) {
		t.Fatalf("child = %d %s", child.Code, child.Body.String())
	}
	deleted := request(t, handler, http.MethodDelete, "/drive/v3/files/file-0001", "", testToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	for _, id := range []string{"file-0001", "file-0002"} {
		missing := request(t, handler, http.MethodGet, "/drive/v3/files/"+id, "", testToken)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("get %s after delete = %d %s", id, missing.Code, missing.Body.String())
		}
	}
}

func TestQueryClauses(t *testing.T) {
	file := storedFile{
		Name: "August 2026", MimeType: folderMimeType, Description: "close", Body: "pay_Acme10482",
		Parents: []string{"folder-finance"},
	}
	ok, err := matchQuery(file, "'folder-finance' in parents and trashed = false and name = 'August 2026'")
	if err != nil || !ok {
		t.Fatalf("match = %v %v", ok, err)
	}
	quoted, err := matchQuery(storedFile{Name: "a and b"}, "name = 'a and b'")
	if err != nil || !quoted {
		t.Fatalf("quoted and = %v %v", quoted, err)
	}
	if _, err := matchQuery(file, "name ="); err == nil {
		t.Fatal("expected invalid q")
	}
}
