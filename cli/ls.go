package cli

import (
	"os"

	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/internal/state"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List live instances",
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		s, err := state.Load()
		if err != nil {
			return err
		}
		return output.Instances(os.Stdout, s.Instances, format)
	},
}
