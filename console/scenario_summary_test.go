package console

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dumbmachine/fabricate/resources/all"
)

func TestScenarioNamesAndDescriptions(t *testing.T) {
	catalog, err := New(all.Registry()).Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scenarios) == 0 {
		t.Fatal("catalog has no scenarios")
	}
	byID := map[string]CatalogScenario{}
	for _, scenario := range catalog.Scenarios {
		if scenario.Name == "" || scenario.Description == "" {
			t.Fatalf("scenario %s missing name or description: %#v", scenario.ID, scenario)
		}
		if utf8.RuneCountInString(scenario.Description) > scenarioDescriptionLimit {
			t.Fatalf("scenario %s description is %d runes", scenario.ID, utf8.RuneCountInString(scenario.Description))
		}
		byID[scenario.ID] = scenario
	}

	minimal := byID["gmail.minimal.v1"]
	if minimal.Name != "Minimal" || !strings.Contains(minimal.Description, "Empty seed") || !strings.Contains(minimal.Description, "me@acme.example") {
		t.Fatalf("minimal: %#v", minimal)
	}
	acme := byID["gmail.acme-corp.v1"]
	if acme.Name != "Acme Corp" || !strings.Contains(acme.Description, "28 messages") || !strings.Contains(acme.Description, "support@acme.example") {
		t.Fatalf("acme: %#v", acme)
	}
	zones := byID["cloudflare.acme-zones.v1"]
	if zones.Name != "Acme Zones" || !strings.Contains(zones.Description, "213 dns records") {
		t.Fatalf("zones: %#v", zones)
	}
	goods := byID["shopify.acme-goods.v1"]
	if goods.Name != "Acme Goods" || goods.Description == "" {
		t.Fatalf("goods: %#v", goods)
	}
}
