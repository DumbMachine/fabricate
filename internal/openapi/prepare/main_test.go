package main

import (
	"encoding/json"
	"testing"
)

func TestStripCodegenIncompatibilities(t *testing.T) {
	doc := map[string]any{
		"info": map[string]any{"title": "Example", "x-internal": true},
		"components": map[string]any{
			"schemas": map[string]any{
				"Task": map[string]any{
					"type":     "object",
					"readOnly": true,
					"properties": map[string]any{
						"gid": map[string]any{"type": "string", "readOnly": true, "writeOnly": false},
					},
				},
			},
		},
	}
	stripCodegenIncompatibilities(doc, false)
	info := doc["info"].(map[string]any)
	if _, ok := info["x-internal"]; ok {
		t.Fatal("x- keys should be stripped")
	}
	task := doc["components"].(map[string]any)["schemas"].(map[string]any)["Task"].(map[string]any)
	if _, ok := task["readOnly"]; ok {
		t.Fatal("readOnly should be stripped")
	}
	gid := task["properties"].(map[string]any)["gid"].(map[string]any)
	if _, ok := gid["readOnly"]; ok {
		t.Fatal("nested readOnly should be stripped")
	}
	if _, ok := gid["writeOnly"]; ok {
		t.Fatal("writeOnly should be stripped")
	}
	if got := info["title"]; got != "Example" {
		t.Fatalf("title = %v", got)
	}
}

func TestStripExamples(t *testing.T) {
	doc := map[string]any{"schema": map[string]any{"type": "object", "example": "not-an-object"}}
	stripCodegenIncompatibilities(doc, true)
	if _, ok := doc["schema"].(map[string]any)["example"]; ok {
		t.Fatal("examples should be stripped")
	}
}

func TestJSONAbleConvertsYAMLMaps(t *testing.T) {
	converted := jsonable(map[string]any{
		"paths": map[any]any{
			"/tasks": []any{
				map[any]any{"gid": "1"},
			},
		},
	}).(map[string]any)
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"paths":{"/tasks":[{"gid":"1"}]}}` {
		t.Fatalf("jsonable = %s", raw)
	}
}

func TestMergeOpenAPIUnionsPaths(t *testing.T) {
	dst := map[string]any{
		"paths": map[string]any{"/contacts": map[string]any{"get": "a"}},
		"components": map[string]any{
			"schemas": map[string]any{"Shared": map[string]any{"type": "object"}},
		},
	}
	src := map[string]any{
		"paths": map[string]any{"/deals": map[string]any{"get": "b"}},
		"components": map[string]any{
			"schemas": map[string]any{
				"Shared": map[string]any{"type": "string"},
				"Deal":   map[string]any{"type": "object"},
			},
		},
	}
	if err := mergeOpenAPI(dst, src, "deals.json"); err != nil {
		t.Fatal(err)
	}
	paths := dst["paths"].(map[string]any)
	if _, ok := paths["/contacts"]; !ok {
		t.Fatal("contacts path missing")
	}
	if _, ok := paths["/deals"]; !ok {
		t.Fatal("deals path missing")
	}
	schemas := dst["components"].(map[string]any)["schemas"].(map[string]any)
	if schemas["Shared"].(map[string]any)["type"] != "object" {
		t.Fatal("first schema should win on duplicate names")
	}
	if _, ok := schemas["Deal"]; !ok {
		t.Fatal("deal schema missing")
	}
}
