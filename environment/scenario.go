package environment

import (
	"fmt"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

// ResolveScenario loads a built-in catalog ID or a JSON file. File paths are
// resolved against spec.SourceDir (the environment manifest directory) when
// set, otherwise the process working directory.
func ResolveScenario(resource httpresource.Resource, spec Spec, ref string) (scenario.Document, error) {
	if resource == nil {
		return scenario.Document{}, fmt.Errorf("environment: resource is required")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return scenario.Document{}, fmt.Errorf("environment: scenario is required")
	}
	if scenario.LooksLikePath(ref) {
		path, err := scenario.ResolvePath(ref, spec.SourceDir)
		if err != nil {
			return scenario.Document{}, err
		}
		doc, err := scenario.LoadFile(path)
		if err != nil {
			return scenario.Document{}, err
		}
		if doc.Resource != resource.Descriptor().ID {
			return scenario.Document{}, fmt.Errorf("environment: scenario %q belongs to resource %q, not %q", doc.ID, doc.Resource, resource.Descriptor().ID)
		}
		return doc, nil
	}
	doc, err := resource.Scenario(ref)
	if err != nil {
		return scenario.Document{}, err
	}
	if doc.Resource != resource.Descriptor().ID {
		return scenario.Document{}, fmt.Errorf("environment: scenario %q belongs to resource %q, not %q", doc.ID, doc.Resource, resource.Descriptor().ID)
	}
	return doc, nil
}
