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
