package cli

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/dumbmachine/fabricate/internal/state"

	"github.com/spf13/cobra"
)

var waitTimeout string

var waitCmd = &cobra.Command{
	Use:   "wait <name>",
	Short: "Block until an instance accepts TCP connections",
	Long: `Wait dials the instance's host:port until the connection succeeds
or --timeout expires. Useful in CI: spin up, wait, run tests.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := state.Load()
		if err != nil {
			return err
		}
		inst := s.Find(args[0])
		if inst == nil {
			return fmt.Errorf("instance %q not found", args[0])
		}
		timeout, err := time.ParseDuration(waitTimeout)
		if err != nil {
			return fmt.Errorf("invalid --timeout %q: %w", waitTimeout, err)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()

		addr := net.JoinHostPort(inst.Creds.Host, strconv.Itoa(inst.Creds.Port))
		var d net.Dialer
		for {
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err == nil {
				_ = conn.Close()
				return nil
			}
			if ctx.Err() != nil {
				return fmt.Errorf("timed out waiting for %s: %w", addr, err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	},
}

func init() {
	waitCmd.Flags().StringVar(&waitTimeout, "timeout", "60s", "Max time to wait")
}
