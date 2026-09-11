package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dumbmachine/fabricate/scenario"
)

// Snapshot is a canonical dump of every service's live scenario document.
type Snapshot struct {
	Environment string                     `json:"environment"`
	Services    map[string]ServiceSnapshot `json:"services"`
}

type ServiceSnapshot struct {
	Name     string            `json:"name"`
	Resource string            `json:"resource"`
	Scenario string            `json:"scenario"`
	Digest   string            `json:"digest"`
	Document scenario.Document `json:"document"`
}

type Diff struct {
	Match    bool          `json:"match"`
	Services []ServiceDiff `json:"services"`
}

type ServiceDiff struct {
	Name            string   `json:"name"`
	Match           bool     `json:"match"`
	ExpectedDigest  string   `json:"expected_digest,omitempty"`
	ActualDigest    string   `json:"actual_digest,omitempty"`
	MissingExpected bool     `json:"missing_expected,omitempty"`
	MissingActual   bool     `json:"missing_actual,omitempty"`
	Paths           []string `json:"paths,omitempty"`
}

func (r *Runtime) Dump(ctx context.Context) (Snapshot, error) {
	if r == nil {
		return Snapshot{}, fmt.Errorf("environment: runtime is required")
	}
	names := make([]string, 0, len(r.Services))
	for name := range r.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	out := Snapshot{
		Environment: r.Spec.Metadata.Name,
		Services:    make(map[string]ServiceSnapshot, len(names)),
	}
	for _, name := range names {
		service := r.Services[name]
		doc, err := service.Dump(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("environment: dump %s: %w", name, err)
		}
		canonical, err := scenario.CanonicalJSON(doc)
		if err != nil {
			return Snapshot{}, fmt.Errorf("environment: canonicalize %s: %w", name, err)
		}
		out.Services[name] = ServiceSnapshot{
			Name:     name,
			Resource: doc.Resource,
			Scenario: doc.ID,
			Digest:   scenario.Digest(canonical),
			Document: doc,
		}
	}
	return out, nil
}

func WriteSnapshot(dir string, snap Snapshot) error {
	if dir == "" {
		return fmt.Errorf("environment: dump directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("environment: create dump directory: %w", err)
	}
	names := make([]string, 0, len(snap.Services))
	for name := range snap.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest := struct {
		Environment string            `json:"environment"`
		Services    []manifestService `json:"services"`
	}{Environment: snap.Environment}
	for _, name := range names {
		entry := snap.Services[name]
		canonical, err := scenario.CanonicalJSON(entry.Document)
		if err != nil {
			return fmt.Errorf("environment: write %s: %w", name, err)
		}
		file := name + ".json"
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, canonical, 0o600); err != nil {
			return fmt.Errorf("environment: write %s: %w", path, err)
		}
		manifest.Services = append(manifest.Services, manifestService{
			Name: name, Resource: entry.Resource, Scenario: entry.Scenario, Digest: entry.Digest, File: file,
		})
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("environment: marshal dump manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("environment: write dump manifest: %w", err)
	}
	return nil
}

func ReadSnapshot(dir string) (Snapshot, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("environment: read dump manifest: %w", err)
	}
	var manifest struct {
		Environment string            `json:"environment"`
		Services    []manifestService `json:"services"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Snapshot{}, fmt.Errorf("environment: parse dump manifest: %w", err)
	}
	if manifest.Environment == "" || len(manifest.Services) == 0 {
		return Snapshot{}, fmt.Errorf("environment: dump manifest is missing environment or services")
	}
	snap := Snapshot{Environment: manifest.Environment, Services: make(map[string]ServiceSnapshot, len(manifest.Services))}
	for _, entry := range manifest.Services {
		if entry.Name == "" || entry.File == "" {
			return Snapshot{}, fmt.Errorf("environment: dump manifest entry is missing name or file")
		}
		doc, err := scenario.LoadFile(filepath.Join(dir, entry.File))
		if err != nil {
			return Snapshot{}, err
		}
		canonical, err := scenario.CanonicalJSON(doc)
		if err != nil {
			return Snapshot{}, fmt.Errorf("environment: canonicalize dump %s: %w", entry.Name, err)
		}
		digest := scenario.Digest(canonical)
		if entry.Digest != "" && entry.Digest != digest {
			return Snapshot{}, fmt.Errorf("environment: dump %s digest %s does not match file %s", entry.Name, entry.Digest, digest)
		}
		snap.Services[entry.Name] = ServiceSnapshot{
			Name: entry.Name, Resource: doc.Resource, Scenario: doc.ID, Digest: digest, Document: doc,
		}
	}
	return snap, nil
}

func DiffSnapshots(expected, actual Snapshot) Diff {
	names := map[string]struct{}{}
	for name := range expected.Services {
		names[name] = struct{}{}
	}
	for name := range actual.Services {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	diff := Diff{Match: true}
	for _, name := range ordered {
		want, haveExpected := expected.Services[name]
		got, haveActual := actual.Services[name]
		entry := ServiceDiff{Name: name}
		switch {
		case !haveExpected:
			entry.MissingExpected = true
			entry.ActualDigest = got.Digest
		case !haveActual:
			entry.MissingActual = true
			entry.ExpectedDigest = want.Digest
		case want.Digest == got.Digest:
			entry.Match = true
			entry.ExpectedDigest = want.Digest
			entry.ActualDigest = got.Digest
		default:
			entry.ExpectedDigest = want.Digest
			entry.ActualDigest = got.Digest
			entry.Paths = jsonPointerDiff(want.Document.State, got.Document.State)
		}
		if !entry.Match {
			diff.Match = false
		}
		diff.Services = append(diff.Services, entry)
	}
	return diff
}

type manifestService struct {
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Scenario string `json:"scenario"`
	Digest   string `json:"digest"`
	File     string `json:"file"`
}

func jsonPointerDiff(expected, actual json.RawMessage) []string {
	var want, got any
	if err := json.Unmarshal(expected, &want); err != nil {
		return []string{"/"}
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		return []string{"/"}
	}
	var paths []string
	collectJSONDiff("", want, got, &paths)
	if len(paths) == 0 {
		return []string{"/"}
	}
	sort.Strings(paths)
	const limit = 32
	if len(paths) > limit {
		return append(paths[:limit], fmt.Sprintf("... %d more", len(paths)-limit))
	}
	return paths
}

func collectJSONDiff(path string, expected, actual any, paths *[]string) {
	if len(*paths) >= 64 {
		return
	}
	pointer := jsonPointer(path)
	if jsonEqual(expected, actual) {
		return
	}
	if expectedMap, expectedOK := asJSONObject(expected); expectedOK {
		actualMap, actualOK := asJSONObject(actual)
		if !actualOK {
			*paths = append(*paths, pointer)
			return
		}
		keys := map[string]struct{}{}
		for key := range expectedMap {
			keys[key] = struct{}{}
		}
		for key := range actualMap {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			collectJSONDiff(path+"/"+escapeJSONPointer(key), expectedMap[key], actualMap[key], paths)
		}
		return
	}
	if expectedList, expectedOK := asJSONArray(expected); expectedOK {
		actualList, actualOK := asJSONArray(actual)
		if !actualOK {
			*paths = append(*paths, pointer)
			return
		}
		n := len(expectedList)
		if len(actualList) > n {
			n = len(actualList)
		}
		if n == 0 && len(expectedList) != len(actualList) {
			*paths = append(*paths, pointer)
			return
		}
		for i := 0; i < n; i++ {
			var left, right any
			if i < len(expectedList) {
				left = expectedList[i]
			}
			if i < len(actualList) {
				right = actualList[i]
			}
			collectJSONDiff(fmt.Sprintf("%s/%d", path, i), left, right, paths)
		}
		return
	}
	*paths = append(*paths, pointer)
}

func jsonPointer(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func escapeJSONPointer(key string) string {
	replaced := make([]byte, 0, len(key)+8)
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '~':
			replaced = append(replaced, '~', '0')
		case '/':
			replaced = append(replaced, '~', '1')
		default:
			replaced = append(replaced, key[i])
		}
	}
	return string(replaced)
}

func asJSONObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func asJSONArray(value any) ([]any, bool) {
	list, ok := value.([]any)
	return list, ok
}

func jsonEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}
