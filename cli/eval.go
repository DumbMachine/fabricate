package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
)

var (
	goldDump       string
	evalExpected   string
	evalChecks     string
	evalRewardFile string
	evalDump       string
	diffChecks     string
	diffRewardFile string
)

var goldCmd = &cobra.Command{
	Use:   "gold <environment-or-service> -- <command...>",
	Short: "Replay a command and write a gold dump of live service state",
	Long: `Gold starts an environment, runs a command against it, then writes a
canonical dump of every service. Use the dump as --expected for fab eval.

The command sees the same FAB_* connection variables as fab run.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runGoldCommand,
}

var evalCmd = &cobra.Command{
	Use:   "eval <environment-or-service> -- <command...>",
	Short: "Run a command and score live state against gold and/or field checks",
	Long: `Eval starts an environment, runs a command, dumps live state, and
scores it. Pass --expected for a full-digest gold dump from fab gold, --checks
for ITSM-style field assertions, or both. The process exits 0 only when every
selected scorer passes.

Informational logs go to stderr. The score object is written to stdout.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runEvalCommand,
}

var diffCmd = &cobra.Command{
	Use:   "diff [--checks FILE] <expected-dir> <actual-dir>",
	Short: "Compare two environment dumps, or grade one dump with field checks",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runDiffCommand,
}

func init() {
	goldCmd.Flags().StringVar(&runScenario, "scenario", "", "Scenario ID or JSON path when running one service")
	goldCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
	goldCmd.Flags().StringVar(&goldDump, "dump", "", "Directory to write the gold dump (required)")
	_ = goldCmd.MarkFlagRequired("dump")

	evalCmd.Flags().StringVar(&runScenario, "scenario", "", "Scenario ID or JSON path when running one service")
	evalCmd.Flags().BoolVar(&runProxy, "proxy", false, "Enable transparent HTTPS proxying")
	evalCmd.Flags().StringVar(&evalExpected, "expected", "", "Gold dump directory from fab gold")
	evalCmd.Flags().StringVar(&evalChecks, "checks", "", "JSON field-assertion file (ITSM-style scoring)")
	evalCmd.Flags().StringVar(&evalDump, "dump", "", "Optional directory to write the actual dump")
	evalCmd.Flags().StringVar(&evalRewardFile, "reward-file", "", "Write 1 or 0 for Harbor-style verifiers")

	diffCmd.Flags().StringVar(&diffChecks, "checks", "", "JSON field-assertion file (grade one dump, or extra scorer)")
	diffCmd.Flags().StringVar(&diffRewardFile, "reward-file", "", "Write 1 or 0 for Harbor-style verifiers")

	rootCmd.AddCommand(goldCmd, evalCmd, diffCmd)
}

func runGoldCommand(cmd *cobra.Command, args []string) error {
	target, childArgs, err := evalTargetAndCommand(args)
	if err != nil {
		return err
	}
	spec, err := resolveEvalSpec(target, runScenario, all.Registry())
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
	if err := sess.runCommand(ctx, childArgs); err != nil {
		_ = sess.Close()
		return err
	}
	snap, err := sess.dump(ctx)
	if err != nil {
		return err
	}
	if err := environment.WriteSnapshot(goldDump, snap); err != nil {
		return err
	}
	if err := closeSessionError(sess.Close()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "fab: wrote gold dump %s\n", goldDump)
	return writeSnapshotSummary(os.Stdout, snap)
}

func runEvalCommand(cmd *cobra.Command, args []string) error {
	if evalExpected == "" && evalChecks == "" {
		return fmt.Errorf("eval: --expected and/or --checks is required")
	}
	target, childArgs, err := evalTargetAndCommand(args)
	if err != nil {
		return err
	}
	var expected environment.Snapshot
	if evalExpected != "" {
		expected, err = environment.ReadSnapshot(evalExpected)
		if err != nil {
			return err
		}
	}
	var checks environment.CheckFile
	if evalChecks != "" {
		checks, err = environment.LoadChecks(evalChecks)
		if err != nil {
			return err
		}
	}
	spec, err := resolveEvalSpec(target, runScenario, all.Registry())
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
	commandErr := sess.runCommand(ctx, childArgs)
	snap, dumpErr := sess.dump(ctx)
	closeErr := closeSessionError(sess.Close())
	if dumpErr != nil {
		return dumpErr
	}
	if evalDump != "" {
		if err := environment.WriteSnapshot(evalDump, snap); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "fab: wrote actual dump %s\n", evalDump)
	}
	result := scoreSnapshot(snap, expected, evalExpected != "", checks, evalChecks != "")
	if commandErr != nil {
		result.Passed = false
		result.CommandError = commandErr.Error()
	}
	if err := writeEvalResult(os.Stdout, result); err != nil {
		return err
	}
	if err := writeRewardFile(evalRewardFile, result.Passed); err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !result.Passed {
		if commandErr != nil {
			return commandErr
		}
		return errors.New(failMessage(evalExpected != "", evalChecks != ""))
	}
	return nil
}

func runDiffCommand(_ *cobra.Command, args []string) error {
	if len(args) == 1 && diffChecks == "" {
		return fmt.Errorf("diff: pass <expected-dir> <actual-dir>, or --checks with one dump directory")
	}
	var expected environment.Snapshot
	var actual environment.Snapshot
	var err error
	compareDigest := len(args) == 2
	if compareDigest {
		expected, err = environment.ReadSnapshot(args[0])
		if err != nil {
			return err
		}
		actual, err = environment.ReadSnapshot(args[1])
		if err != nil {
			return err
		}
	} else {
		actual, err = environment.ReadSnapshot(args[0])
		if err != nil {
			return err
		}
	}
	var checks environment.CheckFile
	if diffChecks != "" {
		checks, err = environment.LoadChecks(diffChecks)
		if err != nil {
			return err
		}
	}
	result := scoreSnapshot(actual, expected, compareDigest, checks, diffChecks != "")
	if err := writeEvalResult(os.Stdout, result); err != nil {
		return err
	}
	if err := writeRewardFile(diffRewardFile, result.Passed); err != nil {
		return err
	}
	if !result.Passed {
		return errors.New(failMessage(compareDigest, diffChecks != ""))
	}
	return nil
}

func scoreSnapshot(actual, expected environment.Snapshot, compareDigest bool, checks environment.CheckFile, runChecks bool) evalResult {
	result := evalResult{Passed: true, Environment: actual.Environment}
	if compareDigest {
		diff := environment.DiffSnapshots(expected, actual)
		result.Diff = &diff
		if !diff.Match {
			result.Passed = false
		}
	}
	if runChecks {
		graded := environment.EvaluateChecks(actual, checks)
		result.Checks = &graded
		if !graded.Passed {
			result.Passed = false
		}
	}
	return result
}

func failMessage(compareDigest, runChecks bool) string {
	switch {
	case compareDigest && runChecks:
		return "eval: live state failed gold dump and/or field checks"
	case runChecks:
		return "eval: live state failed field checks"
	default:
		return "eval: live state does not match the gold dump"
	}
}

func evalTargetAndCommand(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("environment or service is required")
	}
	if len(args) < 2 {
		return "", nil, fmt.Errorf("a command is required after --")
	}
	return args[0], args[1:], nil
}

type evalResult struct {
	Passed       bool                     `json:"passed"`
	Environment  string                   `json:"environment"`
	CommandError string                   `json:"command_error,omitempty"`
	Diff         *environment.Diff        `json:"diff,omitempty"`
	Checks       *environment.CheckResult `json:"checks,omitempty"`
}

func writeEvalResult(w *os.File, result evalResult) error {
	format, err := entityFormat()
	if err != nil {
		return err
	}
	if format == output.FormatJSON {
		return output.JSON(w, result)
	}
	status := "PASS"
	if !result.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(w, "%s %s\n", status, result.Environment)
	if result.CommandError != "" {
		fmt.Fprintf(w, "command: %s\n", result.CommandError)
	}
	if result.Diff != nil {
		for _, service := range result.Diff.Services {
			mark := "ok"
			if !service.Match {
				mark = "mismatch"
			}
			fmt.Fprintf(w, "  %s  %s\n", service.Name, mark)
			if service.Match {
				continue
			}
			if service.MissingExpected {
				fmt.Fprintf(w, "    extra service in actual dump\n")
			}
			if service.MissingActual {
				fmt.Fprintf(w, "    missing from actual dump\n")
			}
			for _, pointer := range service.Paths {
				fmt.Fprintf(w, "    %s\n", pointer)
			}
		}
	}
	if result.Checks != nil {
		if result.Checks.Passed {
			fmt.Fprintf(w, "  checks  ok\n")
		} else {
			fmt.Fprintf(w, "  checks  fail\n")
			for _, failure := range result.Checks.Failures {
				fmt.Fprintf(w, "    %s\n", failure)
			}
		}
	}
	return nil
}

func writeSnapshotSummary(w *os.File, snap environment.Snapshot) error {
	format, err := entityFormat()
	if err != nil {
		return err
	}
	type serviceView struct {
		Name     string `json:"name"`
		Resource string `json:"resource"`
		Scenario string `json:"scenario"`
		Digest   string `json:"digest"`
	}
	view := struct {
		Environment string        `json:"environment"`
		Services    []serviceView `json:"services"`
	}{Environment: snap.Environment}
	names := make([]string, 0, len(snap.Services))
	for name := range snap.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := snap.Services[name]
		view.Services = append(view.Services, serviceView{Name: name, Resource: entry.Resource, Scenario: entry.Scenario, Digest: entry.Digest})
	}
	if format == output.FormatJSON {
		return output.JSON(w, view)
	}
	fmt.Fprintf(w, "environment  %s\n", snap.Environment)
	for _, service := range view.Services {
		fmt.Fprintf(w, "%s  %s  %s\n", service.Name, service.Scenario, service.Digest)
	}
	return nil
}

func writeRewardFile(path string, passed bool) error {
	if path == "" {
		return nil
	}
	value := "0\n"
	if passed {
		value = "1\n"
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		return fmt.Errorf("reward file: %w", err)
	}
	return nil
}
