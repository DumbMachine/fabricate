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
)

const operationsSuffix = ".operations.json"

var httpMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

// Operation is one compiled HTTP operation from a resource's embedded spec.
type Operation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operationId"`
	Summary     string `json:"summary,omitempty"`
}

// OperationCatalog is the committed docs snapshot of a resource's advertised
// HTTP surface. Empty path stubs from include-operation-ids are omitted.
type OperationCatalog struct {
	Integration string      `json:"integration"`
	API         string      `json:"api"`
	Operations  []Operation `json:"operations"`
}

// CatalogFromOpenAPI extracts implemented operations from a compiled OpenAPI
// document. Paths with no HTTP methods are ignored.
func CatalogFromOpenAPI(id, api string, openapiJSON []byte) (OperationCatalog, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OperationCatalog{}, fmt.Errorf("docsexamples: operation catalog integration is required")
	}
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(openapiJSON), &spec); err != nil {
		return OperationCatalog{}, fmt.Errorf("docsexamples: parse OpenAPI for %s: %w", id, err)
	}
	var ops []Operation
	for path, raw := range spec.Paths {
		item := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &item); err != nil {
			return OperationCatalog{}, fmt.Errorf("docsexamples: parse path %s for %s: %w", path, id, err)
		}
		for _, method := range httpMethods {
			body, ok := item[method]
			if !ok {
				continue
			}
			var op struct {
				OperationID string `json:"operationId"`
				Summary     string `json:"summary"`
			}
			if err := json.Unmarshal(body, &op); err != nil {
				return OperationCatalog{}, fmt.Errorf("docsexamples: parse %s %s for %s: %w", method, path, id, err)
			}
			if strings.TrimSpace(op.OperationID) == "" {
				return OperationCatalog{}, fmt.Errorf("docsexamples: %s %s %s is missing operationId", id, strings.ToUpper(method), path)
			}
			ops = append(ops, Operation{
				Method:      strings.ToUpper(method),
				Path:        path,
				OperationID: op.OperationID,
				Summary:     strings.TrimSpace(op.Summary),
			})
		}
	}
	if len(ops) == 0 {
		return OperationCatalog{}, fmt.Errorf("docsexamples: %s OpenAPI has no operations", id)
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return methodRank(ops[i].Method) < methodRank(ops[j].Method)
	})
	api = strings.TrimSpace(api)
	if api == "" {
		api = id
	}
	return OperationCatalog{Integration: id, API: api, Operations: ops}, nil
}

// WriteOperations dumps operation catalogs for official resources into
// packages/docs-content/resources/_generated/<id>.operations.json.
func WriteOperations(repo string, registry *httpresource.Registry, names []string, stderr io.Writer) error {
	if stderr == nil {
		stderr = os.Stderr
	}
	if registry == nil {
		return fmt.Errorf("docsexamples: resource registry is required")
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("docsexamples: resolve repo: %w", err)
	}
	ids, err := selectResourceIDs(registry, names)
	if err != nil {
		return err
	}
	for _, id := range ids {
		resource, ok := registry.Get(id)
		if !ok {
			return fmt.Errorf("docsexamples: unknown resource %q", id)
		}
		descriptor := resource.Descriptor()
		catalog, err := CatalogFromOpenAPI(descriptor.ID, strings.TrimSpace(descriptor.DisplayName+" "+descriptor.Version), resource.Contract().OpenAPIJSON)
		if err != nil {
			return err
		}
		if err := writeOperationCatalog(repo, catalog, stderr); err != nil {
			return err
		}
	}
	return nil
}

func selectResourceIDs(registry *httpresource.Registry, names []string) ([]string, error) {
	all := make([]string, 0, len(registry.Descriptors()))
	for _, descriptor := range registry.Descriptors() {
		all = append(all, descriptor.ID)
	}
	if len(names) == 0 {
		return all, nil
	}
	var ids []string
	seen := map[string]struct{}{}
	for _, name := range names {
		id := filepath.Base(normalizeResource(name))
		if id == "" || id == "." {
			continue
		}
		if _, ok := registry.Get(id); !ok {
			return nil, fmt.Errorf("docsexamples: unknown resource %q", strings.TrimSpace(name))
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("docsexamples: no resources selected")
	}
	return ids, nil
}

func writeOperationCatalog(repo string, catalog OperationCatalog, stderr io.Writer) error {
	if catalog.Integration == "" {
		return fmt.Errorf("docsexamples: operation catalog is missing integration")
	}
	if !resourceID.MatchString(catalog.Integration) {
		return fmt.Errorf("docsexamples: invalid resource %q", catalog.Integration)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("docsexamples: marshal %s operations: %w", catalog.Integration, err)
	}
	body, err := indentJSON(raw)
	if err != nil {
		return fmt.Errorf("docsexamples: indent %s operations: %w", catalog.Integration, err)
	}
	dest := filepath.Join(repo, generatedDir, catalog.Integration+operationsSuffix)
	existing, err := os.ReadFile(dest)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("docsexamples: read existing operations catalog: %w", err)
	}
	if err == nil && bytes.Equal(existing, body) {
		fmt.Fprintf(stderr, "docs-operations: skip %s (unchanged)\n", catalog.Integration)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("docsexamples: mkdir generated: %w", err)
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("docsexamples: write operations catalog: %w", err)
	}
	fmt.Fprintf(stderr, "docs-operations: write %s (%d operations)\n", catalog.Integration, len(catalog.Operations))
	return nil
}

func methodRank(method string) int {
	for i, candidate := range httpMethods {
		if strings.EqualFold(candidate, method) {
			return i
		}
	}
	return len(httpMethods)
}
