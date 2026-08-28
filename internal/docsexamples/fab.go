package docsexamples

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// RunFab executes `fab run <manifest> [--proxy] -- argv...`
// and returns stdout. Informational fab logs stay on stderr.
func RunFab(fab, environment string, proxy bool, argv []string) ([]byte, error) {
	if fab == "" {
		return nil, fmt.Errorf("docsexamples: fab binary is required")
	}
	absFab, err := filepath.Abs(fab)
	if err != nil {
		return nil, fmt.Errorf("docsexamples: resolve fab: %w", err)
	}
	args := []string{"run", environment}
	if proxy {
		args = append(args, "--proxy")
	}
	args = append(args, "--")
	args = append(args, argv...)
	cmd := exec.Command(absFab, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("fab run exited %d", exit.ExitCode())
		}
		return nil, fmt.Errorf("fab run: %w", err)
	}
	return out, nil
}
