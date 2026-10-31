package console

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCreationIdempotencyScopesAndQuotas(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	root := t.TempDir()
	m := New(durableRegistry(t))
	if err := m.Configure(Config{StateDir: root, PublicURL: "http://console.example", MaxSandboxes: 2}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown() })
	h := m.Handler()
	alpha, _ := testProjectToken(t, h, "alpha", 1)
	beta, _ := testProjectToken(t, h, "beta", 1)
	request := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "same-key"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := m.Start(withActor(context.Background(), principal{Project: "alpha"}), request)
			if e != nil {
				failures <- e
			} else {
				ids <- s.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("concurrent retry duplicated sandbox")
		}
		id = got
	}
	w := controlJSON(t, h, beta, "POST", "/api/sessions", request)
	if w.Code != 201 {
		t.Fatal("same key in separate project", w.Code)
	}
	var other Session
	json.Unmarshal(w.Body.Bytes(), &other)
	if other.ID == id {
		t.Fatal("key was not project-scoped")
	}
	if w = controlJSON(t, h, alpha, "POST", "/api/sessions", request); w.Code != 201 {
		t.Fatal("replay rejected at capacity", w.Code)
	}
	different := request
	different.ID = "gmail.minimal.v1"
	if w = controlJSON(t, h, alpha, "POST", "/api/sessions", different); w.Code != 409 {
		t.Fatal("changed payload reused key", w.Code)
	}
	different = request
	different.IdempotencyKey = "another-key"
	if w = controlJSON(t, h, alpha, "POST", "/api/sessions", different); w.Code != 429 {
		t.Fatal("quota ignored", w.Code)
	}
	different = request
	different.Project = "beta"
	if w = controlJSON(t, h, alpha, "POST", "/api/sessions", different); w.Code != 403 {
		t.Fatal("project override accepted", w.Code)
	}
	if w = controlJSON(t, h, alpha, "GET", "/api/requests/same-key?project=beta", nil); w.Code != 403 {
		t.Fatal("status scope bypass", w.Code)
	}
	if w = controlJSON(t, h, alpha, "GET", "/.well-known/fabricate", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	var index AgentIndex
	json.Unmarshal(w.Body.Bytes(), &index)
	if len(index.Sandboxes) != 1 || index.Sandboxes[0].ID != id {
		t.Fatal("discovery leaked sandbox")
	}
	before, _ := m.Get(id)
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := New(durableRegistry(t))
	if err := m2.Configure(Config{StateDir: root, PublicURL: "http://console.example", MaxSandboxes: 2}); err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	restored, err := m2.Start(withActor(context.Background(), principal{Project: "alpha"}), request)
	if err != nil || restored.ID != id {
		t.Fatal("key not durable", err)
	}
	if restored.Services[0].Token != before.Services[0].Token {
		t.Fatal("idempotent recovery rotated service token")
	}
	if w = controlJSON(t, m2.Handler(), alpha, "GET", "/api/sessions/"+id, nil); w.Code != 200 {
		t.Fatal("project credential not durable", w.Code)
	}
	if err = m2.Close(id); err != nil {
		t.Fatal(err)
	}
	if _, err = m2.Start(withActor(context.Background(), principal{Project: "alpha"}), request); errorStatus(err) != 410 {
		t.Fatal("closed key resurrected sandbox", err)
	}
}
func TestInterruptedCreationResumesWithSameIdentity(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	root := t.TempDir()
	m := durableManager(t, root)
	request := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", Project: "default", IdempotencyKey: "interrupted-key"}
	spec, kind, err := m.resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	normalized := request
	normalized.IdempotencyKey = ""
	raw, _ := json.Marshal(normalized)
	record := storedSession{ID: "aaaabbbb", Project: "default", Spec: spec, Kind: kind, Started: time.Now().UTC(), Request: &request, RequestKey: request.IdempotencyKey, RequestDigest: tokenDigest(string(raw))}
	if err = m.storage.create(record); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(m.storage.directory(record.ID), "services", "partial")
	if err = os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(partial, "live.db"), []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	resumed, err := m2.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != record.ID || !resumed.StartedAt.Equal(record.Started) || len(m2.List()) != 1 {
		t.Fatal("incomplete setup was duplicated or lost")
	}
	if _, err = os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("partial initialization retained")
	}
}
func TestExpiryRenewalAndCredentialRevocation(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	root := t.TempDir()
	m := durableManager(t, root)
	h := m.Handler()
	token, _ := testProjectToken(t, h, "expiring", 2)
	ctx := withActor(context.Background(), principal{Project: "expiring"})
	ttl := int64(60)
	session, err := m.Start(ctx, StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "expiry-1", TTLSeconds: &ttl})
	if err != nil {
		t.Fatal(err)
	}
	oldExpiry := *session.ExpiresAt
	if w := controlJSON(t, h, token, "POST", "/api/sessions/"+session.ID+"/renew", map[string]int64{"ttlSeconds": 4000}); w.Code != 400 {
		t.Fatal("renewal bypassed maximum TTL", w.Code)
	}
	if w := controlJSON(t, h, token, "POST", "/api/sessions/"+session.ID+"/renew", map[string]int64{"ttlSeconds": 120}); w.Code != 200 {
		t.Fatal("renew failed", w.Code)
	}
	if err = m.expireSandboxes(oldExpiry.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get(session.ID); !ok {
		t.Fatal("stale expiry sweep deleted renewed sandbox")
	}
	var digest string
	if err = m.storage.db.QueryRow("SELECT digest FROM project_tokens WHERE project='expiring'").Scan(&digest); err != nil || digest != tokenDigest(token) {
		t.Fatal("credential was not hashed", err)
	}
	if err = m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	h2 := m2.Handler()
	restored, _ := m2.Get(session.ID)
	if !restored.ExpiresAt.After(oldExpiry) {
		t.Fatal("renewal not durable")
	}
	past := time.Now().Add(-time.Second)
	m2.mu.Lock()
	m2.sessions[session.ID].expires = &past
	err = m2.storage.renew(session.ID, &past)
	m2.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.serviceURL(session.ID, session.Services[0].Name); ok {
		t.Fatal("expired provider route remained available before cleanup")
	}
	if w := controlJSON(t, h2, token, "POST", "/api/sessions/"+session.ID+"/renew", map[string]int64{"ttlSeconds": 60}); w.Code != 404 {
		t.Fatal("expired sandbox resurrected", w.Code)
	}
	if err = m2.expireSandboxes(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(m2.storage.directory(session.ID)); !os.IsNotExist(err) {
		t.Fatal("expired data not cleaned")
	}
	if _, err = m2.storage.db.Exec("UPDATE project_tokens SET expires=? WHERE project='expiring'", past.Unix()); err != nil {
		t.Fatal(err)
	}
	if w := controlJSON(t, h2, token, "GET", "/api/sessions", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("expired control token accepted", w.Code)
	}
}

func TestConcurrentRenewalAndInspection(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	session, err := m.Start(context.Background(), StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1"})
	if err != nil {
		t.Fatal(err)
	}
	h := m.Handler()
	var wg sync.WaitGroup
	failures := make(chan int, 20)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = m.Get(session.ID)
			_ = m.ListFor(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			w := controlJSON(t, h, testOperator, "POST", "/api/sessions/"+session.ID+"/renew", map[string]int64{"ttlSeconds": int64(60 + i)})
			if w.Code != 200 {
				failures <- w.Code
			}
		}
	}()
	wg.Wait()
	close(failures)
	for code := range failures {
		t.Fatal("renewal failed", code)
	}
}

func TestProjectPolicyUpdatesAreOperatorScoped(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	h := m.Handler()
	token, _ := testProjectToken(t, h, "policy", 1)
	ctx := withActor(context.Background(), principal{Project: "policy"})
	first, err := m.Start(ctx, StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "policy-first"})
	if err != nil {
		t.Fatal(err)
	}
	second := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "policy-second"}
	if _, err = m.Start(ctx, second); errorStatus(err) != 429 {
		t.Fatal("initial quota ignored", err)
	}
	if w := controlJSON(t, h, token, "PATCH", "/api/projects/policy", map[string]int{"maxSandboxes": 2}); w.Code != 403 {
		t.Fatal("project changed its own quota", w.Code)
	}
	if w := controlJSON(t, h, testOperator, "PATCH", "/api/projects/policy", map[string]int{"maxSandboxes": 2}); w.Code != 200 {
		t.Fatal("operator update failed", w.Code)
	}
	if _, err = m.Start(ctx, second); err != nil {
		t.Fatal("updated quota not applied", err)
	}
	restored, _ := m.Get(first.ID)
	if restored.Services[0].Token != first.Services[0].Token || !restored.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatal("policy update changed existing sandbox")
	}
}

// Interrupted creations own disk and quota even before they have a runtime.
// Their expiry/cancellation must work without restarting the deployment.
func TestUnpublishedCreationExpiryAndScopedCancellation(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	h := m.Handler()
	alpha, _ := testProjectToken(t, h, "orphan-alpha", 1)
	beta, _ := testProjectToken(t, h, "orphan-beta", 1)
	for i, project := range []string{"orphan-alpha", "orphan-beta"} {
		request := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", Project: project, IdempotencyKey: "interrupted"}
		spec, kind, err := m.resolve(request)
		if err != nil {
			t.Fatal(err)
		}
		hashInput := request
		hashInput.IdempotencyKey = ""
		raw, _ := json.Marshal(hashInput)
		expiry := time.Now().Add(time.Hour)
		if i == 0 {
			expiry = time.Now().Add(-time.Second)
		}
		id := []string{"ab12cd34", "ef56ab78"}[i]
		record := storedSession{ID: id, Kind: kind, Spec: spec, Project: project, Expires: &expiry, Request: &request, RequestKey: request.IdempotencyKey, RequestDigest: tokenDigest(string(raw))}
		if err := m.storage.create(record); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(m.storage.directory(id), 0700); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if w := controlJSON(t, h, alpha, "POST", "/api/sessions", request); w.Code != 410 {
				t.Fatalf("expired pending key resumed: %d", w.Code)
			}
		}
	}
	if err := m.expireSandboxes(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, status, err := m.storage.session("ab12cd34"); err != nil || status != "" {
		t.Fatal("expired unpublished row retained", status, err)
	}
	if _, err := os.Stat(m.storage.directory("ab12cd34")); !os.IsNotExist(err) {
		t.Fatal("expired unpublished files retained")
	}
	fresh := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "new-after-expiry"}
	if w := controlJSON(t, h, alpha, "POST", "/api/sessions", fresh); w.Code != 201 {
		t.Fatalf("expired creation still consumes quota: %d", w.Code)
	}
	// Another project's nonexpired pending creation must stay private and intact.
	if w := controlJSON(t, h, alpha, "DELETE", "/api/sessions/ef56ab78", nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, status, _ := m.storage.session("ef56ab78"); status != "creating" {
		t.Fatal("cross-project cancellation changed pending creation")
	}
	if w := controlJSON(t, h, beta, "DELETE", "/api/sessions/ef56ab78", nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, status, _ := m.storage.session("ef56ab78"); status != "" {
		t.Fatal("authorized pending cancellation was a no-op")
	}
	request := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "interrupted"}
	if w := controlJSON(t, h, beta, "POST", "/api/sessions", request); w.Code != 410 {
		t.Fatal("cancelled key resurrected", w.Code)
	}
}

func TestFailedDeletionStaysRevokedAndRetries(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	m := durableManager(t, t.TempDir())
	for _, id := range []string{"asana.acme-sprint.v1", "gmail.minimal.v1"} {
		session, err := m.Start(context.Background(), StartRequest{Kind: "scenario", ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.storage.db.Exec("CREATE TRIGGER reject_removal BEFORE DELETE ON sessions BEGIN SELECT RAISE(FAIL,'fixture deletion blocked'); END"); err != nil {
			t.Fatal(err)
		}
		if err := m.Close(session.ID); err == nil {
			t.Fatal("failed deletion reported success")
		}
		if _, ok := m.Get(session.ID); ok {
			t.Fatal("retiring sandbox remains visible")
		}
		if _, ok := m.serviceURL(session.ID, session.Services[0].Name); ok {
			t.Fatal("failed cleanup kept provider access alive")
		}
		if len(m.List()) != 0 {
			t.Fatal("retiring sandbox advertised as ready")
		}
		if _, err := m.storage.db.Exec("DROP TRIGGER reject_removal"); err != nil {
			t.Fatal(err)
		}
		if err := m.expireSandboxes(time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, status, err := m.storage.session(session.ID); err != nil || status != "" {
			t.Fatal("deletion was not retried", status, err)
		}
	}
}

func TestUnsupportedControlRoutesDoNotServeConsole(t *testing.T) {
	m := New(durableRegistry(t))
	for _, path := range []string{"/api/unsupported", "/api/projects/unknown/nothing"} {
		w := controlJSON(t, m.Handler(), "", "GET", path, nil)
		if w.Code != 404 {
			t.Fatalf("unsupported control route %s returned %d", path, w.Code)
		}
	}
}

func TestRequestCancellationPreventsDelayedCreationAndSurvivesRestart(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	root := t.TempDir()
	m := durableManager(t, root)
	h := m.Handler()
	alpha, _ := testProjectToken(t, h, "cancel-alpha", 2)
	beta, _ := testProjectToken(t, h, "cancel-beta", 2)
	request := StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1", IdempotencyKey: "lost-or-delayed"}
	// Cancel before creation has reached admission. Repeated cancellation is safe.
	for i := 0; i < 2; i++ {
		if w := controlJSON(t, h, alpha, "DELETE", "/api/requests/lost-or-delayed", nil); w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	if w := controlJSON(t, h, alpha, "POST", "/api/sessions", request); w.Code != 410 {
		t.Fatal("late request resurrected cancelled key", w.Code)
	}
	// Same key in another project still works and cannot be cancelled by alpha.
	w := controlJSON(t, h, beta, "POST", "/api/sessions", request)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var session Session
	json.Unmarshal(w.Body.Bytes(), &session)
	if w := controlJSON(t, h, alpha, "DELETE", "/api/requests/lost-or-delayed?project=cancel-beta", nil); w.Code != 403 {
		t.Fatal("scope override", w.Code)
	}
	if _, ok := m.Get(session.ID); !ok {
		t.Fatal("cross-project cancellation deleted session")
	}
	if w := controlJSON(t, h, beta, "DELETE", "/api/requests/lost-or-delayed", nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, ok := m.Get(session.ID); ok {
		t.Fatal("key cancellation did not close live sandbox")
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	for _, token := range []string{alpha, beta} {
		if w := controlJSON(t, m2.Handler(), token, "POST", "/api/sessions", request); w.Code != 410 {
			t.Fatal("cancelled key lost after restart", w.Code)
		}
	}
}
