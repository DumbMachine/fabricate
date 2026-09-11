package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/all"
)

type session struct {
	runtime *environment.Runtime
	close   func() error
}

func startSession(ctx context.Context, spec environment.Spec, enableProxy bool) (*session, error) {
	runtime, err := environment.Start(ctx, spec, all.Registry(), enableProxy)
	if err != nil {
		return nil, err
	}
	closed := false
	sess := &session{runtime: runtime}
	sess.close = func() error {
		if closed {
			return nil
		}
		closed = true
		closeCtx, cancel := environment.CloseTimeout()
		defer cancel()
		return runtime.Close(closeCtx)
	}
	printSessionReady(runtime)
	return sess, nil
}

func (s *session) runCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	fmt.Fprintf(os.Stderr, "fab: running %s\n", strings.Join(args, " "))
	child := exec.CommandContext(ctx, args[0], args[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = mergedEnvironment(os.Environ(), s.runtime.Environment())
	if err := child.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("child command: %w", err)
	}
	return nil
}

func (s *session) dump(ctx context.Context) (environment.Snapshot, error) {
	return s.runtime.Dump(ctx)
}

func (s *session) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

func printSessionReady(runtime *environment.Runtime) {
	names := make([]string, 0, len(runtime.Services))
	for name := range runtime.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := runtime.Services[name]
		fmt.Fprintf(os.Stderr, "fab: service %s (%s) ready at %s\n", name, service.Resource.Descriptor().ID, service.URL)
	}
	if runtime.Proxy != nil {
		fmt.Fprintf(os.Stderr, "fab: transparent proxy ready at %s (CA %s)\n", runtime.Proxy.URL, runtime.Proxy.CAPath)
		fmt.Fprintf(os.Stderr, "fab: proxying %s\n", strings.Join(runtime.Proxy.InterceptedHosts(), ", "))
	}
	fmt.Fprintf(os.Stderr, "fab: request log %s\n", runtime.Requests.Path())
}

func resolveEvalSpec(target, scenarioID string, registry *httpresource.Registry) (environment.Spec, error) {
	return resolveRunSpec(target, scenarioID, registry)
}

func closeSessionError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return fmt.Errorf("environment teardown: %w", err)
}
