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

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/environments"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
)

var (
	runEnvironment string
	runScenario    string
	runProxy       bool
)

var runCmd = &cobra.Command{
	Use:   "run <environment-or-service> [--scenario <scenario>] [--proxy] [-- <command...>]",
	Short: "Run a disposable Fabricate environment or service",
	Long: `Run starts an official environment, a manifest, or one service with an
explicit scenario. With a command, Fabricate injects connection variables into
that child and tears everything down when it exits. Without a command, the
services stay in the foreground until interrupted and Fabricate prints the
URLs, credentials, proxy settings, and request log needed by another process.

Provider SDKs can keep their production hostnames with --proxy. Unknown proxy
destinations tunnel unchanged unless the environment opts into strict
rejection; the generated CA is never installed globally.`,
	Args: cobra.ArbitraryArgs,
	RunE: runEnvironmentCommand,
}

func init() {
	runCmd.Flags().StringVar(&runEnvironment, "environment", "", "Environment manifest to run (deprecated; pass it as the first argument)")
	runCmd.Flags().StringVar(&runScenario, "scenario", "", "Scenario to use when running one service")
	runCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
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
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := environment.Start(ctx, spec, registry, runProxy)
	if err != nil {
		return err
	}
	closed := false
	closeRuntime := func() error {
		if closed {
			return nil
		}
		closed = true
		closeCtx, cancel := environment.CloseTimeout()
		defer cancel()
		return runtime.Close(closeCtx)
	}
	defer closeRuntime()

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
	if isManifestTarget(target) {
		if scenarioID != "" {
			return environment.Spec{}, fmt.Errorf("run: --scenario can only be used with a service")
		}
		return environment.Load(target)
	}
	names, err := environments.Names()
	if err != nil {
		return environment.Spec{}, err
	}
	for _, name := range names {
		if target == name {
			if scenarioID != "" {
				return environment.Spec{}, fmt.Errorf("run: --scenario can only be used with a service")
			}
			return environments.Load(target)
		}
	}
	resource, ok := registry.Get(target)
	if !ok {
		return environment.Spec{}, fmt.Errorf("run: unknown environment or service %q", target)
	}
	if scenarioID == "" {
		ids, listErr := resource.ScenarioIDs()
		if listErr != nil {
			return environment.Spec{}, fmt.Errorf("run: list scenarios for %s: %w", target, listErr)
		}
		return environment.Spec{}, fmt.Errorf("run: service %q requires --scenario; choose one of: %s", target, strings.Join(ids, ", "))
	}
	if _, err := resource.Scenario(scenarioID); err != nil {
		ids, listErr := resource.ScenarioIDs()
		if listErr != nil {
			return environment.Spec{}, err
		}
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

func isManifestTarget(target string) bool {
	if target == "/dev/stdin" || strings.ContainsRune(target, os.PathSeparator) {
		return true
	}
	if strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml") {
		return true
	}
	_, err := os.Stat(target)
	return err == nil
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
