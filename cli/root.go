// Package cli wires fab's CLI. Subcommands live in sibling files.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "fab",
	Short: "Run disposable, stateful provider APIs",
	Long: `fab runs reproducible API sandbox environments with known state.

Run an official environment, a local manifest, or one service:

  fab run acme-gmail
  fab run ./environment.yaml -- npm test
  fab run gmail --scenario gmail.minimal.v1

Inspect the available definitions:

  fab environment list
  fab service list --environment acme-support-desk
  fab resource list
  fab scenario list gmail`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var outputFlag string

func init() {
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "",
		"Output format: json or table (default: table for TTY, json for pipes)")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(logsCmd)
}

// Execute is main's entrypoint.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
