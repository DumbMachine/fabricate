package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/dumbmachine/fabricate/requestlog"
	"github.com/spf13/cobra"
)

var (
	logsAll bool
	logsCat bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <environment>",
	Short: "Show durable request logs for an environment",
	Long: `Logs prints the absolute path of the newest JSONL request trace for an
environment. Use --cat to view it or --all to list every retained run. Request
and response payloads are included; authorization, cookies, and credential
fields are redacted before they reach disk.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		paths, err := requestlog.Find(args[0])
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no request logs found for environment %q", args[0])
		}
		if logsCat {
			file, err := os.Open(paths[0])
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(os.Stdout, file)
			return err
		}
		if logsAll {
			for _, path := range paths {
				fmt.Fprintln(os.Stdout, path)
			}
			return nil
		}
		fmt.Fprintln(os.Stdout, paths[0])
		return nil
	},
}

func init() {
	logsCmd.Flags().BoolVar(&logsAll, "all", false, "List request logs for every retained run")
	logsCmd.Flags().BoolVar(&logsCat, "cat", false, "Print the newest request log instead of its path")
}
