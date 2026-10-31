package console

import (
	"context"
	"encoding/json"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/asana"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/resources/gmail"
)

func durableRegistry(t *testing.T) *httpresource.Registry {
	t.Helper()
	registry, err := httpresource.NewRegistry(asana.NewResource(), gmail.NewResource())
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func durableManager(t *testing.T, root string) *Manager {
	t.Helper()
	m := New(durableRegistry(t))
	if err := m.Configure(Config{StateDir: root, PublicURL: "http://console.example"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return m
}
func providerCall(t *testing.T, server, id, service, token, method, path, body string) string {
	t.Helper()
	req, err := http.NewRequest(method, server+"/s/"+id+"/"+service+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		t.Fatalf("provider returned %d", resp.StatusCode)
	}
	return readBody(t, resp)
}
func TestMountedStorageSurvivesShutdown(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", "")
	root := filepath.Join(t.TempDir(), "state with spaces #1")
	m := durableManager(t, root)
	session, err := m.Start(context.Background(), StartRequest{Kind: "composition", ID: "durable-workspace", Services: map[string]ServiceRequest{
		"east": {Resource: "asana", Scenario: "asana.acme-sprint.v1"},
		"west": {Resource: "asana", Scenario: "asana.acme-sprint.v1"},
		"mail": {Resource: "gmail", Scenario: "gmail.acme-corp.v1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	resource, _ := m.registry.Get("asana")
	doc, _ := resource.Scenario("asana.acme-sprint.v1")
	var data struct {
		Tasks []struct {
			GID string `json:"gid"`
		} `json:"tasks"`
	}
	if err = json.Unmarshal(doc.State, &data); err != nil {
		t.Fatal(err)
	}
	path := "/api/1.0/tasks/" + data.Tasks[0].GID
	tokens := map[string]string{}
	for _, s := range session.Services {
		tokens[s.Name] = s.Token
	}
	providerCall(t, server.URL, session.ID, "east", tokens["east"], "PUT", path, `{"data":{"notes":"persisted mutation"}}`)
	if _, err = m.sessions[session.ID].runtime.Services["mail"].State.DB().Exec("DELETE FROM messages WHERE id='msg-0001'"); err != nil {
		t.Fatal(err)
	}
	before := m.present(session, httptest.NewRequest("GET", "http://console.example/api/sessions", nil))
	if err = m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	after, ok := m2.Get(session.ID)
	if !ok {
		t.Fatal("sandbox identity not recovered")
	}
	after = m2.present(after, httptest.NewRequest("GET", "http://console.example/api/sessions", nil))
	if before.ID != after.ID || !before.StartedAt.Equal(after.StartedAt) || before.RequestLog != after.RequestLog {
		t.Fatal("session metadata changed")
	}
	for i, s := range before.Services {
		if s.URL != after.Services[i].URL || s.Token != after.Services[i].Token {
			t.Fatal("service connection changed")
		}
	}
	server2 := httptest.NewServer(m2.Handler())
	defer server2.Close()
	if !strings.Contains(providerCall(t, server2.URL, session.ID, "east", tokens["east"], "GET", path, ""), "persisted mutation") {
		t.Fatal("mutation lost")
	}
	if strings.Contains(providerCall(t, server2.URL, session.ID, "west", tokens["west"], "GET", path, ""), "persisted mutation") {
		t.Fatal("state leaked across services")
	}
	var count int
	if err = m2.sessions[session.ID].runtime.Services["mail"].State.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 27 {
		t.Fatal("second provider data lost", err)
	}
	// The export uses persisted baseline identity and current live data.
	resp := get(t, server2.URL+"/api/sessions/"+session.ID+"/state")
	if resp.StatusCode != 200 || !strings.Contains(readBody(t, resp), "persisted mutation") {
		t.Fatal("restored state export failed")
	}
	entries, _, err := readSessionLog(after.RequestLog, 200)
	if err != nil || len(entries) < 3 {
		t.Fatal("trace did not continue", err)
	}
	for _, s := range m2.sessions[session.ID].runtime.Services {
		var mode string
		var sync int
		if err = s.State.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
			t.Fatal("WAL disabled", err)
		}
		if err = s.State.DB().QueryRow("PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
			t.Fatal("FULL sync disabled", err)
		}
	}
	if err = m2.Close(session.ID); err != nil {
		t.Fatal(err)
	}
	if err = m2.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m3 := durableManager(t, root)
	if len(m3.List()) != 0 {
		t.Fatal("deleted sandbox resurrected")
	}
	if _, err = os.Stat(filepath.Join(root, "sandboxes", session.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted files remain")
	}
}

func TestMountedStorageFailsClosed(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Run("unsupported configuration", func(t *testing.T) {
		for _, cfg := range []Config{{StateDir: t.TempDir()}, {StateDir: t.TempDir(), PublicURL: "http://console.example", Proxy: true}} {
			if err := New(durableRegistry(t)).Configure(cfg); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		}
	})
	t.Run("exclusive owner", func(t *testing.T) {
		root := t.TempDir()
		m := durableManager(t, root)
		if err := New(durableRegistry(t)).Configure(Config{StateDir: root, PublicURL: "http://console.example"}); err == nil {
			t.Fatal("second owner accepted")
		}
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
		durableManager(t, root)
	})
	for _, failure := range []string{"missing database", "missing registry", "different origin", "different contract"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			m := durableManager(t, root)
			session, err := m.Start(context.Background(), StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1"})
			if err != nil {
				t.Fatal(err)
			}
			if failure == "different contract" {
				var raw []byte
				if err = m.storage.db.QueryRow("SELECT record FROM sessions WHERE id=?", session.ID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var record storedSession
				json.Unmarshal(raw, &record)
				for name := range record.Contracts {
					record.Contracts[name] = "incompatible"
				}
				raw, _ = json.Marshal(record)
				if _, err = m.storage.db.Exec("UPDATE sessions SET record=? WHERE id=?", raw, session.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.Shutdown(); err != nil {
				t.Fatal(err)
			}
			cfg := Config{StateDir: root, PublicURL: "http://console.example"}
			switch failure {
			case "missing database":
				if err = os.Remove(filepath.Join(root, "sandboxes", session.ID, "services", session.Services[0].Name, "live.db")); err != nil {
					t.Fatal(err)
				}
			case "missing registry":
				if err = os.Remove(filepath.Join(root, "control.db")); err != nil {
					t.Fatal(err)
				}
			case "different origin":
				cfg.PublicURL = "http://other.example"
			}
			m2 := New(durableRegistry(t))
			if err = m2.Configure(cfg); err == nil {
				m2.Shutdown()
				t.Fatal("invalid recovery silently accepted")
			}
			if len(m2.List()) != 0 {
				t.Fatal("partially recovered session exposed")
			}
		})
	}
}

func TestMountedStorageRecoversInterruptedLifecycle(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	root := t.TempDir()
	m := durableManager(t, root)
	for i, status := range []string{"creating", "deleting"} {
		id := []string{"aaaabbbb", "ccccdddd"}[i]
		record := storedSession{ID: id, Started: time.Now()}
		if err := m.storage.create(record); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(m.storage.directory(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if status == "deleting" {
			if err := m.storage.markDeleting(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	var n int
	if err := m2.storage.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); err != nil || n != 0 {
		t.Fatal("transitional records not cleaned", err)
	}
	files, err := os.ReadDir(filepath.Join(root, "sandboxes"))
	if err != nil || len(files) != 0 {
		t.Fatal("transitional files not cleaned", err)
	}
	_, err = m2.Start(context.Background(), StartRequest{Kind: "composition", ID: "unsupported-test", Services: map[string]ServiceRequest{"unknown": {Resource: "unsupported", Scenario: "unsupported.minimal.v1"}}})
	if err == nil || len(m2.List()) != 0 {
		t.Fatal("unsupported resource was accepted")
	}
	// A failed registry publication must not yield a usable, unrecorded sandbox.
	if _, err = m2.storage.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err = m2.Start(context.Background(), StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1"}); err == nil {
		t.Fatal("failed registry write reported success")
	}
	if len(m2.List()) != 0 {
		t.Fatal("failed publication exposed")
	}
}

// Blocking startup proves shutdown retains exclusive ownership until pending
// database initialization is finished, including its durable cleanup.
type gatedResource struct {
	httpresource.Resource
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *gatedResource) NewServer(ctx context.Context, deps httpresource.ServerDependencies) (httpresource.Server, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.Resource.NewServer(ctx, deps)
}
func TestMountedShutdownWaitsForCreation(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	r := &gatedResource{Resource: asana.NewResource(), entered: make(chan struct{}), release: make(chan struct{})}
	registry, err := httpresource.NewRegistry(r)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m := New(registry)
	if err = m.Configure(Config{StateDir: root, PublicURL: "http://console.example"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() {
		_, e := m.Start(context.Background(), StartRequest{Kind: "scenario", ID: "asana.acme-sprint.v1"})
		started <- e
	}()
	select {
	case <-r.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("start did not reach gate")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.Shutdown() }()
	deadline := time.After(10 * time.Second)
	for {
		m.mu.Lock()
		closing := m.closing
		m.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("shutdown not reached")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err = New(durableRegistry(t)).Configure(Config{StateDir: root, PublicURL: "http://console.example"}); err == nil {
		t.Fatal("ownership released during pending creation")
	}
	close(r.release)
	if err = <-started; err == nil {
		t.Fatal("creation published after shutdown")
	}
	if err = <-stopped; err != nil {
		t.Fatal(err)
	}
	m2 := durableManager(t, root)
	if len(m2.List()) != 0 {
		t.Fatal("cancelled creation recovered")
	}
	files, err := os.ReadDir(filepath.Join(root, "sandboxes"))
	if err != nil || len(files) != 0 {
		t.Fatal("cancelled creation leaked files", err)
	}
}
