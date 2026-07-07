// Package profile loads YAML profile definitions (engine + seed
// scripts + defaults) from both the embedded built-in catalog and
// the user's ~/.config/fab/profiles directory.
package profile

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is the declarative recipe for one fab-able instance.
// Fields here track the schema in profiles/<engine>/<name>/profile.yaml.
// The seed files referenced by Seed[].File live next to profile.yaml
// in the same directory (fs.FS rooted at the profile dir).
type Profile struct {
	Name        string            `yaml:"name"`
	Engine      string            `yaml:"engine"`
	Label       string            `yaml:"label,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Image       string            `yaml:"image,omitempty"`
	Defaults    Defaults          `yaml:"defaults,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Healthcheck Healthcheck       `yaml:"healthcheck,omitempty"`
	Seed        []SeedStep        `yaml:"seed,omitempty"`
	Extensions  []string          `yaml:"extensions,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"`

	// FS is the filesystem rooted at the profile directory, used by
	// engines to resolve SeedStep.File. Set by the loader.
	FS fs.FS `yaml:"-"`
}

// Defaults are engine-defaults baked into the profile. Fab will
// generate a password per-instance unless one is set here (we never
// commit a default password, but a profile *can* pin one for fully
// deterministic local dev — useful in CI fixtures).
type Defaults struct {
	Database string `yaml:"database,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

// Healthcheck overrides the engine's default readiness probe. Most
// profiles will omit this and inherit the engine default.
type Healthcheck struct {
	Cmd     []string `yaml:"cmd,omitempty"`
	Timeout string   `yaml:"timeout,omitempty"`
}

// SeedStep is one operation run after the engine reaches a usable
// state but before fab returns. Type picks the runner; File is
// relative to the profile directory.
//
// Supported types per engine:
//
//	postgres:   sql
//	mysql:      sql
//	mongodb:    js, mongoimport
//	redis:      redis-cli         (one RESP/CLI command per line)
//	prometheus: prom-config, prom-rule
//	ssh:        shell             (executed as root inside the container)
//	github:     emulate-config (single YAML for vercel-labs/emulate)
//	            emulate-postseed (JSON list of API calls to replay after boot —
//	                             for state emulate doesn't seed natively, like
//	                             issues / labels / milestones)
//	aws_console: moto-seed (YAML describing AWS-compatible Moto state)
type SeedStep struct {
	Type string `yaml:"type"`
	File string `yaml:"file"`
}

// Seed types. Each engine picks the subset it understands; unknown
// types surface as an engine-level error so a typo isn't silently
// dropped.
const (
	SeedTypeSQL             = "sql"
	SeedTypeJS              = "js"
	SeedTypeMongoImport     = "mongoimport"
	SeedTypeRedisCLI        = "redis-cli"
	SeedTypePromConfig      = "prom-config"
	SeedTypePromRule        = "prom-rule"
	SeedTypeShell           = "shell"
	SeedTypeEmulateConfig   = "emulate-config"
	SeedTypeEmulatePostSeed = "emulate-postseed"
	SeedTypeMotoSeed        = "moto-seed"
	// SeedTypeMockFixture is the httpmock engine's seed: a JSON fixture the
	// selected mock service loads into its SQLite tables at boot.
	SeedTypeMockFixture = "mock-fixture"
)

// Validate enforces the small invariants downstream code relies on
// (non-empty name + engine, known seed types). The full set of
// known seed types is engine-specific and checked there; here we
// only catch shape errors that would short-circuit a load.
func (p *Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("profile: missing name")
	}
	if strings.TrimSpace(p.Engine) == "" {
		return fmt.Errorf("profile %q: missing engine", p.Name)
	}
	for i, s := range p.Seed {
		if s.Type == "" {
			return fmt.Errorf("profile %q: seed[%d] missing type", p.Name, i)
		}
		if s.File == "" {
			return fmt.Errorf("profile %q: seed[%d] missing file", p.Name, i)
		}
		if strings.HasPrefix(s.File, "/") || strings.Contains(s.File, "..") {
			return fmt.Errorf("profile %q: seed[%d] file must be relative inside the profile dir", p.Name, i)
		}
	}
	return nil
}

// parse unmarshals a profile.yaml into a Profile. The FS field is
// set by the caller after parse; we don't know it here.
func parse(data []byte) (*Profile, error) {
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile parse: %w", err)
	}
	return &p, nil
}

// ReadSeed reads a seed file from the profile FS. Exists so engines
// don't reach into the profile-dir structure directly.
func (p *Profile) ReadSeed(s SeedStep) ([]byte, error) {
	if p.FS == nil {
		return nil, fmt.Errorf("profile %q: no filesystem bound", p.Name)
	}
	return fs.ReadFile(p.FS, path.Clean(s.File))
}
