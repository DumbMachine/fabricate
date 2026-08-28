package docsexamples

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/scenario"
)

func TestCatalogFromScenariosCountsTopLevelArrays(t *testing.T) {
	catalog, err := CatalogFromScenarios("gmail", []scenario.Document{
		mustScenario(t, `{
			"$contract":"fabricate.scenario","$contractVersion":1,
			"$id":"gmail.acme-corp.v1","$resource":"gmail","$resourceVersion":"v1",
			"state":{
				"emailAddress":"support@acme.example",
				"labels":[{"id":"INBOX"}],
				"messages":[
					{"id":"msg-0001","labelIds":["INBOX"]},
					{"id":"msg-0012","labelIds":["SPAM","TRASH"]}
				]
			}
		}`),
		mustScenario(t, `{
			"$contract":"fabricate.scenario","$contractVersion":1,
			"$id":"gmail.minimal.v1","$resource":"gmail","$resourceVersion":"v1",
			"state":{"emailAddress":"me@acme.example","labels":[],"messages":[]}
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Integration != "gmail" || len(catalog.Scenarios) != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
	if catalog.Scenarios[0].ID != "gmail.acme-corp.v1" {
		t.Fatalf("sorted ids = %#v", catalog.Scenarios)
	}
	acme := catalog.Scenarios[0]
	if acme.Counts["messages"] != 2 || acme.Counts["labels"] != 1 {
		t.Fatalf("acme counts = %#v", acme.Counts)
	}
	minimal := catalog.Scenarios[1]
	if minimal.Counts["messages"] != 0 || minimal.Counts["labels"] != 0 {
		t.Fatalf("minimal = %+v", minimal)
	}
}

func TestCatalogFromScenariosRequiresID(t *testing.T) {
	_, err := CatalogFromScenarios("gmail", []scenario.Document{{State: json.RawMessage(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "missing $id") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteOperationsWritesScenarioCatalog(t *testing.T) {
	repo := t.TempDir()
	registry := mustRegistry(t, stubResource{
		id:   "gmail",
		name: "Gmail",
		spec: []byte(`{"paths":{"/profile":{"get":{"operationId":"getProfile"}}}}`),
		scenarios: []scenario.Document{
			mustScenario(t, `{
				"$contract":"fabricate.scenario","$contractVersion":1,
				"$id":"gmail.minimal.v1","$resource":"gmail","$resourceVersion":"v1",
				"state":{"labels":[],"messages":[]}
			}`),
		},
	})
	if err := WriteOperations(repo, registry, nil, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(repo, generatedDir, "gmail.scenarios.json")
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"gmail.minimal.v1"`) || !strings.Contains(string(body), `"messages": 0`) {
		t.Fatalf("got %s", body)
	}
	if err := WriteOperations(repo, registry, []string{"gmail"}, ioDiscard()); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(body) {
		t.Fatalf("rewrite = %s", second)
	}
}

func TestLoadRejectsScenariosOutputPath(t *testing.T) {
	repo := writeExampleRepo(t)
	specPath := filepath.Join(repo, "packages/docs-content/resources/_examples/gmail-list-messages.json")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "gmail-list-messages.json", "gmail.scenarios.json", 1))
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "scenario catalog") {
		t.Fatalf("got %v", err)
	}
}

func mustScenario(t *testing.T, raw string) scenario.Document {
	t.Helper()
	doc, err := scenario.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
