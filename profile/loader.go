package profile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// warnf is the sink for non-fatal loader complaints (bad YAML in a
// single profile, unreadable directory, etc.). Defaults to stderr so
// users see typos that would otherwise make a profile vanish from
// `fab profiles`. Tests or callers that want quiet behavior can swap
// this for a no-op.
var warnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fab: warning: "+format+"\n", args...)
}

// SetWarnf installs a custom warning sink. Useful for tests.
func SetWarnf(f func(format string, args ...any)) { warnf = f }

// catalog is one registered profile source: an fs.FS rooted at a
// <engine>/<name>/profile.yaml tree, plus the source label shown in
// `fab profiles`.
type catalog struct {
	name string
	fsys fs.FS
}

// catalogs is the ordered list of registered profile sources. Later
// registrations take precedence over earlier ones, and the user dir
// (~/.config/fab/profiles) beats them all. The base embedded catalog
// registers itself from the profiles package's init(); a downstream
// binary can embed its own (private) catalog and register it on top:
//
//	//go:embed all:profiles
//	var private embed.FS
//
//	func init() {
//		sub, _ := fs.Sub(private, "profiles")
//		profile.RegisterCatalog("acme", sub)
//	}
var catalogs []catalog

// RegisterCatalog adds a profile catalog. name is the source label
// shown by `fab profiles` ("builtin" for the embedded base catalog);
// fsys must be rooted so <engine>/<name>/profile.yaml paths resolve.
// Catalogs registered later shadow same-name profiles in catalogs
// registered earlier.
func RegisterCatalog(name string, fsys fs.FS) {
	if name == "" {
		name = "builtin"
	}
	catalogs = append(catalogs, catalog{name: name, fsys: fsys})
}

// SetBuiltin registers the embedded built-in catalog. Kept for
// compatibility; new code should call RegisterCatalog.
func SetBuiltin(f fs.FS) { RegisterCatalog("builtin", f) }

// stack returns the profile sources in precedence order: user dir
// first, then registered catalogs newest-first.
func stack() []catalog {
	out := make([]catalog, 0, len(catalogs)+1)
	if dir := UserDir(); dir != "" {
		out = append(out, catalog{name: "user", fsys: os.DirFS(dir)})
	}
	for i := len(catalogs) - 1; i >= 0; i-- {
		out = append(out, catalogs[i])
	}
	return out
}

// UserDir returns the per-user profiles directory. Profiles found
// here override built-ins with the same name.
func UserDir() string {
	if v := os.Getenv("FAB_PROFILES_DIR"); v != "" {
		return v
	}
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "fab", "profiles")
	}
	return filepath.Join(cfg, "fab", "profiles")
}

// Entry is the listing-shape returned by List. We separate it from
// Profile so List doesn't have to parse seed scripts off disk for
// the catalog view.
type Entry struct {
	Name        string   `json:"name"`
	Engine      string   `json:"engine"`
	Slug        string   `json:"slug"` // catalog directory: what `fab create <slug> -p <name>` takes
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Source      string   `json:"source"` // "user" | "builtin" | a registered catalog name
}

// List enumerates every profile, optionally filtered by engine.
// Sources earlier in the stack (user dir, then newest catalog)
// shadow later sources per (slug, name) — the same key `fab create
// <slug> -p <name>` resolves by, so a postgres/empty and a
// kubernetes/empty both list.
func List(engineFilter string) ([]Entry, error) {
	seen := map[string]Entry{}
	for _, c := range stack() {
		entries, err := listFromDir(c.fsys, engineFilter, c.name)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			key := e.Slug + "/" + e.Name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = e
		}
	}

	out := make([]Entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Engine != out[j].Engine {
			return out[i].Engine < out[j].Engine
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// listFromDir scans <root>/<engine>/<name>/profile.yaml entries.
// engineFilter short-circuits at the top-level dir so we don't open
// or parse profiles we'll immediately discard. A missing root dir
// is fine (returns nil, nil); real I/O errors propagate.
func listFromDir(root fs.FS, engineFilter, source string) ([]Entry, error) {
	if root == nil {
		return nil, nil
	}
	engines, err := fs.ReadDir(root, ".")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, eng := range engines {
		if !eng.IsDir() {
			continue
		}
		if engineFilter != "" && eng.Name() != engineFilter {
			continue
		}
		names, err := fs.ReadDir(root, eng.Name())
		if err != nil {
			warnf("read %s profiles dir (%s): %v", source, eng.Name(), err)
			continue
		}
		for _, n := range names {
			if !n.IsDir() {
				continue
			}
			yamlPath := eng.Name() + "/" + n.Name() + "/profile.yaml"
			data, err := fs.ReadFile(root, yamlPath)
			if err != nil {
				// A directory without profile.yaml isn't necessarily
				// broken (someone may be in the middle of authoring
				// it). Only warn for the user-dir case where the
				// missing file is likely a typo — built-ins are
				// shipped clean.
				if source == "user" && !os.IsNotExist(err) {
					warnf("read %s/%s/profile.yaml (%s): %v", eng.Name(), n.Name(), source, err)
				}
				continue
			}
			p, err := parse(data)
			if err != nil {
				warnf("parse %s/%s/profile.yaml (%s): %v — skipped", eng.Name(), n.Name(), source, err)
				continue
			}
			if p.Name == "" {
				p.Name = n.Name()
			}
			if p.Engine == "" {
				p.Engine = eng.Name()
			}
			out = append(out, Entry{
				Name:        p.Name,
				Engine:      p.Engine,
				Slug:        eng.Name(),
				Label:       p.Label,
				Description: p.Description,
				Tags:        p.Tags,
				Source:      source,
			})
		}
	}
	return out, nil
}

// Load resolves a profile by name. Lookup order: user dir first,
// then registered catalogs newest-first. Engine is inferred from the
// directory layout; when two profiles share a name across engines
// (e.g. postgres/empty and mysql/empty), the first engine directory
// alphabetically wins. Callers that need to disambiguate should use
// LoadForEngine.
func Load(name string) (*Profile, error) {
	for _, c := range stack() {
		p, err := loadFromDir(c.fsys, name)
		if err == nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("profile %q not found (looked in %s and %d registered catalog(s))", name, UserDir(), len(catalogs))
}

// LoadForEngine resolves a profile pinned to a specific engine
// directory. Use when the name is ambiguous across engines (e.g.
// both postgres and mysql ship an "empty" profile). Returns an error
// when no profile with the given (engine, name) pair exists in any
// source.
func LoadForEngine(engine, name string) (*Profile, error) {
	for _, c := range stack() {
		p, err := loadFromDirForEngine(c.fsys, engine, name)
		if err == nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("profile %q for engine %q not found (looked in %s and %d registered catalog(s))", name, engine, UserDir(), len(catalogs))
}

func loadFromDirForEngine(root fs.FS, engine, name string) (*Profile, error) {
	if root == nil {
		return nil, fmt.Errorf("nil root")
	}
	yamlPath := engine + "/" + name + "/profile.yaml"
	data, err := fs.ReadFile(root, yamlPath)
	if err != nil {
		return nil, err
	}
	p, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	if p.Engine == "" {
		p.Engine = engine
	}
	sub, err := fs.Sub(root, engine+"/"+name)
	if err != nil {
		return nil, err
	}
	p.FS = sub
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func loadFromDir(root fs.FS, name string) (*Profile, error) {
	if root == nil {
		return nil, fmt.Errorf("nil root")
	}
	engines, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, err
	}
	for _, eng := range engines {
		if !eng.IsDir() {
			continue
		}
		yamlPath := eng.Name() + "/" + name + "/profile.yaml"
		data, err := fs.ReadFile(root, yamlPath)
		if err != nil {
			continue
		}
		p, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", name, err)
		}
		if p.Name == "" {
			p.Name = name
		}
		if p.Engine == "" {
			p.Engine = eng.Name()
		}
		sub, err := fs.Sub(root, eng.Name()+"/"+name)
		if err != nil {
			return nil, err
		}
		p.FS = sub
		if err := p.Validate(); err != nil {
			return nil, err
		}
		return p, nil
	}
	return nil, fmt.Errorf("profile %q not found", name)
}
