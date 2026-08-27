// Package docsexamples writes committed resource-page snapshots: documented
// command output and conformance compatibility reports. The docs build never
// starts environments.
package docsexamples

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/environment"
)

const (
	examplesDir  = "packages/docs-content/resources/_examples"
	generatedDir = "packages/docs-content/resources/_generated"
	catalogName  = "catalog.json"
)

type catalog struct {
	SharedRoots []string `json:"sharedRoots"`
}

// Spec is one documented command. Roots used for provenance are the spec
// file, the environment manifest, each resource referenced by that
// manifest, catalog shared roots, and any ExtraRoots.
type Spec struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Environment      string   `json:"environment"`
	EnvironmentLabel string   `json:"environmentLabel"`
	Proxy            bool     `json:"proxy"`
	OutputPath       string   `json:"outputPath"`
	PublishedCommand string   `json:"publishedCommand"`
	Argv             []string `json:"argv"`
	ExtraRoots       []string `json:"extraRoots,omitempty"`
	Path             string   `json:"-"`
	Roots            []string `json:"-"`
}

func Load(repo string) ([]Spec, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("docsexamples: resolve repo: %w", err)
	}
	cat, err := loadCatalog(filepath.Join(repo, examplesDir, catalogName))
	if err != nil {
		return nil, err
	}
	if err := validateRoots(repo, cat.SharedRoots, "catalog sharedRoots"); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(repo, examplesDir))
	if err != nil {
		return nil, fmt.Errorf("docsexamples: read %s: %w", examplesDir, err)
	}
	var specs []Spec
	seenID := map[string]string{}
	seenOut := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == catalogName || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(examplesDir, entry.Name()))
		spec, err := loadSpec(repo, rel, cat)
		if err != nil {
			return nil, err
		}
		if previous, ok := seenID[spec.ID]; ok {
			return nil, fmt.Errorf("docsexamples: duplicate id %q (%s and %s)", spec.ID, previous, spec.Path)
		}
		if previous, ok := seenOut[spec.OutputPath]; ok {
			return nil, fmt.Errorf("docsexamples: duplicate outputPath %q (%s and %s)", spec.OutputPath, previous, spec.Path)
		}
		seenID[spec.ID] = spec.Path
		seenOut[spec.OutputPath] = spec.Path
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("docsexamples: no example specs in %s", examplesDir)
	}
	return specs, nil
}

func loadCatalog(path string) (catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return catalog{}, fmt.Errorf("docsexamples: read catalog: %w", err)
	}
	var cat catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return catalog{}, fmt.Errorf("docsexamples: parse catalog: %w", err)
	}
	if len(cat.SharedRoots) == 0 {
		return catalog{}, fmt.Errorf("docsexamples: catalog sharedRoots must not be empty")
	}
	return cat, nil
}

func loadSpec(repo, rel string, cat catalog) (Spec, error) {
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		return Spec{}, fmt.Errorf("docsexamples: read %s: %w", rel, err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("docsexamples: parse %s: %w", rel, err)
	}
	spec.Path = rel
	if spec.ID == "" {
		return Spec{}, fmt.Errorf("docsexamples: %s: id is required", rel)
	}
	if spec.Title == "" {
		return Spec{}, fmt.Errorf("docsexamples: %s: title is required", rel)
	}
	if spec.Environment == "" {
		return Spec{}, fmt.Errorf("docsexamples: %s: environment is required", rel)
	}
	if spec.EnvironmentLabel == "" {
		return Spec{}, fmt.Errorf("docsexamples: %s: environmentLabel is required", rel)
	}
	if spec.PublishedCommand == "" {
		return Spec{}, fmt.Errorf("docsexamples: %s: publishedCommand is required", rel)
	}
	if len(spec.Argv) == 0 {
		return Spec{}, fmt.Errorf("docsexamples: %s: argv must not be empty", rel)
	}
	if err := requireRelative(spec.Environment); err != nil {
		return Spec{}, fmt.Errorf("docsexamples: %s: environment: %w", rel, err)
	}
	if err := requireRelative(spec.OutputPath); err != nil {
		return Spec{}, fmt.Errorf("docsexamples: %s: outputPath: %w", rel, err)
	}
	if !strings.HasPrefix(spec.OutputPath, generatedDir+"/") {
		return Spec{}, fmt.Errorf("docsexamples: %s: outputPath must be under %s/", rel, generatedDir)
	}
	if strings.HasSuffix(spec.OutputPath, ".compatibility.json") {
		return Spec{}, fmt.Errorf("docsexamples: %s: outputPath %s is a compatibility report; CommandOutput snapshots cannot use that name", rel, spec.OutputPath)
	}
	if err := validateRoots(repo, spec.ExtraRoots, rel+" extraRoots"); err != nil {
		return Spec{}, err
	}
	resources, err := environmentResources(repo, spec.Environment)
	if err != nil {
		return Spec{}, fmt.Errorf("docsexamples: %s: %w", rel, err)
	}
	spec.Roots = assembleRoots(rel, spec.Environment, resources, cat.SharedRoots, spec.ExtraRoots)
	if err := validateRoots(repo, spec.Roots, rel+" roots"); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func environmentResources(repo, rel string) ([]string, error) {
	env, err := environment.Load(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var resources []string
	for _, service := range env.Services {
		if _, ok := seen[service.Resource]; ok {
			continue
		}
		seen[service.Resource] = struct{}{}
		resources = append(resources, service.Resource)
	}
	sort.Strings(resources)
	return resources, nil
}

func assembleRoots(specPath, environment string, resources, shared, extra []string) []string {
	roots := []string{specPath, environment}
	for _, resource := range resources {
		roots = append(roots, filepath.ToSlash(filepath.Join("resources", resource)))
	}
	roots = append(roots, shared...)
	roots = append(roots, extra...)
	return uniqueSorted(roots)
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = filepath.ToSlash(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
