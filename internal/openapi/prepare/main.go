package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var ins stringList
	out := flag.String("out", "", "prepared JSON path for oapi-codegen")
	stripExamples := flag.Bool("strip-examples", false, "drop example keys that vendor documents often leave invalid")
	assignIDs := flag.Bool("assign-operation-ids", false, "fill missing operationId values from method+path")
	openapiVersion := flag.String("openapi-version", "", "rewrite the openapi version (use 3.0.3 for OAS 3.1 vendor docs)")
	var keepIDs stringList
	var dropPaths stringList
	flag.Var(&keepIDs, "keep-operation-ids", "drop operations whose operationId is not in this list; repeat as needed")
	flag.Var(&dropPaths, "drop-path", "drop path templates that contain this substring; repeat as needed")
	flag.Var(&ins, "in", "source OpenAPI file (YAML or JSON); repeat to merge")
	flag.Parse()
	if len(ins) == 0 || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: prepare -in openapi.yaml [-in more.json] -out generated/openapi.prepared.json")
		os.Exit(2)
	}
	merged, err := loadAndMerge(ins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: %v\n", err)
		os.Exit(1)
	}
	typed, ok := jsonable(merged).(map[string]any)
	if !ok {
		fmt.Fprintln(os.Stderr, "openapi prepare: document must be an object")
		os.Exit(1)
	}
	merged = typed
	if *openapiVersion != "" {
		merged["openapi"] = *openapiVersion
		normalizeNullableTypes(merged)
	}
	if *assignIDs {
		assignOperationIDs(merged)
	}
	if len(keepIDs) > 0 {
		keepOperations(merged, keepIDs)
		delete(merged, "webhooks")
	}
	if len(dropPaths) > 0 {
		dropMatchingPaths(merged, dropPaths)
	}
	_, isSwagger := merged["swagger"]
	// Full OAS 3 catalogs (HubSpot, Asana, Intercom) already have parameter
	// schemas. Walking every map looking for {"in": ...} mutates unrelated
	// schema properties and churns generated bindings. Only the subsetted
	// vendor docs and Swagger 2 catalogs need this repair.
	if len(keepIDs) > 0 || isSwagger {
		ensureParameterSchemas(merged)
	}
	if len(keepIDs) > 0 {
		fillEmptyJSONResponses(merged)
	}
	if isSwagger {
		dropSwaggerBodyParameters(merged)
	}
	stripCodegenIncompatibilities(merged, *stripExamples)
	encoded, err := json.Marshal(merged)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: write %s: %v\n", *out, err)
		os.Exit(1)
	}
}

func loadAndMerge(paths []string) (map[string]any, error) {
	var merged map[string]any
	for i, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		typed, ok := doc.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: document must be an object", path)
		}
		if i == 0 {
			merged = typed
			continue
		}
		if err := mergeOpenAPI(merged, typed, path); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func mergeOpenAPI(dst, src map[string]any, source string) error {
	if err := mergeNamedMap(dst, src, "paths", source); err != nil {
		return err
	}
	dstComponents, _ := dst["components"].(map[string]any)
	srcComponents, _ := src["components"].(map[string]any)
	if srcComponents == nil {
		return nil
	}
	if dstComponents == nil {
		dst["components"] = srcComponents
		return nil
	}
	for _, section := range []string{"schemas", "responses", "parameters", "requestBodies", "headers", "securitySchemes", "examples", "links", "callbacks"} {
		if err := mergeNamedMap(dstComponents, srcComponents, section, source); err != nil {
			return err
		}
	}
	return nil
}

func mergeNamedMap(dst, src map[string]any, key, source string) error {
	srcMap, _ := src[key].(map[string]any)
	if len(srcMap) == 0 {
		return nil
	}
	dstMap, _ := dst[key].(map[string]any)
	if dstMap == nil {
		dst[key] = srcMap
		return nil
	}
	for name, value := range srcMap {
		if _, exists := dstMap[name]; exists {
			continue
		}
		dstMap[name] = value
	}
	_ = source
	return nil
}

func jsonable(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = jsonable(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = jsonable(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = jsonable(child)
		}
		return out
	default:
		return value
	}
}

func assignOperationIDs(doc map[string]any) {
	paths, _ := doc["paths"].(map[string]any)
	for path, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, op := range ops {
			switch strings.ToLower(method) {
			case "get", "post", "put", "patch", "delete", "head", "options":
			default:
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := opMap["operationId"].(string); strings.TrimSpace(id) != "" {
				continue
			}
			opMap["operationId"] = operationIDFrom(method, path)
		}
	}
}

func dropMatchingPaths(doc map[string]any, needles []string) {
	paths, _ := doc["paths"].(map[string]any)
	for path := range paths {
		for _, needle := range needles {
			if strings.Contains(path, needle) {
				delete(paths, path)
				break
			}
		}
	}
}

func keepOperations(doc map[string]any, ids []string) {
	wanted := map[string]struct{}{}
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	paths, _ := doc["paths"].(map[string]any)
	for path, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, op := range ops {
			if !isHTTPMethod(method) {
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				delete(ops, method)
				continue
			}
			id, _ := opMap["operationId"].(string)
			if _, keep := wanted[id]; !keep {
				delete(ops, method)
			}
		}
		if !pathHasOperation(ops) {
			delete(paths, path)
		}
	}
}

func isHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
}

func pathHasOperation(ops map[string]any) bool {
	for method := range ops {
		if isHTTPMethod(method) {
			return true
		}
	}
	return false
}

func fillEmptyJSONResponses(doc map[string]any) {
	paths, _ := doc["paths"].(map[string]any)
	for _, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, op := range ops {
			if !isHTTPMethod(method) {
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				continue
			}
			responses, _ := opMap["responses"].(map[string]any)
			for _, resp := range responses {
				respMap, ok := resp.(map[string]any)
				if !ok {
					continue
				}
				content, _ := respMap["content"].(map[string]any)
				jsonContent, _ := content["application/json"].(map[string]any)
				if jsonContent == nil {
					continue
				}
				schema, _ := jsonContent["schema"].(map[string]any)
				if len(schema) == 0 {
					jsonContent["schema"] = map[string]any{"type": "object", "additionalProperties": true}
				}
			}
		}
	}
}

func dropSwaggerBodyParameters(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if params, ok := typed["parameters"].([]any); ok {
			filtered := make([]any, 0, len(params))
			for _, param := range params {
				item, ok := param.(map[string]any)
				if !ok {
					filtered = append(filtered, param)
					continue
				}
				in, _ := item["in"].(string)
				if strings.ToLower(in) == "body" {
					continue
				}
				filtered = append(filtered, param)
			}
			typed["parameters"] = filtered
		}
		for _, child := range typed {
			dropSwaggerBodyParameters(child)
		}
	case []any:
		for _, child := range typed {
			dropSwaggerBodyParameters(child)
		}
	}
}

func operationIDFrom(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() == 0 || b.String()[b.Len()-1] == '_' {
				continue
			}
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func ensureParameterSchemas(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if name, ok := typed["name"].(string); ok && name != "" {
			if in, ok := typed["in"].(string); ok && isParameterIn(in) {
				if _, hasSchema := typed["schema"]; !hasSchema {
					if _, hasContent := typed["content"]; !hasContent {
						if _, hasType := typed["type"]; hasType {
							schema := map[string]any{}
							for _, key := range []string{"type", "format", "items", "enum", "default"} {
								if child, ok := typed[key]; ok {
									schema[key] = child
								}
							}
							if len(schema) > 0 {
								typed["schema"] = schema
							}
						} else {
							typed["schema"] = map[string]any{"type": "string"}
						}
					}
				}
			}
		}
		for _, child := range typed {
			ensureParameterSchemas(child)
		}
	case []any:
		for _, child := range typed {
			ensureParameterSchemas(child)
		}
	}
}

func isParameterIn(in string) bool {
	switch strings.ToLower(in) {
	case "query", "header", "path", "cookie", "body":
		return true
	default:
		return false
	}
}

func normalizeNullableTypes(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if types, ok := typed["type"].([]any); ok {
			var nonNull string
			null := false
			for _, item := range types {
				name, _ := item.(string)
				if name == "null" {
					null = true
					continue
				}
				if nonNull == "" {
					nonNull = name
				} else {
					nonNull = ""
					break
				}
			}
			if null && nonNull != "" {
				typed["type"] = nonNull
				typed["nullable"] = true
			}
		}
		for _, child := range typed {
			normalizeNullableTypes(child)
		}
	case []any:
		for _, child := range typed {
			normalizeNullableTypes(child)
		}
	}
}

func stripCodegenIncompatibilities(value any, stripExamples bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "readOnly" || key == "writeOnly" || (len(key) > 1 && key[0] == 'x' && key[1] == '-') {
				delete(typed, key)
				continue
			}
			if key == "example" && (stripExamples || child == nil) {
				delete(typed, key)
				continue
			}
			stripCodegenIncompatibilities(child, stripExamples)
		}
	case []any:
		for _, child := range typed {
			stripCodegenIncompatibilities(child, stripExamples)
		}
	}
}
