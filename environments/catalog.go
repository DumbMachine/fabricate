// Package environments embeds the official environment catalog shipped with fab.
package environments

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/environment"
)

//go:embed *.yaml
var catalog embed.FS

// Names returns the official environment names in stable order.
func Names() ([]string, error) {
	entries, err := catalog.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("environment catalog: list: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// Resolve loads a manifest path or an official catalog name.
func Resolve(target string) (environment.Spec, error) {
	if target == "" {
		return environment.Spec{}, fmt.Errorf("environment is required")
	}
	if isManifestPath(target) {
		return environment.Load(target)
	}
	names, err := Names()
	if err != nil {
		return environment.Spec{}, err
	}
	for _, name := range names {
		if target == name {
			return Load(target)
		}
	}
	return environment.Spec{}, fmt.Errorf("unknown environment %q; choose one of: %s", target, strings.Join(names, ", "))
}

func isManifestPath(target string) bool {
	if target == "/dev/stdin" || strings.ContainsRune(target, os.PathSeparator) {
		return true
	}
	return strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml")
}

// Load returns an official environment by name.
func Load(name string) (environment.Spec, error) {
	raw, err := catalog.ReadFile(name + ".yaml")
	if err != nil {
		return environment.Spec{}, fmt.Errorf("environment catalog: unknown environment %q", name)
	}
	spec, err := environment.Parse(raw)
	if err != nil {
		return environment.Spec{}, fmt.Errorf("environment catalog: %s: %w", name, err)
	}
	return spec, nil
}
