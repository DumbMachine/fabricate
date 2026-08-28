package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/resources/all"
)

func TestOfficialEnvironmentViewsIncludeServices(t *testing.T) {
	views, err := officialEnvironmentViews()
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Name == "acme-support-desk" {
			if len(view.Services) != 4 {
				t.Fatalf("services = %d", len(view.Services))
			}
			return
		}
	}
	t.Fatal("acme-support-desk not found")
}

func TestValidateEnvironmentDefinitionChecksScenario(t *testing.T) {
	spec, err := loadEnvironmentDefinition("acme-gmail")
	if err != nil {
		t.Fatal(err)
	}
	spec.Services["support-mail"] = environment.ServiceSpec{Resource: "gmail", Scenario: "missing"}
	if err := validateEnvironmentDefinition(spec, all.Registry()); err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResourceAndScenarioViews(t *testing.T) {
	registry := all.Registry()
	resource, err := resourceViewByID(registry, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resource.Scenarios, ","); got != "gmail.acme-corp.v1,gmail.minimal.v1" {
		t.Fatalf("scenarios = %q", got)
	}
	views, err := scenarioViews(registry, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Resource != "gmail" {
		t.Fatalf("views = %#v", views)
	}
}

func TestLoadAndValidateLocalScenario(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.json")
	raw := []byte(`{
  "$contract":"fabricate.scenario",
  "$contractVersion":1,
  "$id":"gmail.local.v1",
  "$resource":"gmail",
  "$resourceVersion":"v1",
  "state":{"emailAddress":"local@example.com","labels":[],"messages":[]}
}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	doc, resource, err := loadScenarioDefinition(path, all.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID != "gmail.local.v1" {
		t.Fatalf("id = %q", doc.ID)
	}
	if err := resource.Scenarios().Validate(t.Context(), doc); err != nil {
		t.Fatal(err)
	}
}

func TestEntityTableOutput(t *testing.T) {
	var buffer bytes.Buffer
	err := writeServiceList(&buffer, []serviceView{{Name: "mail", Resource: "gmail", Scenario: "gmail.minimal.v1"}}, output.FormatTable)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAME", "RESOURCE", "SCENARIO", "gmail.minimal.v1"} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("output %q does not contain %q", buffer.String(), want)
		}
	}
}

func TestEnvironmentInspectOmitsEmptyProxy(t *testing.T) {
	spec, err := loadEnvironmentDefinition("acme-gmail")
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := writeEnvironmentInspect(&buffer, spec, output.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), `"proxy"`) {
		t.Fatalf("unexpected empty proxy: %s", buffer.String())
	}
}
