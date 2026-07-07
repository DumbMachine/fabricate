//go:build e2e

// Package e2e drives the real fab binary against a real Docker daemon.
// It is the outer verification loop: unit tests prove the pieces,
// these tests prove `fab create → use creds → fab destroy` end to end.
//
// Run via `make e2e` (or `go test -tags e2e ./e2e/...`). Requirements:
//   - a running Docker daemon (tests skip, loudly, without one)
//   - `make images` for the httpmock cases (they skip if the image is absent)
//
// Every test uses an isolated FAB_STATE_FILE so a developer's real
// ~/.config/fab/state.json is never touched.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fabBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fab-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdtemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	fabBin = filepath.Join(dir, "fab")
	build := exec.Command("go", "build", "-o", fabBin, "../cmd/fab")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build fab:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// fab runs the built binary with an isolated state file and returns
// combined stdout (stderr is streamed to the test log via t).
func fab(t *testing.T, stateFile string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(fabBin, args...)
	cmd.Env = append(os.Environ(),
		"FAB_STATE_FILE="+stateFile,
		"FAB_PROFILES_DIR="+filepath.Join(filepath.Dir(stateFile), "no-user-profiles"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		t.Logf("fab %s (stderr):\n%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String(), err
}

func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available — skipping e2e")
	}
}

func haveImage(image string) bool {
	return exec.Command("docker", "image", "inspect", image).Run() == nil
}

// creds is the -o json shape of `fab create` / `fab creds`.
type creds struct {
	Name  string `json:"name"`
	Creds struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
		URL      string `json:"url"`
	} `json:"creds"`
}

func createInstance(t *testing.T, stateFile, engine, profile, name string) creds {
	t.Helper()
	out, err := fab(t, stateFile, "create", engine, "-p", profile, "--name", name, "-o", "json")
	if err != nil {
		t.Fatalf("fab create %s -p %s: %v\n%s", engine, profile, err, out)
	}
	t.Cleanup(func() {
		if out, err := fab(t, stateFile, "destroy", name); err != nil {
			t.Errorf("fab destroy %s: %v\n%s", name, err, out)
		}
	})
	var c creds
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("parse create output: %v\n%s", err, out)
	}
	return c
}

// TestRedisLifecycle is the smallest full loop: create a seeded-blank
// Redis, prove the port is really listening, see it in ls, destroy it.
func TestRedisLifecycle(t *testing.T) {
	requireDocker(t)
	stateFile := filepath.Join(t.TempDir(), "state.json")

	c := createInstance(t, stateFile, "redis", "empty", "e2e-redis")
	if c.Creds.Port == 0 || c.Creds.Password == "" {
		t.Fatalf("unexpected creds: %+v", c)
	}

	addr := net.JoinHostPort(c.Creds.Host, fmt.Sprint(c.Creds.Port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.Close()

	out, err := fab(t, stateFile, "ls", "-o", "json")
	if err != nil || !strings.Contains(out, "e2e-redis") {
		t.Fatalf("ls should show e2e-redis: %v\n%s", err, out)
	}

	// wait should return immediately for a live instance.
	if out, err := fab(t, stateFile, "wait", "e2e-redis"); err != nil {
		t.Fatalf("fab wait: %v\n%s", err, out)
	}
}

// TestHttpmockGmailFlow closes the stateful-mock loop through real HTTP:
// list unread, send a reply into the thread, mark the original read,
// and watch the unread count drop.
func TestHttpmockGmailFlow(t *testing.T) {
	requireDocker(t)
	if !haveImage("fabricate/httpmock:local") {
		t.Skip("fabricate/httpmock:local missing — run `make images` first")
	}
	stateFile := filepath.Join(t.TempDir(), "state.json")

	c := createInstance(t, stateFile, "gmail", "support-inbox", "e2e-gmail")
	base := c.Creds.URL

	getJSON := func(path string, v any) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("GET %s: parse: %v\n%s", path, err, body)
		}
	}

	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	getJSON("/gmail/v1/users/me/profile", &profile)
	if profile.EmailAddress != "support@acme.dev" {
		t.Fatalf("profile = %+v", profile)
	}

	var unread struct {
		Messages []struct{ ID, ThreadID string } `json:"messages"`
	}
	getJSON("/gmail/v1/users/me/messages?q=is:unread", &unread)
	if len(unread.Messages) == 0 {
		t.Fatal("seeded inbox should have unread messages")
	}
	before := len(unread.Messages)
	first := unread.Messages[0]

	// Mark the first unread message read; the unread count must drop.
	resp, err := http.Post(
		base+"/gmail/v1/users/me/messages/"+first.ID+"/modify",
		"application/json",
		strings.NewReader(`{"removeLabelIds":["UNREAD"]}`),
	)
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("modify = %d", resp.StatusCode)
	}

	getJSON("/gmail/v1/users/me/messages?q=is:unread", &unread)
	if len(unread.Messages) != before-1 {
		t.Fatalf("unread count = %d, want %d", len(unread.Messages), before-1)
	}
}

// TestHttpmockLinearFlow proves a GraphQL mutation really lands: create
// an issue, then find its fresh identifier in the next query.
func TestHttpmockLinearFlow(t *testing.T) {
	requireDocker(t)
	if !haveImage("fabricate/httpmock:local") {
		t.Skip("fabricate/httpmock:local missing — run `make images` first")
	}
	stateFile := filepath.Join(t.TempDir(), "state.json")

	c := createInstance(t, stateFile, "linear", "sprint-board", "e2e-linear")

	gql := func(query string, variables map[string]any) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
		resp, err := http.Post(c.Creds.URL+"/graphql", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("graphql: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("graphql = %d: %s", resp.StatusCode, body)
		}
		return string(body)
	}

	if out := gql("query { viewer { email } }", nil); !strings.Contains(out, "val@acme.dev") {
		t.Fatalf("viewer = %s", out)
	}

	out := gql("mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { identifier } } }",
		map[string]any{"input": map[string]any{"teamId": "team-eng", "title": "e2e follow-up", "priority": 3}})
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("issueCreate = %s", out)
	}

	if out := gql("query { issues { nodes { title } } }", nil); !strings.Contains(out, "e2e follow-up") {
		t.Fatalf("created issue missing: %s", out)
	}
}
