package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func main() {
	in := flag.String("in", "", "source OpenAPI file (YAML or JSON)")
	out := flag.String("out", "", "prepared JSON path for oapi-codegen")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: prepare -in openapi.yaml -out generated/openapi.prepared.json")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: read %s: %v\n", *in, err)
		os.Exit(1)
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: parse %s: %v\n", *in, err)
		os.Exit(1)
	}
	stripCodegenIncompatibilities(doc)
	encoded, err := json.Marshal(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "openapi prepare: write %s: %v\n", *out, err)
		os.Exit(1)
	}
}

func stripCodegenIncompatibilities(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key := range typed {
			if key == "readOnly" || key == "writeOnly" || (len(key) > 1 && key[0] == 'x' && key[1] == '-') {
				delete(typed, key)
			}
		}
		for _, child := range typed {
			stripCodegenIncompatibilities(child)
		}
	case []any:
		for _, child := range typed {
			stripCodegenIncompatibilities(child)
		}
	}
}
