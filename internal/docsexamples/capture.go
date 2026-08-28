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
)

// Envelope is the committed snapshot imported by the docs site.
type Envelope struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Environment EnvironmentRef  `json:"environment"`
	Command     string          `json:"command"`
	Output      json.RawMessage `json:"output"`
	Provenance  Provenance      `json:"provenance"`
}

// EnvironmentRef names the manifest the published command uses.
type EnvironmentRef struct {
	Label    string `json:"label"`
	Manifest string `json:"manifest"`
}

// Options select which examples to consider and when to recapture.
type Options struct {
	IDs       []string
	Resources []string
	All       bool
	SinceRef  string
}

// Result is one example's capture outcome.
type Result struct {
	ID     string
	Action string
}

// Runner starts an environment and returns the wrapped command's stdout.
type Runner func(environment string, proxy bool, argv []string) ([]byte, error)

// Capture recaptures dirty examples and writes snapshots. Unchanged
// provenance is left on disk.
func Capture(repo string, runner Runner, opts Options, stderr io.Writer) ([]Result, error) {
	if stderr == nil {
		stderr = os.Stderr
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("docsexamples: resolve repo: %w", err)
	}
	specs, err := Load(repo)
	if err != nil {
		return nil, err
	}
	selected, err := selectSpecs(specs, opts)
	if err != nil {
		return nil, err
	}

	var changed []string
	if opts.SinceRef != "" {
		changed, err = changedFiles(repo, opts.SinceRef)
		if err != nil {
			return nil, err
		}
	}
	forceAll := opts.All || toolChanged(changed)
	if len(selected) == 0 {
		return nil, fmt.Errorf("docsexamples: no examples matched the selection")
	}

	var results []Result
	for _, spec := range selected {
		prov, err := digestSpec(repo, spec)
		if err != nil {
			return results, err
		}
		dirty, reason, err := needsCapture(repo, spec, prov, forceAll, changed)
		if err != nil {
			return results, err
		}
		if !dirty {
			fmt.Fprintf(stderr, "docs-examples: skip %s (%s)\n", spec.ID, shortDigest(prov.Digest))
			results = append(results, Result{ID: spec.ID, Action: "skip"})
			continue
		}
		fmt.Fprintf(stderr, "docs-examples: capture %s (%s)\n", spec.ID, reason)
		stdout, err := runner(filepath.Join(repo, filepath.FromSlash(spec.Environment)), spec.Proxy, spec.Argv)
		if err != nil {
			return results, fmt.Errorf("docsexamples: %s: %w", spec.ID, err)
		}
		if err := writeEnvelope(repo, spec, stdout, prov); err != nil {
			return results, err
		}
		results = append(results, Result{ID: spec.ID, Action: "capture"})
	}
	return results, nil
}

func selectSpecs(specs []Spec, opts Options) ([]Spec, error) {
	wantIDs := map[string]struct{}{}
	for _, id := range opts.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		wantIDs[id] = struct{}{}
	}
	resources := normalizeResources(opts.Resources)
	if len(wantIDs) == 0 && len(resources) == 0 {
		return specs, nil
	}
	var selected []Spec
	for _, spec := range specs {
		ok := false
		if _, want := wantIDs[spec.ID]; want {
			ok = true
			delete(wantIDs, spec.ID)
		}
		for _, resource := range resources {
			if specTouchesResource(spec, resource) {
				ok = true
				break
			}
		}
		if !ok {
			continue
		}
		selected = append(selected, spec)
	}
	if len(wantIDs) > 0 {
		var missing []string
		for id := range wantIDs {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("docsexamples: unknown example id %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func normalizeResources(names []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, name := range names {
		name = normalizeResource(name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeResource(name string) string {
	name = strings.TrimSpace(strings.Trim(filepath.ToSlash(name), "/"))
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "resources/") {
		return name
	}
	return "resources/" + name
}

func specTouchesResource(spec Spec, resource string) bool {
	for _, root := range spec.Roots {
		if pathTouchesRoot(root, resource) || pathTouchesRoot(resource, root) {
			return true
		}
	}
	return false
}

func needsCapture(repo string, spec Spec, prov Provenance, forceAll bool, changed []string) (bool, string, error) {
	if forceAll {
		return true, "forced", nil
	}
	for _, file := range changed {
		if pathTouchesRoot(file, spec.OutputPath) {
			return true, "snapshot changed", nil
		}
		for _, root := range spec.Roots {
			if pathTouchesRoot(file, root) {
				return true, "input " + root + " changed", nil
			}
		}
	}
	current, err := readProvenance(filepath.Join(repo, filepath.FromSlash(spec.OutputPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return true, "missing snapshot", nil
		}
		return false, "", fmt.Errorf("docsexamples: %s: read snapshot: %w", spec.ID, err)
	}
	if current.Algorithm != Algorithm || current.Digest != prov.Digest {
		return true, "provenance drifted", nil
	}
	return false, "", nil
}

func readProvenance(path string) (Provenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Provenance{}, err
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Provenance{}, err
	}
	return envelope.Provenance, nil
}

func writeEnvelope(repo string, spec Spec, stdout []byte, prov Provenance) error {
	output, err := compactJSON(stdout)
	if err != nil {
		return fmt.Errorf("docsexamples: %s: command stdout is not JSON: %w", spec.ID, err)
	}
	envelope := Envelope{
		ID:          spec.ID,
		Title:       spec.Title,
		Environment: EnvironmentRef{Label: spec.EnvironmentLabel, Manifest: spec.Environment},
		Command:     spec.PublishedCommand,
		Output:      output,
		Provenance:  prov,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("docsexamples: %s: marshal snapshot: %w", spec.ID, err)
	}
	pretty, err := indentJSON(body)
	if err != nil {
		return fmt.Errorf("docsexamples: %s: indent snapshot: %w", spec.ID, err)
	}
	path := filepath.Join(repo, filepath.FromSlash(spec.OutputPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("docsexamples: %s: mkdir: %w", spec.ID, err)
	}
	if err := os.WriteFile(path, pretty, 0o644); err != nil {
		return fmt.Errorf("docsexamples: %s: write snapshot: %w", spec.ID, err)
	}
	return nil
}

func compactJSON(raw []byte) (json.RawMessage, error) {
	docs, err := decodeJSONDocuments(bytes.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(docs) == 1 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, docs[0]); err != nil {
			return nil, err
		}
		return compact.Bytes(), nil
	}
	return json.Marshal(docs)
}

func indentJSON(raw []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, bytes.TrimSpace(raw)); err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// decodeJSONDocuments reads one or more JSON values from stdout. A
// documented multi-service command emits one curl body per line; those
// bodies are concatenated JSON, not a single document.
func decodeJSONDocuments(raw []byte) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var docs []json.RawMessage
	for {
		var doc json.RawMessage
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("empty JSON stdout")
	}
	return docs, nil
}

func toolChanged(files []string) bool {
	for _, file := range files {
		if pathTouchesRoot(file, "cmd/docsexamples") || pathTouchesRoot(file, "internal/docsexamples") {
			return true
		}
	}
	return false
}

func shortDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
