package main

import "testing"

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
	stripCodegenIncompatibilities(doc)
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
