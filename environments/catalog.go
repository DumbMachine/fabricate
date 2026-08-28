// Package environments embeds the official environment catalog shipped with fab.
package environments

import (
	"embed"
	"fmt"
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
