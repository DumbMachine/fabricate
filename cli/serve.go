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

var (
	serveAddr string
	serveUI   bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the managed sandbox console",
	Long: `Serve starts a local web console for Fabricate's managed mode.

Open the printed URL to browse official environments and scenarios, start a
sandbox, and copy the connection steps for an app or a coding agent. Sandboxes
keep running until you close them in the console or stop this command. This is
the form to run as a deployed service.

Pass --ui to also open that console in a local browser. fab run --ui serves
the same console beside a wrapped command.

The console binds to the address you pass. Sandbox processes stay on loopback.
When you open the console through another host, each service URL uses that
host, and the console forwards the call to the sandbox.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return serveConsole(cmd.Context(), serveAddr, serveUI)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:4173", "Address for the managed console")
	serveCmd.Flags().BoolVar(&serveUI, "ui", false, "Open the console in a local browser")
	rootCmd.AddCommand(serveCmd)
}

type consoleListener struct {
	url    string
	server *http.Server
	errCh  chan error
}

func listenConsole(addr string, handler http.Handler) (*consoleListener, error) {
	listener, err := net.Listen(listenNetwork(addr), addr)
	if err != nil {
		return nil, fmt.Errorf("console: listen: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	return &consoleListener{url: consoleURL(listener.Addr()), server: server, errCh: errCh}, nil
}

func (c *consoleListener) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.server.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	serveErr := <-c.errCh
	if err != nil {
		return err
	}
	return serveErr
}

func serveConsole(ctx context.Context, addr string, open bool) error {
	manager := console.New(all.Registry())
	listener, err := listenConsole(addr, manager.Handler())
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "fab: managed console at %s\n", listener.url)
	if open {
		if err := openConsoleFn(listener.url); err != nil {
			fmt.Fprintf(os.Stderr, "fab: could not open a browser: %v\n", err)
		}
	}
	select {
	case <-ctx.Done():
		if err := listener.close(); err != nil {
			_ = manager.Shutdown()
			return fmt.Errorf("serve: %w", err)
		}
		if err := manager.Shutdown(); err != nil {
			return fmt.Errorf("serve: close sandboxes: %w", err)
		}
		return nil
	case err := <-listener.errCh:
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
