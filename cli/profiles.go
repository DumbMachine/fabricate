package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/engine/httpmock"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/spf13/cobra"
)

var profilesEngine string

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List available profiles",
	Long: `Profiles lists the catalog: built-ins, registered catalogs, and
your own under ~/.config/fab/profiles. Each row's slug + name is a
runnable ` + "`fab create <slug> -p <name>`" + `.

The authoring loop, end to end:

  fab profiles schema                  # profile.yaml reference (+ seed types per engine)
  fab profiles show <name> --files     # working example incl. its seed files
  fab profiles init <slug> <name>      # scaffold; slug = engine OR mock service
  fab create <slug> <-p name>          # run it`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		entries, err := profile.List(profilesEngine)
		if err != nil {
			return err
		}
		return output.Profiles(os.Stdout, entries, f)
	},
}

var (
	profilesShowEngine string
	profilesShowFiles  bool
)

// showFileCap bounds how much of one seed file `show --files` prints.
// Big SQL seeds run to megabytes; the point of --files is learning the
// shape, not dumping the corpus.
const showFileCap = 200 << 10

var profilesShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show a profile's full definition",
	Long: `Show prints one profile: metadata, seed steps, the exact create
command, and (with --files) the seed files themselves. --files is how
you learn a fixture format from a working example before authoring
your own — e.g. the gmail mock's JSON shape:

  fab profiles show support-inbox --files`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		p, slug, err := resolveProfileForShow(profilesShowEngine, args[0])
		if err != nil {
			return err
		}
		if f == output.FormatJSON {
			entry := profile.Entry{
				Name: p.Name, Engine: p.Engine, Slug: slug, Label: p.Label,
				Description: p.Description, Tags: p.Tags, Source: "loaded",
			}
			if !profilesShowFiles {
				return output.Profiles(os.Stdout, []profile.Entry{entry}, f)
			}
			files, err := readSeedFiles(p)
			if err != nil {
				return err
			}
			return output.JSON(os.Stdout, map[string]any{
				"profile": entry,
				"create":  fmt.Sprintf("fab create %s -p %s", slug, args[0]),
				"env":     p.Env,
				"files":   files,
			})
		}
		fmt.Fprintf(os.Stdout, "name:        %s\n", p.Name)
		fmt.Fprintf(os.Stdout, "engine:      %s\n", p.Engine)
		fmt.Fprintf(os.Stdout, "create:      fab create %s -p %s\n", slug, args[0])
		if p.Label != "" {
			fmt.Fprintf(os.Stdout, "label:       %s\n", p.Label)
		}
		if p.Image != "" {
			fmt.Fprintf(os.Stdout, "image:       %s\n", p.Image)
		}
		if p.Defaults.Database != "" {
			fmt.Fprintf(os.Stdout, "database:    %s\n", p.Defaults.Database)
		}
		if p.Defaults.Username != "" {
			fmt.Fprintf(os.Stdout, "username:    %s\n", p.Defaults.Username)
		}
		if len(p.Env) > 0 {
			fmt.Fprintln(os.Stdout, "env:")
			keys := make([]string, 0, len(p.Env))
			for k := range p.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(os.Stdout, "  %s: %s\n", k, p.Env[k])
			}
		}
		if len(p.Extensions) > 0 {
			fmt.Fprintf(os.Stdout, "extensions:  %v\n", p.Extensions)
		}
		if len(p.Seed) > 0 {
			fmt.Fprintln(os.Stdout, "seed:")
			for _, s := range p.Seed {
				fmt.Fprintf(os.Stdout, "  - %s: %s\n", s.Type, s.File)
			}
		}
		if p.Description != "" {
			fmt.Fprintf(os.Stdout, "\ndescription:\n%s\n", p.Description)
		}
		if profilesShowFiles {
			for _, s := range p.Seed {
				data, err := p.ReadSeed(s)
				if err != nil {
					return fmt.Errorf("read seed %q: %w", s.File, err)
				}
				fmt.Fprintf(os.Stdout, "\n--- %s (%s, %d bytes) ---\n", s.File, s.Type, len(data))
				if len(data) > showFileCap {
					os.Stdout.Write(data[:showFileCap])
					fmt.Fprintf(os.Stdout, "\n... truncated at %d of %d bytes ...\n", showFileCap, len(data))
					continue
				}
				os.Stdout.Write(data)
				if len(data) > 0 && data[len(data)-1] != '\n' {
					fmt.Fprintln(os.Stdout)
				}
			}
		}
		return nil
	},
}

// readSeedFiles returns seed contents keyed by filename for the JSON
// --files view, applying the same per-file cap as the table view.
func readSeedFiles(p *profile.Profile) (map[string]string, error) {
	files := map[string]string{}
	for _, s := range p.Seed {
		data, err := p.ReadSeed(s)
		if err != nil {
			return nil, fmt.Errorf("read seed %q: %w", s.File, err)
		}
		if len(data) > showFileCap {
			data = data[:showFileCap]
		}
		files[s.File] = string(data)
	}
	return files, nil
}

var profilesSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print the profile.yaml schema (for agents authoring profiles)",
	Long: `Schema prints the full profile.yaml schema with field-level
comments and per-engine seed type rules. Pair with` + " `fab engines` " + `to
discover what seed types each engine accepts, then write your own
profile under ~/.config/fab/profiles/<engine>/<name>/.

  fab profiles schema           # commented YAML, copy-paste ready
  fab profiles schema -o json   # machine-readable shape`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		if f == output.FormatJSON {
			return output.SchemaJSON(os.Stdout, engineInfos())
		}
		fmt.Fprint(os.Stdout, profileSchemaYAML(engineInfos()))
		return nil
	},
}

var profilesInitCmd = &cobra.Command{
	Use:   "init <engine|mock-service> <name>",
	Short: "Scaffold a starter profile under ~/.config/fab/profiles/",
	Long: `Init creates a new profile directory at
  ~/.config/fab/profiles/<slug>/<name>/
with a commented profile.yaml that matches the slug's seed-type
contract. The directory's path is printed on stdout — drop your
seed files next to profile.yaml and run` + " `fab create <slug> -p <name>`." + `

The slug is either a container engine or a mock service hosted by the
httpmock engine (` + strings.Join(httpmock.KnownServices, ", ") + `):

  fab profiles init postgres my-app
  fab profiles init ssh dev-bastion
  fab profiles init linear my-board     # httpmock-backed Linear fixture
  fab profiles init gmail my-inbox      # httpmock-backed Gmail fixture

For mock services, learn the fixture shape from a built-in example,
e.g.` + " `fab profiles show support-inbox --files` " + `for gmail.

A user-authored profile shadows a built-in with the same name.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, name := args[0], args[1]
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("invalid profile name %q (no slashes, must be non-empty)", name)
		}

		// A slug is either a real engine or a mock service that the
		// httpmock engine serves; mock services get an httpmock-shaped
		// starter with env.MOCK_SERVICE prefilled.
		mockService := ""
		eng, engErr := resolveEngine(slug)
		if engErr != nil {
			if !isMockService(slug) {
				return fmt.Errorf("%q is neither an engine (%s) nor a mock service (%s)",
					slug, supportedEngines(), joinComma(httpmock.KnownServices))
			}
			mockService = slug
		} else if slug == httpmock.Engine {
			mockService = "TODO" // bare `init httpmock` — user picks the service
		}

		dir := filepath.Join(profile.UserDir(), slug, name)
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("profile dir already exists: %s", dir)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", dir, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}

		var body string
		if mockService != "" {
			body = starterMockProfileYAML(mockService, name)
			seedPath := filepath.Join(dir, "seed.json")
			if err := os.WriteFile(seedPath, []byte("{}\n"), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", seedPath, err)
			}
		} else {
			body = starterProfileYAML(eng.Info(), name)
		}
		yamlPath := filepath.Join(dir, "profile.yaml")
		if err := os.WriteFile(yamlPath, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", yamlPath, err)
		}
		fmt.Fprintln(os.Stderr, "fab: scaffolded profile at:")
		fmt.Fprintln(os.Stdout, dir)
		if mockService != "" {
			if example, ok := mockServiceExamples[mockService]; ok {
				fmt.Fprintf(os.Stderr, "fab: next: fill seed.json — see the fixture shape with `fab profiles show %s --files`,\n", example)
			} else {
				fmt.Fprintf(os.Stderr, "fab: next: fill seed.json — the fixture shape is documented in mockd/services/%s,\n", mockService)
			}
		} else {
			fmt.Fprintln(os.Stderr, "fab: next: edit profile.yaml, drop seed files next to it,")
		}
		fmt.Fprintf(os.Stderr, "fab:       then run `fab create %s -p %s`.\n", slug, name)
		return nil
	},
}

// mockServiceExamples maps a mock service to the built-in profile that
// demonstrates its fixture shape (viewable with `profiles show
// <example> --files`). Services without an entry document the shape in
// their mockd package comment instead.
var mockServiceExamples = map[string]string{
	"gmail":       "support-inbox",
	"linear":      "sprint-board",
	"google-play": "reviews-demo",
}

// isMockService reports whether slug is one of the httpmock image's
// MOCK_SERVICE values.
func isMockService(slug string) bool {
	for _, s := range httpmock.KnownServices {
		if s == slug {
			return true
		}
	}
	return false
}

// starterMockProfileYAML is the httpmock-shaped counterpart of
// starterProfileYAML: env.MOCK_SERVICE prefilled and a single
// mock-fixture seed pointing at the scaffolded seed.json.
func starterMockProfileYAML(service, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", name)
	b.WriteString("engine: httpmock\n")
	fmt.Fprintf(&b, "label: \"%s — TODO\"\n", name)
	b.WriteString("description: |\n")
	b.WriteString("  TODO: describe the seeded state and what workflows it rehearses.\n")
	b.WriteString("env:\n")
	fmt.Fprintf(&b, "  MOCK_SERVICE: %s", service)
	if service == "TODO" {
		fmt.Fprintf(&b, "   # one of: %s", strings.Join(httpmock.KnownServices, ", "))
	}
	b.WriteString("\n")
	b.WriteString("defaults:\n")
	b.WriteString("  password: \"\"      # bearer token clients send; blank → fab generates one\n")
	b.WriteString("seed:\n")
	b.WriteString("  - { type: mock-fixture, file: seed.json }\n")
	fmt.Fprintf(&b, "# Fixture shape: see mockd/services/%s (package comment)", service)
	if example, ok := mockServiceExamples[service]; ok {
		fmt.Fprintf(&b, ",\n# or a working example: fab profiles show %s --files", example)
	}
	b.WriteString("\ntags: []\n")
	return b.String()
}

// profileSchemaYAML returns the canonical, commented profile.yaml
// schema. It's authored as a string (not generated from struct tags)
// so the field comments — which are the load-bearing context for an
// agent — stay readable instead of getting reduced to bare types.
func profileSchemaYAML(infos []engine.Info) string {
	var b strings.Builder
	b.WriteString("# fab profile schema — the shape of profile.yaml.\n")
	b.WriteString("# Drop this file under ~/.config/fab/profiles/<engine>/<name>/\n")
	b.WriteString("# alongside its seed files, then `fab create <engine> -p <name>`.\n\n")
	b.WriteString("name: my-profile           # required. Used as `fab create -p <name>`.\n")
	b.WriteString("engine: postgres           # required. One of: ")
	slugs := make([]string, 0, len(infos))
	for _, info := range infos {
		slugs = append(slugs, info.Slug)
	}
	sort.Strings(slugs)
	b.WriteString(strings.Join(slugs, " | "))
	b.WriteString(".\n")
	b.WriteString("label: \"Short blurb\"      # optional. Shown in `fab profiles`.\n")
	b.WriteString("description: |             # optional. Multiline; first line shown in lists.\n")
	b.WriteString("  Free-form. Use it to document what the profile contains and\n")
	b.WriteString("  what an agent should query/look for.\n")
	b.WriteString("image: \"\"                  # optional. Overrides the engine's default image.\n")
	b.WriteString("defaults:\n")
	b.WriteString("  database: \"\"             # SQL: db name. Mongo: authSource. Redis: numeric db index.\n")
	b.WriteString("  username: \"\"             # SQL/Mongo/SSH: account name. Ignored by Prometheus.\n")
	b.WriteString("  password: \"\"             # blank → fab generates a random one (preferred).\n")
	b.WriteString("env:                       # optional. Extra container env vars.\n")
	b.WriteString("  KEY: value               # ssh: passed through to the container.\n")
	b.WriteString("  # httpmock only — REQUIRED: which mock service this container serves.\n")
	b.WriteString("  # MOCK_SERVICE: " + strings.Join(httpmock.KnownServices, " | ") + "\n")
	b.WriteString("  # Fixture shapes: `fab profiles show <example> --files` (e.g. support-inbox,\n")
	b.WriteString("  # sprint-board, reviews-demo) or mockd/services/<service> package docs.\n")
	b.WriteString("healthcheck:\n")
	b.WriteString("  timeout: 180s            # how long fab waits for the container to be ready.\n")
	b.WriteString("seed:                      # optional. Run after the engine is ready, in array order.\n")
	for _, info := range infos {
		fmt.Fprintf(&b, "  # %s accepts: %s\n", info.Slug, strings.Join(info.SupportedSeeds, ", "))
	}
	b.WriteString("  - { type: sql, file: 00-schema.sql }\n")
	b.WriteString("extensions: []             # postgres only: e.g. [pg_trgm]. Auto-CREATE EXTENSION on boot.\n")
	b.WriteString("tags: []                   # optional. Free-form labels.\n")
	return b.String()
}

// starterProfileYAML returns a minimal but engine-correct profile.yaml
// for `fab profiles init`. The goal is "edit two fields and run it,"
// not "copy the full schema and prune what you don't need."
func starterProfileYAML(info engine.Info, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "engine: %s\n", info.Slug)
	fmt.Fprintf(&b, "label: \"%s — TODO\"\n", name)
	b.WriteString("description: |\n")
	b.WriteString("  TODO: describe what this profile contains and what queries\n")
	b.WriteString("  or workflows it's meant to rehearse.\n")
	fmt.Fprintf(&b, "image: %s\n", info.DefaultImage)
	b.WriteString("defaults:\n")
	b.WriteString("  database: \"\"      # fab default if blank\n")
	b.WriteString("  username: \"\"      # fab default if blank\n")
	b.WriteString("  password: \"\"      # blank → fab generates a random one\n")
	b.WriteString("healthcheck:\n")
	b.WriteString("  timeout: 180s\n")
	b.WriteString("seed: []\n")
	fmt.Fprintf(&b, "# Seed types accepted by the %s engine: %s\n", info.Slug, strings.Join(info.SupportedSeeds, ", "))
	b.WriteString("# Example:\n")
	if len(info.SupportedSeeds) > 0 {
		fmt.Fprintf(&b, "#   - { type: %s, file: 00-init.%s }\n", info.SupportedSeeds[0], suggestedExt(info.SupportedSeeds[0]))
	}
	b.WriteString("tags: []\n")
	return b.String()
}

// suggestedExt maps a seed-type slug to the file extension humans
// typically give the seed file. Cosmetic only — the engine reads the
// file regardless of extension.
func suggestedExt(seedType string) string {
	switch seedType {
	case profile.SeedTypeSQL:
		return "sql"
	case profile.SeedTypeJS:
		return "js"
	case profile.SeedTypeMongoImport, profile.SeedTypeShell:
		return "sh"
	case profile.SeedTypeRedisCLI:
		return "redis"
	case profile.SeedTypePromConfig, profile.SeedTypePromRule:
		return "yml"
	default:
		return "txt"
	}
}

func init() {
	profilesCmd.Flags().StringVar(&profilesEngine, "engine", "", "Filter by catalog slug: an engine ("+supportedEngines()+") or a mock service ("+joinComma(httpmock.KnownServices)+")")
	profilesShowCmd.Flags().StringVar(&profilesShowEngine, "engine", "", "Disambiguate when the profile name exists across engines (e.g. `empty`)")
	profilesShowCmd.Flags().BoolVar(&profilesShowFiles, "files", false, "Also print the profile's seed files (learn a fixture shape from a working example)")
	profilesCmd.AddCommand(profilesShowCmd)
	profilesCmd.AddCommand(profilesSchemaCmd)
	profilesCmd.AddCommand(profilesInitCmd)
}

// resolveProfileForShow loads a profile for `fab profiles show`. When
// engineSlug is set, looks under that engine directly. When it's blank,
// scans the catalog and either picks the unique match or errors with a
// disambiguation hint listing the engines that ship a profile under that
// name. Avoids the silent "first engine alphabetically wins" behaviour
// of bare profile.Load.
func resolveProfileForShow(engineSlug, name string) (*profile.Profile, string, error) {
	if engineSlug != "" {
		p, err := profile.LoadForEngine(engineSlug, name)
		return p, engineSlug, err
	}
	entries, err := profile.List("")
	if err != nil {
		return nil, "", err
	}
	var slugs []string
	for _, e := range entries {
		if e.Name == name {
			// The slug (catalog directory) is what LoadForEngine keys
			// on — for httpmock-backed profiles it differs from the
			// implementation engine (e.g. linear/sprint-board).
			slugs = append(slugs, e.Slug)
		}
	}
	switch len(slugs) {
	case 0:
		return nil, "", fmt.Errorf("profile %q not found", name)
	case 1:
		p, err := profile.LoadForEngine(slugs[0], name)
		return p, slugs[0], err
	default:
		return nil, "", fmt.Errorf("profile name %q is ambiguous (engines: %s) — pass --engine <slug>",
			name, strings.Join(slugs, ", "))
	}
}
