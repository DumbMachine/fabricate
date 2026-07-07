// Package cli wires fab's CLI. Subcommands live in sibling files.
package cli

import (
	"fmt"
	"os"

	// Side-effect import: registers the embedded profile catalog
	// onto the loader. Without this, fab has no built-in profiles.
	_ "github.com/dumbmachine/fabricate/profiles"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "fab",
	Short: "Spin up real, seeded, throwaway local resources",
	Long: `fab provisions ephemeral, seeded resources from declarative profiles
and returns connection credentials: real databases and hosts
(postgres, mysql, mongodb, redis, prometheus, ssh, kubernetes,
aws_console) and stateful HTTP-API mocks where writes take effect
(linear, gmail, google-play, github, ...). Docker-backed by default,
with an optional Kubernetes target.

Discover what's available, then create:

  fab engines                     # engines + accepted seed types
  fab profiles                    # every (slug, profile) pair
  fab create <slug> -p <profile>  # provision; prints credentials

Author your own profile without reading source:

  fab profiles schema                    # the profile.yaml reference
  fab profiles show <name> --files       # crib a working example + its seed files
  fab profiles init <slug> <name>        # scaffold under ~/.config/fab/profiles/`,
	SilenceUsage: true,
}

var outputFlag string

func init() {
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "",
		"Output format: json, table, env, url (default: table for TTY, json for pipes)")

	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(credsCmd)
	rootCmd.AddCommand(destroyCmd)
	rootCmd.AddCommand(profilesCmd)
	rootCmd.AddCommand(enginesCmd)
	rootCmd.AddCommand(waitCmd)
}

// Execute is main's entrypoint.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
