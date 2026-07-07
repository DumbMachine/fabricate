package profile

import (
	"os"
	"testing"
	"testing/fstest"
)

// resetCatalogs clears registered catalogs for a test and restores
// them afterward, and points the user dir somewhere empty so the
// developer's real ~/.config/fab/profiles can't leak into assertions.
func resetCatalogs(t *testing.T) {
	t.Helper()
	old := catalogs
	catalogs = nil
	t.Setenv("FAB_PROFILES_DIR", t.TempDir())
	t.Cleanup(func() { catalogs = old })
}

func yamlProfile(name, engine, label string) []byte {
	return []byte("name: " + name + "\nengine: " + engine + "\nlabel: \"" + label + "\"\n")
}

func TestRegisterCatalogPrecedence(t *testing.T) {
	resetCatalogs(t)

	base := fstest.MapFS{
		"postgres/app/profile.yaml":   {Data: yamlProfile("app", "postgres", "base")},
		"postgres/other/profile.yaml": {Data: yamlProfile("other", "postgres", "base-only")},
	}
	private := fstest.MapFS{
		"postgres/app/profile.yaml": {Data: yamlProfile("app", "postgres", "private")},
	}
	RegisterCatalog("builtin", base)
	RegisterCatalog("acme", private)

	p, err := LoadForEngine("postgres", "app")
	if err != nil {
		t.Fatalf("LoadForEngine: %v", err)
	}
	if p.Label != "private" {
		t.Fatalf("later catalog should shadow earlier: got label %q", p.Label)
	}

	// The base-only profile is still reachable.
	p, err = LoadForEngine("postgres", "other")
	if err != nil {
		t.Fatalf("LoadForEngine(other): %v", err)
	}
	if p.Label != "base-only" {
		t.Fatalf("got label %q", p.Label)
	}
}

func TestListMergesAndLabelsSources(t *testing.T) {
	resetCatalogs(t)

	RegisterCatalog("builtin", fstest.MapFS{
		"postgres/app/profile.yaml":    {Data: yamlProfile("app", "postgres", "base")},
		"redis/cache/profile.yaml":     {Data: yamlProfile("cache", "redis", "base")},
		"postgres/broken/profile.yaml": {Data: []byte(":\tnot yaml")},
	})
	RegisterCatalog("acme", fstest.MapFS{
		"postgres/app/profile.yaml": {Data: yamlProfile("app", "postgres", "private")},
	})
	var warned bool
	oldWarnf := warnf
	SetWarnf(func(string, ...any) { warned = true })
	t.Cleanup(func() { warnf = oldWarnf })

	entries, err := List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Name] = e.Source
	}
	if got["app"] != "acme" {
		t.Fatalf("app should come from the acme catalog, got %q", got["app"])
	}
	if got["cache"] != "builtin" {
		t.Fatalf("cache should come from builtin, got %q", got["cache"])
	}
	for _, e := range entries {
		if e.Slug == "" {
			t.Fatalf("entry %q missing slug (catalog directory)", e.Name)
		}
	}
	if _, ok := got["broken"]; ok {
		t.Fatal("broken profile should be skipped, not listed")
	}
	if !warned {
		t.Fatal("broken profile should produce a warning")
	}

	// Engine filter.
	entries, err = List("redis")
	if err != nil {
		t.Fatalf("List(redis): %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "cache" {
		t.Fatalf("List(redis) = %+v", entries)
	}
}

func TestUserDirBeatsCatalogs(t *testing.T) {
	resetCatalogs(t)

	RegisterCatalog("builtin", fstest.MapFS{
		"postgres/app/profile.yaml": {Data: yamlProfile("app", "postgres", "base")},
	})

	dir := t.TempDir()
	t.Setenv("FAB_PROFILES_DIR", dir)
	writeProfile(t, dir, "postgres", "app", yamlProfile("app", "postgres", "user"))

	p, err := LoadForEngine("postgres", "app")
	if err != nil {
		t.Fatalf("LoadForEngine: %v", err)
	}
	if p.Label != "user" {
		t.Fatalf("user dir should win, got label %q", p.Label)
	}

	entries, err := List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Source != "user" {
		t.Fatalf("List = %+v", entries)
	}
}

// writeProfile drops a profile.yaml under dir/<engine>/<name>/.
func writeProfile(t *testing.T, dir, engine, name string, data []byte) {
	t.Helper()
	pdir := dir + "/" + engine + "/" + name
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pdir+"/profile.yaml", data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSeedFileFromCatalog(t *testing.T) {
	resetCatalogs(t)

	RegisterCatalog("builtin", fstest.MapFS{
		"postgres/app/profile.yaml": {Data: []byte(
			"name: app\nengine: postgres\nseed:\n  - { type: sql, file: 00-schema.sql }\n")},
		"postgres/app/00-schema.sql": {Data: []byte("CREATE TABLE t(x int);")},
	})

	p, err := LoadForEngine("postgres", "app")
	if err != nil {
		t.Fatalf("LoadForEngine: %v", err)
	}
	data, err := p.ReadSeed(p.Seed[0])
	if err != nil {
		t.Fatalf("ReadSeed: %v", err)
	}
	if string(data) != "CREATE TABLE t(x int);" {
		t.Fatalf("seed = %q", data)
	}
}

func TestValidateRejectsEscapingSeedPaths(t *testing.T) {
	p := &Profile{
		Name:   "x",
		Engine: "postgres",
		Seed:   []SeedStep{{Type: "sql", File: "../outside.sql"}},
	}
	if err := p.Validate(); err == nil {
		t.Fatal("want error for seed path escaping the profile dir")
	}
}

func TestLoadMissingProfileError(t *testing.T) {
	resetCatalogs(t)
	if _, err := Load("nope"); err == nil {
		t.Fatal("want error for unknown profile")
	}
	if _, err := LoadForEngine("postgres", "nope"); err == nil {
		t.Fatal("want error for unknown (engine, profile)")
	}
}
