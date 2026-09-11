package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikePath(t *testing.T) {
	if LooksLikePath("gmail.acme-corp.v1") {
		t.Fatal("catalog ID looked like a path")
	}
	if !LooksLikePath("./inbox.json") || !LooksLikePath("inbox.json") || !LooksLikePath("scenarios/inbox.json") {
		t.Fatal("file refs should look like paths")
	}
}

func TestLoadFileRoundTrip(t *testing.T) {
	path := filepath.Join("..", "resources", "gmail", "scenarios", "minimal.v1.json")
	doc, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID != "gmail.minimal.v1" || doc.Resource != "gmail" {
		t.Fatalf("unexpected document: %#v", doc)
	}
}

func TestResolvePathJoinsBaseDir(t *testing.T) {
	dir := t.TempDir()
	path, err := ResolvePath("inbox.json", dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "inbox.json") {
		t.Fatalf("path = %s", path)
	}
	abs, err := ResolvePath(filepath.Join(dir, "inbox.json"), "/unused")
	if err != nil || abs != filepath.Join(dir, "inbox.json") {
		t.Fatalf("abs = %s err=%v", abs, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
