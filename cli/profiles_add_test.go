package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAddSource(t *testing.T) {
	cases := []struct {
		in   string
		want addSource
		err  bool
	}{
		{in: "acme/fab-profiles", want: addSource{owner: "acme", repo: "fab-profiles"}},
		{in: "acme/fab-profiles#v1.2.0", want: addSource{owner: "acme", repo: "fab-profiles", ref: "v1.2.0"}},
		{in: "acme/fab-profiles/packs/payments", want: addSource{owner: "acme", repo: "fab-profiles", subdir: "packs/payments"}},
		{in: "acme/fab-profiles/packs/payments#main", want: addSource{owner: "acme", repo: "fab-profiles", ref: "main", subdir: "packs/payments"}},
		{in: "https://github.com/acme/fab-profiles", want: addSource{owner: "acme", repo: "fab-profiles"}},
		{in: "https://github.com/acme/fab-profiles/tree/main/packs/payments", want: addSource{owner: "acme", repo: "fab-profiles", ref: "main", subdir: "packs/payments"}},
		{in: "github.com/acme/fab-profiles.git", want: addSource{owner: "acme", repo: "fab-profiles"}},
		{in: "/abs/path", want: addSource{local: "/abs/path"}},
		{in: "./rel/path", want: addSource{local: "./rel/path"}},
		{in: "acme", err: true},
		{in: "", err: true},
		{in: "acme/repo/../../etc", err: true},
		{in: "https://evil.example/acme/repo", err: true},
		{in: "acme/repo#ref?x=1", err: true},
	}
	for _, c := range cases {
		got, err := parseAddSource(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseAddSource(%q): want error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddSource(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAddSource(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// writePack lays out a two-profile pack: catalog shape with one
// container profile and one httpmock profile.
func writePack(t *testing.T, root string, underProfilesDir bool) {
	t.Helper()
	base := root
	if underProfilesDir {
		base = filepath.Join(root, "profiles")
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("postgres/checkout-db/profile.yaml", "name: checkout-db\nengine: postgres\nlabel: \"Checkout DB\"\nseed:\n  - { type: sql, file: 00-schema.sql }\n")
	write("postgres/checkout-db/00-schema.sql", "CREATE TABLE carts(id int);")
	write("linear/acme-board/profile.yaml", "name: acme-board\nengine: httpmock\nenv:\n  MOCK_SERVICE: linear\nseed:\n  - { type: mock-fixture, file: seed.json }\n")
	write("linear/acme-board/seed.json", "{}")
}

func TestDiscoverPackLayouts(t *testing.T) {
	for _, underProfiles := range []bool{false, true} {
		root := t.TempDir()
		writePack(t, root, underProfiles)
		found, err := discoverPack(root)
		if err != nil {
			t.Fatalf("discoverPack(underProfiles=%v): %v", underProfiles, err)
		}
		if len(found) != 2 {
			t.Fatalf("underProfiles=%v: found %d profiles, want 2: %+v", underProfiles, len(found), found)
		}
	}
}

func TestDiscoverSingleProfileRootDerivesSlug(t *testing.T) {
	root := t.TempDir()
	yaml := "name: solo-board\nengine: httpmock\nenv:\n  MOCK_SERVICE: linear\n"
	if err := os.WriteFile(filepath.Join(root, "profile.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := discoverPack(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Slug != "linear" || found[0].Name != "solo-board" {
		t.Fatalf("found = %+v", found)
	}
}

func TestSelectFromPack(t *testing.T) {
	found := []packEntry{
		{Slug: "postgres", Name: "checkout-db"},
		{Slug: "linear", Name: "acme-board"},
	}
	if _, err := selectFromPack(found, nil, false); err == nil {
		t.Fatal("multi-profile pack with no selection should error, not prompt")
	}
	sel, err := selectFromPack(found, []string{"linear/acme-board"}, false)
	if err != nil || len(sel) != 1 || sel[0].Name != "acme-board" {
		t.Fatalf("slug/name selection = %+v, %v", sel, err)
	}
	sel, err = selectFromPack(found, []string{"checkout-db"}, false)
	if err != nil || len(sel) != 1 || sel[0].Slug != "postgres" {
		t.Fatalf("name selection = %+v, %v", sel, err)
	}
	if _, err := selectFromPack(found, []string{"nope"}, false); err == nil {
		t.Fatal("unknown selection should error")
	}
	sel, err = selectFromPack(found, nil, true)
	if err != nil || len(sel) != 2 {
		t.Fatalf("--all = %+v, %v", sel, err)
	}
	one := found[:1]
	sel, err = selectFromPack(one, nil, false)
	if err != nil || len(sel) != 1 {
		t.Fatalf("single-profile auto-select = %+v, %v", sel, err)
	}
}

// gzTar builds an in-memory tarball with the given entries, mimicking
// GitHub's single-root-dir layout.
func gzTar(t *testing.T, entries map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return &buf
}

func TestExtractTarGzStripsRootAndBlocksTraversal(t *testing.T) {
	dst := t.TempDir()
	ok := gzTar(t, map[string]string{
		"repo-abc123/postgres/db/profile.yaml": "name: db\nengine: postgres\n",
	})
	if err := extractTarGz(ok, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "postgres", "db", "profile.yaml")); err != nil {
		t.Fatalf("root dir not stripped: %v", err)
	}

	evil := gzTar(t, map[string]string{
		"repo-abc123/../../escape.txt": "boom",
	})
	if err := extractTarGz(evil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("traversal should be rejected, got %v", err)
	}
}

func TestCopyProfileDirInstall(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, false)
	found, err := discoverPack(root)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	for _, e := range found {
		if err := copyProfileDir(e.dir, filepath.Join(target, e.Slug, e.Name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{
		"postgres/checkout-db/profile.yaml",
		"postgres/checkout-db/00-schema.sql",
		"linear/acme-board/seed.json",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(want))); err != nil {
			t.Fatalf("missing installed file %s: %v", want, err)
		}
	}
}
