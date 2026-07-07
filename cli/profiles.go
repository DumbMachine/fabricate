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
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/spf13/cobra"
)

var profilesEngine string

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List available profiles",
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

var profilesShowEngine string

var profilesShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show a profile's full definition",
	Args:  cobra.ExactArgs(1),
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
			return output.Profiles(os.Stdout, []profile.Entry{{
				Name: p.Name, Engine: p.Engine, Slug: slug, Label: p.Label,
				Description: p.Description, Tags: p.Tags, Source: "loaded",
			}}, f)
		}
		fmt.Fprintf(os.Stdout, "name:        %s\n", p.Name)
		fmt.Fprintf(os.Stdout, "engine:      %s\n", p.Engine)
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
		return nil
	},
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
	Use:   "init <engine> <name>",
	Short: "Scaffold a starter profile under ~/.config/fab/profiles/",
	Long: `Init creates a new profile directory at
  ~/.config/fab/profiles/<engine>/<name>/
with a commented profile.yaml that matches the engine's seed-type
contract. The directory's path is printed on stdout — drop your
seed files next to profile.yaml and run` + " `fab create <engine> -p <name>`." + `

  fab profiles init postgres my-app
  fab profiles init ssh dev-bastion

A user-authored profile shadows a built-in with the same name.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		engineSlug, name := args[0], args[1]
		eng, err := resolveEngine(engineSlug)
		if err != nil {
			return err
		}
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("invalid profile name %q (no slashes, must be non-empty)", name)
		}
		dir := filepath.Join(profile.UserDir(), engineSlug, name)
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("profile dir already exists: %s", dir)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", dir, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		body := starterProfileYAML(eng.Info(), name)
		yamlPath := filepath.Join(dir, "profile.yaml")
		if err := os.WriteFile(yamlPath, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", yamlPath, err)
		}
		fmt.Fprintln(os.Stderr, "fab: scaffolded profile at:")
		fmt.Fprintln(os.Stdout, dir)
		fmt.Fprintln(os.Stderr, "fab: next: edit profile.yaml, drop seed files next to it,")
		fmt.Fprintf(os.Stderr, "fab:       then run `fab create %s -p %s`.\n", engineSlug, name)
		return nil
	},
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
	b.WriteString("env:                       # optional. Extra container env vars (consumed by SSH today).\n")
	b.WriteString("  KEY: value\n")
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
	profilesCmd.Flags().StringVar(&profilesEngine, "engine", "", "Filter by engine (postgres|mysql|mongodb|redis|prometheus|ssh|kubernetes|github)")
	profilesShowCmd.Flags().StringVar(&profilesShowEngine, "engine", "", "Disambiguate when the profile name exists across engines (e.g. `empty`)")
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
