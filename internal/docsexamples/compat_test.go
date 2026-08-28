package docsexamples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCompatibilitySkipVolatile(t *testing.T) {
	repo := t.TempDir()
	destDir := filepath.Join(repo, generatedDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "integration": "gmail",
  "verification": {"client": "googleapis@144.0.0"},
  "testedAt": "2026-01-01T00:00:00.000Z",
  "testedCommit": "aaa"
}
`
	if err := os.WriteFile(filepath.Join(destDir, "gmail.compatibility.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(t.TempDir(), "incoming.json")
	incoming := `{
  "integration": "gmail",
  "verification": {"client": "googleapis@144.0.0"},
  "testedAt": "2026-08-27T12:00:00.000Z",
  "testedCommit": "bbb"
}
`
	if err := os.WriteFile(from, []byte(incoming), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallCompatibility(repo, from, "gmail", ioDiscard()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destDir, "gmail.compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("expected committed timestamps to stay, got %s", got)
	}
}

func TestInstallCompatibilityWritesPayloadChange(t *testing.T) {
	repo := t.TempDir()
	from := filepath.Join(t.TempDir(), "incoming.json")
	incoming := `{"integration":"gmail","verification":{"client":"googleapis@144.0.0"},"testedAt":"2026-08-27T12:00:00.000Z","testedCommit":"bbb"}`
	if err := os.WriteFile(from, []byte(incoming), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallCompatibility(repo, from, "gmail", ioDiscard()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repo, generatedDir, "gmail.compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"client": "googleapis@144.0.0"`) {
		t.Fatalf("got %s", got)
	}
	if !strings.HasSuffix(string(got), "\n") {
		t.Fatal("expected trailing newline")
	}
}

func TestInstallCompatibilityRequiresIntegration(t *testing.T) {
	from := filepath.Join(t.TempDir(), "incoming.json")
	if err := os.WriteFile(from, []byte(`{"verification":{"client":"curl@8"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallCompatibility(t.TempDir(), from, "gmail", ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "missing integration") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallCompatibilityRejectsUnreadableDest(t *testing.T) {
	repo := t.TempDir()
	dest := filepath.Join(repo, generatedDir, "gmail.compatibility.json")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(t.TempDir(), "incoming.json")
	if err := os.WriteFile(from, []byte(`{"integration":"gmail"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallCompatibility(repo, from, "gmail", ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "read existing compatibility report") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallCompatibilityRejectsMismatchedIntegration(t *testing.T) {
	from := filepath.Join(t.TempDir(), "incoming.json")
	if err := os.WriteFile(from, []byte(`{"integration":"asana"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallCompatibility(t.TempDir(), from, "gmail", ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("got %v", err)
	}
}
