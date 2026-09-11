package docsexamples

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunFab executes a documented command against a local environment manifest
// and returns stdout. Informational fab logs stay on stderr.
func RunFab(fab, repo string, spec Spec) ([]byte, error) {
	if fab == "" {
		return nil, fmt.Errorf("docsexamples: fab binary is required")
	}
	if repo == "" {
		return nil, fmt.Errorf("docsexamples: repository root is required")
	}
	absFab, err := filepath.Abs(fab)
	if err != nil {
		return nil, fmt.Errorf("docsexamples: resolve fab: %w", err)
	}
	environment := filepath.Join(repo, filepath.FromSlash(spec.Environment))
	args, err := fabArgs(environment, spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(absFab, args...)
	cmd.Dir = repo
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			if spec.AllowFailure {
				return out, nil
			}
			return nil, fmt.Errorf("fab %s exited %d", args[0], exit.ExitCode())
		}
		return nil, fmt.Errorf("fab %s: %w", args[0], err)
	}
	return out, nil
}

func fabArgs(environment string, spec Spec) ([]string, error) {
	invocation := strings.TrimSpace(spec.Invocation)
	if invocation == "" {
		invocation = "run"
	}
	switch invocation {
	case "run", "eval", "gold":
	default:
		return nil, fmt.Errorf("docsexamples: invocation %q is not supported", invocation)
	}
	args := []string{invocation, environment}
	if spec.Proxy {
		args = append(args, "--proxy")
	}
	args = append(args, spec.Flags...)
	args = append(args, "--")
	args = append(args, spec.Argv...)
	return args, nil
}
