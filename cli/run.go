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
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
)

var (
	runEnvironment string
	runProxy       bool
)

var runCmd = &cobra.Command{
	Use:   "run --environment <manifest> [--proxy] -- <command...>",
	Short: "Run a command inside a disposable Fabricate environment",
	Long: `Run starts every HTTP service in an environment, optionally starts a
process-scoped transparent HTTPS proxy, injects connection and trust variables
into the child command, and always tears the environment down afterward.

Provider SDKs can keep their production hostnames with --proxy. Unknown proxy
destinations tunnel unchanged unless the environment opts into strict
rejection; the generated CA is never installed globally.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runEnvironmentCommand,
}

func init() {
	runCmd.Flags().StringVar(&runEnvironment, "environment", "", "Environment manifest to run (required)")
	runCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
	_ = runCmd.MarkFlagRequired("environment")
}

func runEnvironmentCommand(cmd *cobra.Command, args []string) error {
	spec, err := environment.Load(runEnvironment)
	if err != nil {
		return err
	}
	registry := all.Registry()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := environment.Start(ctx, spec, registry, runProxy)
	if err != nil {
		return err
	}
	closeRuntime := func() error {
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
	}
	fmt.Fprintf(os.Stderr, "fab: request log %s\n", runtime.Requests.Path())
	fmt.Fprintf(os.Stderr, "fab: running %s\n", strings.Join(args, " "))

	child := exec.CommandContext(ctx, args[0], args[1:]...)
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
