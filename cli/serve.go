package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dumbmachine/fabricate/console"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
)

var serveAddr string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the managed sandbox console",
	Long: `Serve starts a local web console for Fabricate's managed mode.

Open the printed URL to browse official environments and scenarios, start a
sandbox, and copy the connection steps for an app or a coding agent. Sandboxes
keep running until you close them in the console or stop this command.

The console binds to the address you pass. Sandbox processes stay on loopback.
When you open the console through another host, each service URL uses that
host, and the console forwards the call to the sandbox.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return serveConsole(cmd.Context(), serveAddr)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:4173", "Address for the managed console")
	rootCmd.AddCommand(serveCmd)
}

func serveConsole(ctx context.Context, addr string) error {
	listener, err := net.Listen(listenNetwork(addr), addr)
	if err != nil {
		return fmt.Errorf("serve: listen: %w", err)
	}
	manager := console.New(all.Registry())
	server := &http.Server{
		Handler:           manager.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	fmt.Fprintf(os.Stderr, "fab: managed console at %s\n", consoleURL(listener.Addr()))
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err := manager.Shutdown(); err != nil {
			return fmt.Errorf("serve: close sandboxes: %w", err)
		}
		return <-errCh
	case err := <-errCh:
		_ = manager.Shutdown()
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}

func listenNetwork(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "tcp"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "tcp"
	}
	if ip.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}

func consoleURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String()
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
