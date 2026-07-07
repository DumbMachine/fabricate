package cli

import (
	"fmt"
	"os"

	"github.com/dumbmachine/fabricate/engine/kubernetes"
	"github.com/dumbmachine/fabricate/engine/ssh"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/internal/state"

	"github.com/spf13/cobra"
)

var credsCmd = &cobra.Command{
	Use:   "creds <name>",
	Short: "Print connection credentials for an instance",
	Long: `Creds prints connection credentials in the requested format.

Common patterns:
  fab creds web                # human-readable table
  fab creds web --url          # just the connection URL
  eval "$(fab creds web -o env)"  # source as shell env vars`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		if credsURLOnly {
			f = output.FormatURL
		}
		if credsEnv {
			f = output.FormatEnv
		}
		s, err := state.Load()
		if err != nil {
			return err
		}
		inst := s.Find(args[0])
		if inst == nil {
			return fmt.Errorf("instance %q not found", args[0])
		}
		// Plumb engine-specific on-disk artifacts (ssh private key, kube
		// kubeconfig) so `fab creds <x> -o env` can emit FAB_SSH_KEY_PATH
		// / KUBECONFIG. The output package only has Creds, not the
		// container ID, so the cmd layer has to do the bind.
		switch inst.Engine {
		case ssh.Engine:
			output.SetSSHKeyPath(ssh.KeyPath(inst.ArtifactKey()))
		case kubernetes.Engine:
			output.SetKubeconfigPath(kubernetes.KubeconfigPath(inst.ArtifactKey()))
		}
		return output.Creds(os.Stdout, inst.Creds, f)
	},
}

var (
	credsURLOnly bool
	credsEnv     bool
)

func init() {
	credsCmd.Flags().BoolVar(&credsURLOnly, "url", false, "Print only the connection URL")
	credsCmd.Flags().BoolVar(&credsEnv, "env", false, "Print shell-sourceable env vars")
}
