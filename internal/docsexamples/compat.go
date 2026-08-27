package docsexamples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var resourceID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func InstallCompatibility(repo, from, resource string, stderr io.Writer) error {
	if stderr == nil {
		stderr = os.Stderr
	}
	resource = strings.TrimSpace(resource)
	if !resourceID.MatchString(resource) {
		return fmt.Errorf("docsexamples: invalid resource %q", resource)
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("docsexamples: resolve repo: %w", err)
	}
	incoming, err := os.ReadFile(from)
	if err != nil {
		return fmt.Errorf("docsexamples: read compatibility report: %w", err)
	}
	report, err := parseCompatibility(incoming)
	if err != nil {
		return fmt.Errorf("docsexamples: parse compatibility report: %w", err)
	}
	got, ok := report["integration"].(string)
	if !ok || got == "" {
		return fmt.Errorf("docsexamples: compatibility report is missing integration")
	}
	if got != resource {
		return fmt.Errorf("docsexamples: report integration %q does not match resource %s", got, resource)
	}

	dest := filepath.Join(repo, generatedDir, resource+".compatibility.json")
	body, err := indentJSON(incoming)
	if err != nil {
		return fmt.Errorf("docsexamples: indent compatibility report: %w", err)
	}
	existing, err := os.ReadFile(dest)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("docsexamples: read existing compatibility report: %w", err)
	}
	if err == nil {
		same, err := compatibilityPayloadEqual(existing, incoming)
		if err != nil {
			return err
		}
		if same {
			fmt.Fprintf(stderr, "docs-compat: skip %s (unchanged)\n", resource)
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("docsexamples: mkdir generated: %w", err)
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("docsexamples: write compatibility report: %w", err)
	}
	fmt.Fprintf(stderr, "docs-compat: write %s\n", resource)
	return nil
}

func parseCompatibility(raw []byte) (map[string]any, error) {
	var report map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &report); err != nil {
		return nil, err
	}
	if report == nil {
		return nil, fmt.Errorf("report is not a JSON object")
	}
	return report, nil
}

func compatibilityPayloadEqual(a, b []byte) (bool, error) {
	left, err := stripCompatibilityVolatile(a)
	if err != nil {
		return false, err
	}
	right, err := stripCompatibilityVolatile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}

func stripCompatibilityVolatile(raw []byte) ([]byte, error) {
	report, err := parseCompatibility(raw)
	if err != nil {
		return nil, err
	}
	delete(report, "testedAt")
	delete(report, "testedCommit")
	return json.Marshal(report)
}
