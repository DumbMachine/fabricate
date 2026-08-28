package docsexamples

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Algorithm is the provenance hash version. Bump it when the walk
// rules change so existing snapshots become dirty.
const Algorithm = "docs-examples-root-v1"

// RootDigest is the content address of one declared input root.
type RootDigest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Provenance records what was hashed to produce a snapshot. It is the
// public trail for why a docs example was recaptured.
type Provenance struct {
	Algorithm string       `json:"algorithm"`
	Digest    string       `json:"digest"`
	Roots     []RootDigest `json:"roots"`
}

func digestSpec(repo string, spec Spec) (Provenance, error) {
	var roots []RootDigest
	for _, rel := range spec.Roots {
		sum, err := digestRoot(repo, rel)
		if err != nil {
			return Provenance{}, fmt.Errorf("docsexamples: %s: hash %s: %w", spec.ID, rel, err)
		}
		roots = append(roots, RootDigest{Path: rel, Digest: sum})
	}
	return Provenance{Algorithm: Algorithm, Digest: combineDigests(roots), Roots: roots}, nil
}

func digestRoot(repo, rel string) (string, error) {
	abs := filepath.Join(repo, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink; refuse to hash a moving target", rel)
	}
	if info.Mode().IsRegular() {
		return hashFile(abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a file or directory", rel)
	}
	return hashTree(abs, rel)
}

func hashTree(abs, rel string) (string, error) {
	var entries []string
	err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if skipDir(name) && path != abs {
				return fs.SkipDir
			}
			return nil
		}
		if skipFile(name) {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			child, err := filepath.Rel(abs, path)
			if err != nil {
				return err
			}
			return fmt.Errorf("%s/%s is a symlink; refuse to hash a moving target", rel, filepath.ToSlash(child))
		}
		if !d.Type().IsRegular() {
			return nil
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		child, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(child)+"\x00"+sum+"\n")
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("%s contains no hashable files", rel)
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, entry := range entries {
		io.WriteString(h, entry)
	}
	return prefixed(h.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return prefixed(h.Sum(nil)), nil
}

func combineDigests(roots []RootDigest) string {
	h := sha256.New()
	io.WriteString(h, Algorithm+"\n")
	for _, root := range roots {
		io.WriteString(h, root.Path+"\x00"+root.Digest+"\n")
	}
	return prefixed(h.Sum(nil))
}

func prefixed(sum []byte) string {
	return "sha256:" + hex.EncodeToString(sum)
}

func skipDir(name string) bool {
	switch name {
	case "conformance", "testdata", ".git":
		return true
	default:
		return false
	}
}

func skipFile(name string) bool {
	if name == ".DS_Store" || name == "openapi.prepared.json" {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasSuffix(name, "_test.go") || strings.HasSuffix(lower, ".md")
}

func requireRelative(rel string) error {
	if rel == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("%q is absolute", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean != rel {
		return fmt.Errorf("%q is not a clean slash path", rel)
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("%q escapes the repository", rel)
	}
	return nil
}

func validateRoots(repo string, roots []string, what string) error {
	for _, rel := range roots {
		if err := requireRelative(rel); err != nil {
			return fmt.Errorf("docsexamples: %s: %w", what, err)
		}
		if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("docsexamples: %s: %s: %w", what, rel, err)
		}
	}
	return nil
}

func pathTouchesRoot(changed, root string) bool {
	changed = filepath.ToSlash(changed)
	root = filepath.ToSlash(root)
	return changed == root || strings.HasPrefix(changed, root+"/")
}
