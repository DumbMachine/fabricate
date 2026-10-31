package console

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestAgentContractProjectionUsesCompiledSchemas(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	t.Setenv("FAB_CONSOLE_TOKEN", testOperator)
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := durableManager(t, t.TempDir())
	h := m.Handler()
	w := controlJSON(t, h, testOperator, "GET", "/api/resources/asana/contract?view=operations&limit=2", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var catalog struct {
		Operations []map[string]any `json:"operations"`
		Total      int              `json:"total"`
		HasMore    bool             `json:"hasMore"`
		BasePath   string           `json:"basePath"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Operations) != 2 || catalog.Total <= 2 || !catalog.HasMore || catalog.BasePath != "/api/1.0" {
		t.Fatal("invalid operation page")
	}
	full := controlJSON(t, h, testOperator, "GET", "/api/resources/asana/contract", nil)
	compact := controlJSON(t, h, testOperator, "GET", "/api/resources/asana/contract?operations=GetTask,UpdateTask&view=compact", nil)
	if compact.Code != 200 || compact.Body.Len() >= full.Body.Len() {
		t.Fatal("selected contract was not projected", compact.Code)
	}
	var result struct {
		OpenAPI json.RawMessage `json:"openapi"`
	}
	if err := json.Unmarshal(compact.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(result.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Validate(context.Background()); err != nil {
		t.Fatal("projected OpenAPI is invalid", err)
	}
	for _, query := range []string{"view=operations&limit=101", "view=operations&offset=-1", "view=unknown", "view=compact&operations=unsupported"} {
		w = controlJSON(t, h, testOperator, "GET", "/api/resources/asana/contract?"+query, nil)
		if w.Code < 400 {
			t.Fatal("unsupported contract request accepted", query)
		}
	}
}
