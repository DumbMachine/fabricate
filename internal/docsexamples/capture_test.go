package docsexamples

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDigestRootIgnoresTestsConformanceAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep.go"), "package gmail\n")
	writeFile(t, filepath.Join(dir, "keep_test.go"), "package gmail\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# gmail\n")
	writeFile(t, filepath.Join(dir, "conformance", "client.mjs"), "export {}\n")
	writeFile(t, filepath.Join(dir, "testdata", "fixture.json"), "{}\n")

	first, err := digestRoot(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "keep_test.go"), "package gmail\nfunc TestX(t *testing.T) {}\n")
	writeFile(t, filepath.Join(dir, "conformance", "client.mjs"), "changed\n")
	second, err := digestRoot(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("ignored files changed the digest: %s vs %s", first, second)
	}

	writeFile(t, filepath.Join(dir, "keep.go"), "package gmail\nconst X = 1\n")
	third, err := digestRoot(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("expected keep.go change to dirty the digest")
	}
}

func TestDigestRootRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.go")
	writeFile(t, target, "package gmail\n")
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := digestRoot(dir, "."); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestDigestStableAcrossWalkOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.go"), "b")
	writeFile(t, filepath.Join(dir, "a.go"), "a")
	first, err := digestRoot(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.go"), "a")
	writeFile(t, filepath.Join(dir, "b.go"), "b")
	second, err := digestRoot(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest depended on write order: %s vs %s", first, second)
	}
}

func TestLoadRepositoryCatalog(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repo := filepath.Join(filepath.Dir(file), "..", "..")
	specs, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatal("no example specs loaded")
	}
	seen := map[string]struct{}{}
	for _, spec := range specs {
		if _, ok := seen[spec.ID]; ok {
			t.Fatalf("duplicate id %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
		if len(spec.Roots) == 0 {
			t.Fatalf("%s has no roots", spec.ID)
		}
		prov, err := digestSpec(repo, spec)
		if err != nil {
			t.Fatalf("%s: %v", spec.ID, err)
		}
		if prov.Digest == "" || prov.Algorithm != Algorithm {
			t.Fatalf("%s: invalid provenance", spec.ID)
		}
	}
}

func TestLoadAssemblesRootsFromEnvironment(t *testing.T) {
	repo := writeExampleRepo(t)
	specs, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs", len(specs))
	}
	spec := specs[0]
	want := []string{
		"cli/run.go",
		"engine/http",
		"environment",
		"environments/acme-gmail.yaml",
		"httpresource",
		"packages/docs-content/resources/_examples/gmail-list-messages.json",
		"requestlog",
		"resources/gmail",
		"scenario",
	}
	if strings.Join(spec.Roots, ",") != strings.Join(want, ",") {
		t.Fatalf("roots = %v, want %v", spec.Roots, want)
	}
}

func TestCaptureSkipsMatchingProvenance(t *testing.T) {
	repo := writeExampleRepo(t)
	specs, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := func(string, bool, []string) ([]byte, error) {
		calls++
		return []byte(`{"ok":true}`), nil
	}
	if _, err := Capture(repo, runner, Options{All: true}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("forced capture calls = %d", calls)
	}
	calls = 0
	results, err := Capture(repo, runner, Options{}, ioDiscard())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("skip recaptured; runner called %d times", calls)
	}
	if len(results) != 1 || results[0].Action != "skip" {
		t.Fatalf("results = %+v", results)
	}

	writeFile(t, filepath.Join(repo, "resources/gmail/server.go"), "package gmail\nconst changed = 1\n")
	results, err = Capture(repo, runner, Options{}, ioDiscard())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("dirty resource did not recapture; calls = %d", calls)
	}
	if results[0].Action != "capture" {
		t.Fatalf("results = %+v", results)
	}

	prov, err := digestSpec(repo, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, specs[0].OutputPath))
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Provenance.Digest != prov.Digest {
		t.Fatalf("snapshot digest %s, want %s", envelope.Provenance.Digest, prov.Digest)
	}
	if envelope.Provenance.Algorithm != Algorithm {
		t.Fatalf("algorithm = %q", envelope.Provenance.Algorithm)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Output); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `{"ok":true}` {
		t.Fatalf("output = %s", envelope.Output)
	}
}

func TestCaptureConcatenatedJSONDocuments(t *testing.T) {
	repo := writeExampleRepo(t)
	runner := func(string, bool, []string) ([]byte, error) {
		return []byte("{\"z\":1}\n\n{\"a\":2}\n"), nil
	}
	if _, err := Capture(repo, runner, Options{All: true}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "packages/docs-content/resources/_generated/gmail-list-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Output); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `[{"z":1},{"a":2}]` {
		t.Fatalf("concatenated stdout = %s", envelope.Output)
	}
}

func TestCapturePreservesJSONKeyOrder(t *testing.T) {
	repo := writeExampleRepo(t)
	runner := func(string, bool, []string) ([]byte, error) {
		return []byte(`{"z":1,"a":2}`), nil
	}
	if _, err := Capture(repo, runner, Options{All: true}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "packages/docs-content/resources/_generated/gmail-list-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"z": 1`)) || !bytes.Contains(data, []byte(`"a": 2`)) {
		t.Fatalf("missing keys: %s", data)
	}
	z := bytes.Index(data, []byte(`"z"`))
	a := bytes.Index(data, []byte(`"a"`))
	if z < 0 || a < 0 || z > a {
		t.Fatalf("key order was not preserved: %s", data)
	}
}

func TestNeedsCaptureReasons(t *testing.T) {
	repo := writeExampleRepo(t)
	specs, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[0]
	prov, err := digestSpec(repo, spec)
	if err != nil {
		t.Fatal(err)
	}
	dirty, reason, err := needsCapture(repo, spec, prov, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || reason != "missing snapshot" {
		t.Fatalf("missing snapshot: dirty=%v reason=%q", dirty, reason)
	}
	if err := writeEnvelope(repo, spec, []byte(`{"ok":true}`), prov); err != nil {
		t.Fatal(err)
	}
	dirty, _, err = needsCapture(repo, spec, prov, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("matching provenance should skip")
	}
	dirty, reason, err = needsCapture(repo, spec, prov, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || reason != "forced" {
		t.Fatalf("force: dirty=%v reason=%q", dirty, reason)
	}
	dirty, reason, err = needsCapture(repo, spec, prov, false, []string{spec.OutputPath})
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || reason != "snapshot changed" {
		t.Fatalf("generated path: dirty=%v reason=%q", dirty, reason)
	}
	dirty, reason, err = needsCapture(repo, spec, prov, false, []string{"resources/gmail/server.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || !strings.Contains(reason, "resources/gmail") {
		t.Fatalf("resource path: dirty=%v reason=%q", dirty, reason)
	}
}

func TestToolChanged(t *testing.T) {
	if !toolChanged([]string{"internal/docsexamples/digest.go"}) {
		t.Fatal("internal/docsexamples should force all")
	}
	if !toolChanged([]string{"cmd/docsexamples/main.go"}) {
		t.Fatal("cmd/docsexamples should force all")
	}
	if toolChanged([]string{"packages/docs-content/index.mdx"}) {
		t.Fatal("docs copy should not force all")
	}
}

func TestCaptureForceWhenGeneratedFileChanged(t *testing.T) {
	repo := writeExampleRepo(t)
	gitInit(t, repo)
	runner := func(string, bool, []string) ([]byte, error) {
		return []byte(`{"ok":true}`), nil
	}
	if _, err := Capture(repo, runner, Options{All: true}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repo, "snapshot")
	writeFile(t, filepath.Join(repo, "packages/docs-content/resources/_generated/gmail-list-messages.json"), "{}\n")
	gitCommitAll(t, repo, "hand edit")

	calls := 0
	runner = func(string, bool, []string) ([]byte, error) {
		calls++
		return []byte(`{"ok":true}`), nil
	}
	base := gitOutput(t, repo, "rev-parse", "HEAD~1")
	if _, err := Capture(repo, runner, Options{SinceRef: base}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("hand-edited snapshot was skipped; calls = %d", calls)
	}
}

func TestSelectUnknownID(t *testing.T) {
	repo := writeExampleRepo(t)
	_, err := Capture(repo, func(string, bool, []string) ([]byte, error) {
		t.Fatal("runner should not run")
		return nil, nil
	}, Options{IDs: []string{"missing"}}, ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "unknown example id") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejectsOutputPathOutsideGenerated(t *testing.T) {
	repo := writeExampleRepo(t)
	specPath := filepath.Join(repo, "packages/docs-content/resources/_examples/gmail-list-messages.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("packages/docs-content/resources/_generated/gmail-list-messages.json"), []byte("packages/docs-content/gmail.json"), 1)
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "outputPath must be under") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejectsCompatibilityOutputPath(t *testing.T) {
	repo := writeExampleRepo(t)
	specPath := filepath.Join(repo, "packages/docs-content/resources/_examples/gmail-list-messages.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("packages/docs-content/resources/_generated/gmail-list-messages.json"), []byte("packages/docs-content/resources/_generated/gmail.compatibility.json"), 1)
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "compatibility report") {
		t.Fatalf("got %v", err)
	}
}

func TestCaptureResourceFilter(t *testing.T) {
	repo := writeExampleRepo(t)
	_, err := Capture(repo, func(string, bool, []string) ([]byte, error) {
		t.Fatal("runner should not run")
		return nil, nil
	}, Options{Resources: []string{"resources/asana"}}, ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "no examples matched") {
		t.Fatalf("got %v", err)
	}

	calls := 0
	_, err = Capture(repo, func(string, bool, []string) ([]byte, error) {
		calls++
		return []byte(`{"ok":true}`), nil
	}, Options{Resources: []string{"gmail"}}, ioDiscard())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("gmail resource filter calls = %d", calls)
	}
}

func TestNormalizeResource(t *testing.T) {
	if got := normalizeResource("gmail"); got != "resources/gmail" {
		t.Fatalf("short name = %q", got)
	}
	if got := normalizeResource("resources/gmail"); got != "resources/gmail" {
		t.Fatalf("path = %q", got)
	}
	if got := normalizeResources([]string{"gmail", "resources/gmail", "asana"}); len(got) != 2 || got[0] != "resources/gmail" || got[1] != "resources/asana" {
		t.Fatalf("dedupe = %v", got)
	}
}

func TestToolChangeForcesAll(t *testing.T) {
	repo := writeExampleRepo(t)
	gitInit(t, repo)
	runner := func(string, bool, []string) ([]byte, error) {
		return []byte(`{"ok":true}`), nil
	}
	if _, err := Capture(repo, runner, Options{All: true}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repo, "snapshot")
	writeFile(t, filepath.Join(repo, "internal/docsexamples/digest.go"), "package docsexamples\n")
	gitCommitAll(t, repo, "tool")

	calls := 0
	runner = func(string, bool, []string) ([]byte, error) {
		calls++
		return []byte(`{"ok":true}`), nil
	}
	base := gitOutput(t, repo, "rev-parse", "HEAD~1")
	if _, err := Capture(repo, runner, Options{SinceRef: base}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("tool change did not force recapture; calls = %d", calls)
	}
}

func writeExampleRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "cli/run.go"), "package cli\n")
	writeFile(t, filepath.Join(repo, "engine/http/proxy.go"), "package http\n")
	writeFile(t, filepath.Join(repo, "environment/runner.go"), "package environment\n")
	writeFile(t, filepath.Join(repo, "httpresource/resource.go"), "package httpresource\n")
	writeFile(t, filepath.Join(repo, "requestlog/log.go"), "package requestlog\n")
	writeFile(t, filepath.Join(repo, "scenario/digest.go"), "package scenario\n")
	writeFile(t, filepath.Join(repo, "resources/gmail/server.go"), "package gmail\n")
	writeFile(t, filepath.Join(repo, "environments/acme-gmail.yaml"), ""+
		"apiVersion: fabricate.dev/v1alpha1\n"+
		"kind: Environment\n"+
		"metadata:\n  name: acme-gmail\n"+
		"services:\n  support-mail:\n    resource: gmail\n    scenario: gmail.acme-corp.v1\n")
	writeFile(t, filepath.Join(repo, "packages/docs-content/resources/_examples/catalog.json"), `{
  "sharedRoots": [
    "cli/run.go",
    "engine/http",
    "environment",
    "httpresource",
    "requestlog",
    "scenario"
  ]
}
`)
	writeFile(t, filepath.Join(repo, "packages/docs-content/resources/_examples/gmail-list-messages.json"), `{
  "id": "gmail-list-messages",
  "title": "List seeded messages",
  "environment": "environments/acme-gmail.yaml",
  "environmentLabel": "Acme's Gmail",
  "proxy": true,
  "outputPath": "packages/docs-content/resources/_generated/gmail-list-messages.json",
  "publishedCommand": "curl -fsSL example | fab run --environment /dev/stdin --proxy -- curl -sS https://gmail.googleapis.com/gmail/v1/users/me/messages",
  "argv": ["curl", "-sS", "https://gmail.googleapis.com/gmail/v1/users/me/messages"]
}
`)
	return repo
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ioDiscard() *bytes.Buffer { return &bytes.Buffer{} }

func gitInit(t *testing.T, repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for this test")
	}
	gitRun(t, repo, "init", "--template=")
	gitRun(t, repo, "config", "user.email", "docs-examples@test")
	gitRun(t, repo, "config", "user.name", "docs-examples")
	gitRun(t, repo, "config", "commit.gpgsign", "false")
	gitCommitAll(t, repo, "init")
}

func gitCommitAll(t *testing.T, repo, message string) {
	t.Helper()
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", message)
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if args[0] == "init" {
			t.Skipf("git init unavailable in this environment: %s", out)
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
