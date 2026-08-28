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

func TestNormalizeNullableTypes(t *testing.T) {
	doc := map[string]any{
		"type": []any{"string", "null"},
		"properties": map[string]any{
			"active_until": map[string]any{"type": []any{"string", "null"}},
		},
	}
	normalizeNullableTypes(doc)
	if doc["type"] != "string" || doc["nullable"] != true {
		t.Fatalf("root = %#v", doc)
	}
	prop := doc["properties"].(map[string]any)["active_until"].(map[string]any)
	if prop["type"] != "string" || prop["nullable"] != true {
		t.Fatalf("prop = %#v", prop)
	}
}

func TestAssignOperationIDs(t *testing.T) {
	doc := map[string]any{
		"paths": map[string]any{
			"/v2/objects/{object}/records": map[string]any{
				"get":  map[string]any{"summary": "Get"},
				"post": map[string]any{"operationId": "createRecord"},
			},
		},
	}
	assignOperationIDs(doc)
	ops := doc["paths"].(map[string]any)["/v2/objects/{object}/records"].(map[string]any)
	if ops["get"].(map[string]any)["operationId"] != "get_v2_objects_object_records" {
		t.Fatalf("assigned id = %v", ops["get"].(map[string]any)["operationId"])
	}
	if ops["post"].(map[string]any)["operationId"] != "createRecord" {
		t.Fatal("existing operationId should be kept")
	}
}

func TestDropMatchingPaths(t *testing.T) {
	doc := map[string]any{
		"paths": map[string]any{
			"/repos/{owner}/{repo}/issues":          map[string]any{"get": map[string]any{"operationId": "issues"}},
			"/repos/{owner}/{repo}/git/blobs":       map[string]any{"get": map[string]any{"operationId": "blobs"}},
			"/repos/{owner}/{repo}/contents/{path}": map[string]any{"get": map[string]any{"operationId": "contents"}},
		},
	}
	dropMatchingPaths(doc, []string{"/git/", "/contents"})
	paths := doc["paths"].(map[string]any)
	if _, ok := paths["/repos/{owner}/{repo}/issues"]; !ok {
		t.Fatal("issues should be kept")
	}
	if _, ok := paths["/repos/{owner}/{repo}/git/blobs"]; ok {
		t.Fatal("git blobs should be dropped")
	}
	if _, ok := paths["/repos/{owner}/{repo}/contents/{path}"]; ok {
		t.Fatal("contents should be dropped")
	}
}

func TestKeepOperations(t *testing.T) {
	doc := map[string]any{
		"paths": map[string]any{
			"/emails": map[string]any{
				"get":  map[string]any{"operationId": "emails/list"},
				"post": map[string]any{"operationId": "emails/send"},
			},
			"/webhooks": map[string]any{
				"post": map[string]any{"operationId": "webhook"},
			},
		},
	}
	keepOperations(doc, []string{"emails/send", "emails/list"})
	paths := doc["paths"].(map[string]any)
	if _, ok := paths["/webhooks"]; ok {
		t.Fatal("unwanted path should be dropped")
	}
	emails := paths["/emails"].(map[string]any)
	if emails["get"].(map[string]any)["operationId"] != "emails/list" {
		t.Fatal("kept get")
	}
	if emails["post"].(map[string]any)["operationId"] != "emails/send" {
		t.Fatal("kept post")
	}
}

func TestFillEmptyJSONResponses(t *testing.T) {
	doc := map[string]any{
		"paths": map[string]any{
			"/me/": map[string]any{
				"get": map[string]any{
					"responses": map[string]any{
						"200": map[string]any{
							"content": map[string]any{
								"application/json": map[string]any{"example": map[string]any{"id": "1"}},
							},
						},
					},
				},
			},
		},
	}
	fillEmptyJSONResponses(doc)
	schema := doc["paths"].(map[string]any)["/me/"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("schema = %#v", schema)
	}
}

func TestEnsureParameterSchemas(t *testing.T) {
	doc := map[string]any{
		"parameters": []any{
			map[string]any{"name": "id", "in": "path", "type": "string"},
			map[string]any{"name": "q", "in": "query"},
		},
		"properties": map[string]any{
			"in": map[string]any{"type": "string", "description": "JSON pointer into the body"},
		},
	}
	ensureParameterSchemas(doc)
	pathParam := doc["parameters"].([]any)[0].(map[string]any)
	schema, _ := pathParam["schema"].(map[string]any)
	if schema["type"] != "string" {
		t.Fatalf("swagger-style path param schema = %#v", pathParam["schema"])
	}
	queryParam := doc["parameters"].([]any)[1].(map[string]any)
	schema, _ = queryParam["schema"].(map[string]any)
	if schema["type"] != "string" {
		t.Fatalf("missing schema default = %#v", queryParam["schema"])
	}
	prop := doc["properties"].(map[string]any)["in"].(map[string]any)
	if _, ok := prop["schema"]; ok {
		t.Fatalf("schema property named in must not be treated as a parameter: %#v", prop)
	}
}

func TestDropSwaggerBodyParameters(t *testing.T) {
	doc := map[string]any{
		"parameters": []any{
			map[string]any{"in": "path", "name": "id"},
			map[string]any{"in": "body", "name": "body"},
		},
	}
	dropSwaggerBodyParameters(doc)
	params := doc["parameters"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["in"] != "path" {
		t.Fatalf("params = %#v", params)
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
