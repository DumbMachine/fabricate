package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dumbmachine/fabricate/console"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
)

var (
	serveAddr      string
	servePublicURL string
	serveProxy     bool
	serveUI        bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the managed sandbox console",
	Long: `Serve starts a web console for Fabricate's managed mode.

Open the printed URL to browse official environments and scenarios, start a
sandbox, and copy the connection steps for an app or a coding agent. Sandboxes
keep running until you close them in the console or stop this command. This is
the form to run as a deployed service.

Start follows how this process was launched. Without --proxy, a sandbox
publishes its service URL on the console host. With --proxy, every sandbox
starts through the transparent proxy: clients keep production hostnames, honor
HTTP_PROXY and the generated CA, and the proxy replaces Authorization on
routed hosts.

The console listens on --addr. The default, 127.0.0.1:4173, is only reachable
on this machine. To host it, set the listen address and the URL clients use:

  fab serve --addr 0.0.0.0:4173 --public-url https://console.example.com
  fab serve --addr 0.0.0.0:4173 --public-url https://console.example.com --proxy

When --public-url is set and --addr is left at the default, serve listens on
0.0.0.0 using the default port. FAB_CONSOLE_ADDR, FAB_CONSOLE_PUBLIC_URL, and
FAB_CONSOLE_PROXY set the same values when the flags are omitted.

Sandbox processes stay on loopback. The console forwards /s/{session}/{service}
to the sandbox. With no --public-url, each service URL uses the host that
opened the console.

Pass --ui to also open the console in a local browser. fab run --ui serves the
same console beside a wrapped command, in proxy mode when that run used
--proxy.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := resolveConsoleSettings(
			serveAddr, cmd.Flags().Changed("addr"),
			servePublicURL, cmd.Flags().Changed("public-url"),
			serveProxy, cmd.Flags().Changed("proxy"),
			serveUI,
		)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return serveConsole(cmd.Context(), settings)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:4173", "Address for the managed console")
	serveCmd.Flags().StringVar(&servePublicURL, "public-url", "", "Public base URL clients use to reach this console")
	serveCmd.Flags().BoolVar(&serveProxy, "proxy", false, "Start every sandbox with the transparent proxy")
	serveCmd.Flags().BoolVar(&serveUI, "ui", false, "Open the console in a local browser")
	rootCmd.AddCommand(serveCmd)
}

type consoleSettings struct {
	ListenAddr string
	PublicURL  string
	Proxy      bool
	Open       bool
	Widened    bool
}

func resolveConsoleSettings(addr string, addrSet bool, publicURL string, publicSet bool, proxy bool, proxySet bool, open bool) (consoleSettings, error) {
	if !addrSet {
		if value := strings.TrimSpace(os.Getenv("FAB_CONSOLE_ADDR")); value != "" {
			addr = value
			addrSet = true
		}
	}
	if !publicSet {
		if value := strings.TrimSpace(os.Getenv("FAB_CONSOLE_PUBLIC_URL")); value != "" {
			publicURL = value
		}
	}
	if !proxySet {
		if value := strings.TrimSpace(os.Getenv("FAB_CONSOLE_PROXY")); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return consoleSettings{}, fmt.Errorf("FAB_CONSOLE_PROXY must be true or false")
			}
			proxy = parsed
		}
	}
	normalized, err := console.NormalizePublicURL(publicURL)
	if err != nil {
		return consoleSettings{}, err
	}
	listen, widened := hostedListenAddr(addr, normalized, addrSet)
	return consoleSettings{
		ListenAddr: listen,
		PublicURL:  normalized,
		Proxy:      proxy,
		Open:       open,
		Widened:    widened,
	}, nil
}

func hostedListenAddr(addr, publicURL string, addrExplicit bool) (string, bool) {
	if addrExplicit || publicURL == "" || !publicHostWantsNetwork(publicURL) {
		return addr, false
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "4173"
	}
	return net.JoinHostPort("0.0.0.0", port), true
}

func publicHostWantsNetwork(publicURL string) bool {
	parsed, err := url.Parse(publicURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" || loopbackHost(host) {
		return false
	}
	return true
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

func serveConsole(ctx context.Context, settings consoleSettings) error {
	manager := console.New(all.Registry())
	if err := manager.Configure(console.Config{Proxy: settings.Proxy, PublicURL: settings.PublicURL}); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	listener, err := listenConsole(settings.ListenAddr, manager.Handler())
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	reportConsole(listener.url, settings)
	if settings.Open {
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

func reportConsole(localURL string, settings consoleSettings) {
	advertised := localURL
	if settings.PublicURL != "" {
		advertised = settings.PublicURL
	}
	fmt.Fprintf(os.Stderr, "fab: managed console at %s\n", advertised)
	if settings.Widened || (settings.PublicURL != "" && advertised != localURL) {
		fmt.Fprintf(os.Stderr, "fab: listening on %s\n", settings.ListenAddr)
	}
	if settings.Proxy {
		fmt.Fprintf(os.Stderr, "fab: proxy mode is on\n")
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

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
