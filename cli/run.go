package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

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
	runDump        string
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
rejection; the generated CA is never installed globally.

--dump writes a canonical snapshot of live service state after the command
exits, or when an interrupt stops a foreground run.`,
	Args: cobra.ArbitraryArgs,
	RunE: runEnvironmentCommand,
}

func init() {
	runCmd.Flags().StringVar(&runEnvironment, "environment", "", "Environment manifest to run (deprecated; pass it as the first argument)")
	runCmd.Flags().StringVar(&runScenario, "scenario", "", "Scenario ID or JSON path when running one service")
	runCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
	runCmd.Flags().StringVar(&runDump, "dump", "", "Write a canonical dump of live service state after the command exits")
	_ = runCmd.Flags().MarkDeprecated("environment", "pass the environment name or manifest as the first argument")
}

func runEnvironmentCommand(cmd *cobra.Command, args []string) error {
	target, childArgs, err := runTargetAndCommand(args, runEnvironment)
	if err != nil {
		return err
	}
	spec, err := resolveRunSpec(target, runScenario, all.Registry())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sess, err := startSession(ctx, spec, runProxy)
	if err != nil {
		return err
	}
	defer sess.Close()
	if len(childArgs) == 0 {
		printStandaloneEnvironment(os.Stderr, sess.runtime.Environment())
		fmt.Fprintln(os.Stderr, "fab: running until interrupted")
		<-ctx.Done()
		if err := dumpSession(ctx, sess, runDump); err != nil {
			return err
		}
		return closeSessionError(sess.Close())
	}
	commandErr := sess.runCommand(ctx, childArgs)
	if err := dumpSession(ctx, sess, runDump); err != nil {
		_ = sess.Close()
		return err
	}
	closeErr := closeSessionError(sess.Close())
	if commandErr != nil {
		return commandErr
	}
	return closeErr
}

func dumpSession(ctx context.Context, sess *session, dir string) error {
	if dir == "" {
		return nil
	}
	snap, err := sess.dump(ctx)
	if err != nil {
		return err
	}
	if err := environment.WriteSnapshot(dir, snap); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "fab: wrote dump %s\n", dir)
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
	if scenarioID == "" {
		docs, err := resource.ScenarioDocuments()
		if err != nil {
			return environment.Spec{}, fmt.Errorf("run: list scenarios for %s: %w", target, err)
		}
		return environment.Spec{}, fmt.Errorf("run: service %q requires --scenario; choose one of: %s", target, strings.Join(scenario.IDs(docs), ", "))
	}
	spec := environment.Spec{
		APIVersion: environment.APIVersion,
		Kind:       "Environment",
		Metadata:   environment.Metadata{Name: target},
		Services: map[string]environment.ServiceSpec{
			target: {Resource: target, Scenario: scenarioID},
		},
	}
	if scenario.LooksLikePath(scenarioID) {
		cwd, err := os.Getwd()
		if err != nil {
			return environment.Spec{}, fmt.Errorf("run: working directory: %w", err)
		}
		spec.SourceDir = cwd
	}
	if _, err := environment.ResolveScenario(resource, spec, scenarioID); err != nil {
		if !scenario.LooksLikePath(scenarioID) {
			docs, listErr := resource.ScenarioDocuments()
			if listErr == nil {
				return environment.Spec{}, fmt.Errorf("run: unknown scenario %q for service %q; choose one of: %s", scenarioID, target, strings.Join(scenario.IDs(docs), ", "))
			}
		}
		return environment.Spec{}, fmt.Errorf("run: %w", err)
	}
	return spec, nil
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
