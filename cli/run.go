package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/dumbmachine/fabricate/console"
	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/environments"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/dumbmachine/fabricate/scenario"
	"github.com/spf13/cobra"
)

var (
	runEnvironment string
	runScenario    string
	runProxy       bool
	runUI          bool
	runUIAddr      string
	runUIPublicURL string
)

var runCmd = &cobra.Command{
	Use:   "run <environment-or-service> [--scenario <scenario>] [--proxy] [--ui] [-- <command...>]",
	Short: "Run a disposable Fabricate environment or service",
	Long: `Run starts an official environment, a manifest, or one service with an
explicit scenario. With a command, Fabricate injects connection variables into
that child and tears everything down when it exits. Without a command, the
services stay in the foreground until interrupted and Fabricate prints the
URLs, credentials, proxy settings, and request log needed by another process.

Provider SDKs can keep their production hostnames with --proxy. Unknown proxy
destinations tunnel unchanged unless the environment opts into strict
rejection; the generated CA is never installed globally.

--ui serves the managed console for the duration of this run and opens it in
a local browser. The environment this command started stays up until the
command exits. From the console you can start more environments and scenarios
beside it. Those sandboxes use proxy mode when this command was started with
--proxy, and the direct service URL otherwise. fab serve is the same console
when nothing is wrapped.

--ui-addr is the console listen address. --ui-public-url is the base clients
use when this console is hosted. FAB_CONSOLE_ADDR and FAB_CONSOLE_PUBLIC_URL
set those when the flags are omitted. A public URL with the default listen
address binds 0.0.0.0 on that port.`,
	Args: cobra.ArbitraryArgs,
	RunE: runEnvironmentCommand,
}

func init() {
	runCmd.Flags().StringVar(&runEnvironment, "environment", "", "Environment manifest to run (deprecated; pass it as the first argument)")
	runCmd.Flags().StringVar(&runScenario, "scenario", "", "Scenario to use when running one service")
	runCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
	runCmd.Flags().BoolVar(&runUI, "ui", false, "Serve the managed console and open it while this run is active")
	runCmd.Flags().StringVar(&runUIAddr, "ui-addr", "127.0.0.1:4173", "Address for the console when --ui is set")
	runCmd.Flags().StringVar(&runUIPublicURL, "ui-public-url", "", "Public base URL for the console when --ui is set")
	_ = runCmd.Flags().MarkDeprecated("environment", "pass the environment name or manifest as the first argument")
}

func runEnvironmentCommand(cmd *cobra.Command, args []string) error {
	target, childArgs, err := runTargetAndCommand(args, runEnvironment)
	if err != nil {
		return err
	}
	registry := all.Registry()
	spec, err := resolveRunSpec(target, runScenario, registry)
	if err != nil {
		return err
	}
	var uiSettings consoleSettings
	if runUI {
		uiSettings, err = resolveConsoleSettings(
			runUIAddr, cmd.Flags().Changed("ui-addr"),
			runUIPublicURL, cmd.Flags().Changed("ui-public-url"),
			runProxy, true,
			true,
		)
		if err != nil {
			return fmt.Errorf("run: %w", err)
		}
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := environment.Start(ctx, spec, registry, runProxy)
	if err != nil {
		return err
	}
	manager := console.New(registry)
	var ui *consoleListener
	anchored := false
	closed := false
	closeRuntime := func() error {
		if closed {
			return nil
		}
		closed = true
		if anchored {
			var first error
			if ui != nil {
				first = ui.close()
			}
			if err := manager.Shutdown(); first == nil {
				first = err
			}
			return first
		}
		closeCtx, cancel := environment.CloseTimeout()
		defer cancel()
		return runtime.Close(closeCtx)
	}
	if runUI {
		kind := "environment"
		if runScenario != "" {
			kind = "scenario"
		}
		if err := manager.Configure(console.Config{Proxy: uiSettings.Proxy, PublicURL: uiSettings.PublicURL}); err != nil {
			closeCtx, cancel := environment.CloseTimeout()
			_ = runtime.Close(closeCtx)
			cancel()
			return fmt.Errorf("run: %w", err)
		}
		session, adoptErr := manager.Adopt(spec, runtime, runProxy, kind)
		if adoptErr != nil {
			closeCtx, cancel := environment.CloseTimeout()
			_ = runtime.Close(closeCtx)
			cancel()
			return adoptErr
		}
		anchored = true
		ui, err = listenConsole(uiSettings.ListenAddr, manager.Handler())
		if err != nil {
			_ = manager.Shutdown()
			return err
		}
		if err := openConsoleFn(ui.url); err != nil {
			fmt.Fprintf(os.Stderr, "fab: could not open a browser: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "fab: console %s\n", ui.url)
		if uiSettings.PublicURL != "" {
			fmt.Fprintf(os.Stderr, "fab: public URL %s\n", uiSettings.PublicURL)
		}
		if uiSettings.Widened {
			fmt.Fprintf(os.Stderr, "fab: listening on %s\n", uiSettings.ListenAddr)
		}
		if uiSettings.Proxy {
			fmt.Fprintf(os.Stderr, "fab: proxy mode is on\n")
		}
		fmt.Fprintf(os.Stderr, "fab: sandbox %s is held by this run\n", session.ID)
	}
	defer func() { _ = closeRuntime() }()

	serviceNames := make([]string, 0, len(runtime.Services))
	for name := range runtime.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		service := runtime.Services[name]
		fmt.Fprintf(os.Stderr, "fab: service %s (%s) ready at %s\n", name, service.Resource.Descriptor().ID, service.URL)
	}
	if runtime.Proxy != nil {
		fmt.Fprintf(os.Stderr, "fab: transparent proxy ready at %s (CA %s)\n", runtime.Proxy.URL, runtime.Proxy.CAPath)
		fmt.Fprintf(os.Stderr, "fab: proxying %s\n", strings.Join(runtime.Proxy.InterceptedHosts(), ", "))
	}
	fmt.Fprintf(os.Stderr, "fab: request log %s\n", runtime.Requests.Path())
	if len(childArgs) == 0 {
		printStandaloneEnvironment(os.Stderr, runtime.Environment())
		fmt.Fprintln(os.Stderr, "fab: running until interrupted")
		<-ctx.Done()
		if err := closeRuntime(); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("environment teardown: %w", err)
		}
		return nil
	}
	fmt.Fprintf(os.Stderr, "fab: running %s\n", strings.Join(childArgs, " "))

	child := exec.CommandContext(ctx, childArgs[0], childArgs[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = mergedEnvironment(os.Environ(), runtime.Environment())
	err = child.Run()
	closeErr := closeRuntime()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("child command: %w", err)
	}
	if closeErr != nil && !errors.Is(closeErr, context.Canceled) {
		return fmt.Errorf("environment teardown: %w", closeErr)
	}
	return nil
}

func runTargetAndCommand(args []string, legacyEnvironment string) (string, []string, error) {
	if legacyEnvironment != "" {
		return legacyEnvironment, args, nil
	}
	if len(args) == 0 {
		return "", nil, fmt.Errorf("run: environment or service is required")
	}
	return args[0], args[1:], nil
}

func resolveRunSpec(target, scenarioID string, registry *httpresource.Registry) (environment.Spec, error) {
	if target == "" {
		return environment.Spec{}, fmt.Errorf("run: environment or service is required")
	}
	if scenarioID != "" {
		return wrapStandaloneService(target, scenarioID, registry)
	}
	spec, err := environments.Resolve(target)
	if err == nil {
		return spec, nil
	}
	if _, ok := registry.Get(target); ok {
		return wrapStandaloneService(target, "", registry)
	}
	return environment.Spec{}, fmt.Errorf("run: unknown environment or service %q", target)
}

func wrapStandaloneService(target, scenarioID string, registry *httpresource.Registry) (environment.Spec, error) {
	resource, ok := registry.Get(target)
	if !ok {
		return environment.Spec{}, fmt.Errorf("run: --scenario can only be used with a service")
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		return environment.Spec{}, fmt.Errorf("run: list scenarios for %s: %w", target, err)
	}
	ids := scenario.IDs(docs)
	if scenarioID == "" {
		return environment.Spec{}, fmt.Errorf("run: service %q requires --scenario; choose one of: %s", target, strings.Join(ids, ", "))
	}
	if _, ok := scenario.Lookup(docs, scenarioID); !ok {
		return environment.Spec{}, fmt.Errorf("run: unknown scenario %q for service %q; choose one of: %s", scenarioID, target, strings.Join(ids, ", "))
	}
	return environment.Spec{
		APIVersion: environment.APIVersion,
		Kind:       "Environment",
		Metadata:   environment.Metadata{Name: target},
		Services: map[string]environment.ServiceSpec{
			target: {Resource: target, Scenario: scenarioID},
		},
	}, nil
}

func printStandaloneEnvironment(out *os.File, values map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(out, "fab: %s=%s\n", key, values[key])
	}
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index > 0 {
			values[entry[:index]] = entry[index+1:]
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
