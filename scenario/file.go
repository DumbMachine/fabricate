package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LooksLikePath reports whether ref is a filesystem path rather than a
// catalog scenario ID such as gmail.acme-corp.v1.
func LooksLikePath(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	return strings.ContainsRune(ref, '/') || strings.ContainsRune(ref, os.PathSeparator) || strings.HasSuffix(ref, ".json")
}

// LoadFile reads and parses a scenario document from path.
func LoadFile(path string) (Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("scenario: read %s: %w", path, err)
	}
	doc, err := Parse(raw)
	if err != nil {
		return Document{}, fmt.Errorf("scenario: %s: %w", path, err)
	}
	return doc, nil
}

// ResolvePath joins a possibly-relative scenario path with baseDir. Empty
// baseDir means the process working directory.
func ResolvePath(ref, baseDir string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("scenario: path is empty")
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref), nil
	}
	if baseDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("scenario: working directory: %w", err)
		}
		baseDir = cwd
	}
	return filepath.Clean(filepath.Join(baseDir, ref)), nil
}
