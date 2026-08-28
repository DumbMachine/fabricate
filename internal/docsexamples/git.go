package docsexamples

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func changedFiles(repo, ref string) ([]string, error) {
	if ref == "" {
		return nil, fmt.Errorf("docsexamples: since-ref is empty")
	}
	if strings.Trim(ref, "0") == "" {
		return nil, fmt.Errorf("docsexamples: since-ref %q is not a commit; pass --all for a full recapture", ref)
	}
	cmd := exec.Command("git", "-C", repo, "diff", "--name-only", "--no-renames", "--relative", ref+"...HEAD")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exit.Stderr))
		}
		if stderr == "" {
			stderr = err.Error()
		}
		return nil, fmt.Errorf("docsexamples: git diff --name-only %s...HEAD: %s", ref, stderr)
	}
	var files []string
	for _, line := range bytes.Split(out, []byte("\n")) {
		name := filepath.ToSlash(string(bytes.TrimSpace(line)))
		if name == "" {
			continue
		}
		files = append(files, name)
	}
	return files, nil
}
