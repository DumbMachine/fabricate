//go:build e2e

// Package e2e drives the real fab binary against a real HTTP environment.
// Unit tests prove the pieces; these tests prove
// `fab run <environment> -- <command>` starts services, injects connection
// variables, and tears down when the child exits.
//
// Run via `make e2e` (or `go test -tags e2e ./e2e/...`).
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func fab(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(fabBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		t.Logf("fab %s (stderr):\n%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String(), err
}

func TestEnvironmentList(t *testing.T) {
	out, err := fab(t, "environment", "list", "-o", "json")
	if err != nil {
		t.Fatalf("fab environment list: %v\n%s", err, out)
	}
	for _, needle := range []string{"acme-gmail", "acme-posthog"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("environment list missing %q:\n%s", needle, out)
		}
	}
}

func TestRunGmailDirect(t *testing.T) {
	out, err := fab(t, "run", "acme-gmail", "--", "sh", "-c",
		`curl -sS -H "Authorization: Bearer $FAB_SUPPORT_MAIL_TOKEN" "$FAB_SUPPORT_MAIL_URL/gmail/v1/users/me/messages"`)
	if err != nil {
		t.Fatalf("fab run acme-gmail: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"id":"msg-`) {
		t.Fatalf("direct Gmail list missing seeded messages:\n%s", out)
	}
}

func TestRunGmailProxy(t *testing.T) {
	out, err := fab(t, "run", "acme-gmail", "--proxy", "--", "sh", "-c",
		`curl -sS https://gmail.googleapis.com/gmail/v1/users/me/messages`)
	if err != nil {
		t.Fatalf("fab run acme-gmail --proxy: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"id":"msg-`) {
		t.Fatalf("proxy Gmail list missing seeded messages:\n%s", out)
	}
}
