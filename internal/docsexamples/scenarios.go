package docsexamples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

const scenariosSuffix = ".scenarios.json"

// ScenarioFacts is the committed docs snapshot of one catalog scenario's
// top-level seeded collections.
type ScenarioFacts struct {
	ID       string         `json:"id"`
	Counts   map[string]int `json:"counts"`
	Listable map[string]int `json:"listable,omitempty"`
}

// ScenarioCatalog is the committed docs snapshot of a resource's catalog
// scenarios. Counts come from top-level JSON arrays in scenario state.
type ScenarioCatalog struct {
	Integration string          `json:"integration"`
	Scenarios   []ScenarioFacts `json:"scenarios"`
}

// CatalogFromScenarios extracts collection counts from a resource's catalog
// scenarios. For Gmail-shaped messages, listable is the default
// users.messages.list size (spam and trash omitted).
func CatalogFromScenarios(id string, docs []scenario.Document) (ScenarioCatalog, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ScenarioCatalog{}, fmt.Errorf("docsexamples: scenario catalog integration is required")
	}
	entries := make([]ScenarioFacts, 0, len(docs))
	seen := map[string]struct{}{}
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" {
			return ScenarioCatalog{}, fmt.Errorf("docsexamples: %s scenario is missing $id", id)
		}
		if _, dup := seen[doc.ID]; dup {
			return ScenarioCatalog{}, fmt.Errorf("docsexamples: %s duplicate scenario %q", id, doc.ID)
		}
		seen[doc.ID] = struct{}{}
		counts, listable, err := scenarioCounts(doc.State)
		if err != nil {
			return ScenarioCatalog{}, fmt.Errorf("docsexamples: %s scenario %s: %w", id, doc.ID, err)
		}
		entries = append(entries, ScenarioFacts{ID: doc.ID, Counts: counts, Listable: listable})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return ScenarioCatalog{Integration: id, Scenarios: entries}, nil
}

func writeScenarioCatalog(repo string, resource httpresource.Resource, stderr io.Writer) error {
	if resource == nil {
		return fmt.Errorf("docsexamples: resource is required")
	}
	ids, err := resource.ScenarioIDs()
	if err != nil {
		return fmt.Errorf("docsexamples: list %s scenarios: %w", resource.Descriptor().ID, err)
	}
	docs := make([]scenario.Document, 0, len(ids))
	for _, id := range ids {
		doc, err := resource.Scenario(id)
		if err != nil {
			return fmt.Errorf("docsexamples: load %s scenario %s: %w", resource.Descriptor().ID, id, err)
		}
		docs = append(docs, doc)
	}
	catalog, err := CatalogFromScenarios(resource.Descriptor().ID, docs)
	if err != nil {
		return err
	}
	return writeJSONCatalog(repo, catalog.Integration, scenariosSuffix, catalog, stderr, func() string {
		return fmt.Sprintf("docs-operations: write %s (%d scenarios)", catalog.Integration, len(catalog.Scenarios))
	})
}

func scenarioCounts(state json.RawMessage) (map[string]int, map[string]int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(state, &obj); err != nil {
		return nil, nil, fmt.Errorf("state: %w", err)
	}
	counts := map[string]int{}
	var listable map[string]int
	for key, raw := range obj {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		counts[key] = len(items)
		if key != "messages" {
			continue
		}
		listed, ok := listableMessages(items)
		if !ok || listed == len(items) {
			continue
		}
		listable = map[string]int{"messages": listed}
	}
	return counts, listable, nil
}

func listableMessages(items []json.RawMessage) (int, bool) {
	listed := 0
	shaped := false
	for _, raw := range items {
		var message struct {
			LabelIDs []string `json:"labelIds"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return 0, false
		}
		shaped = true
		hidden := false
		for _, label := range message.LabelIDs {
			if label == "SPAM" || label == "TRASH" {
				hidden = true
				break
			}
		}
		if !hidden {
			listed++
		}
	}
	return listed, shaped
}

func writeJSONCatalog(repo, integration, suffix string, catalog any, stderr io.Writer, wrote func() string) error {
	if integration == "" {
		return fmt.Errorf("docsexamples: catalog is missing integration")
	}
	if !resourceID.MatchString(integration) {
		return fmt.Errorf("docsexamples: invalid resource %q", integration)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("docsexamples: marshal %s catalog: %w", integration, err)
	}
	body, err := indentJSON(raw)
	if err != nil {
		return fmt.Errorf("docsexamples: indent %s catalog: %w", integration, err)
	}
	dest := filepath.Join(repo, generatedDir, integration+suffix)
	existing, err := os.ReadFile(dest)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("docsexamples: read existing catalog: %w", err)
	}
	if err == nil && bytes.Equal(existing, body) {
		fmt.Fprintf(stderr, "docs-operations: skip %s %s (unchanged)\n", integration, strings.TrimPrefix(suffix, "."))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("docsexamples: mkdir generated: %w", err)
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("docsexamples: write catalog: %w", err)
	}
	fmt.Fprintln(stderr, wrote())
	return nil
}
