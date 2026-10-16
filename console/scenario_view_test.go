package console

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dumbmachine/fabricate/resources/all"
)

func TestScenarioViewSummarizesSeedData(t *testing.T) {
	manager := New(all.Registry())

	minimal, err := manager.Scenario("gmail.minimal.v1")
	if err != nil {
		t.Fatal(err)
	}
	if minimal.DisplayName != "Gmail" || minimal.Resource != "gmail" || scalarValue(minimal, "emailAddress") != "me@acme.example" {
		t.Fatalf("minimal view: %#v", minimal)
	}
	if !containsHost(minimal.ProviderHosts, "gmail.googleapis.com") {
		t.Fatalf("hosts: %#v", minimal.ProviderHosts)
	}
	labels := collectionByKey(t, minimal, "labels")
	messages := collectionByKey(t, minimal, "messages")
	if labels.Total != 0 || messages.Total != 0 || labels.Shown != 0 || len(labels.Rows) != 0 {
		t.Fatalf("empty collections: labels %#v messages %#v", labels, messages)
	}

	acme, err := manager.Scenario("gmail.acme-corp.v1")
	if err != nil {
		t.Fatal(err)
	}
	if scalarValue(acme, "emailAddress") != "support@acme.example" {
		t.Fatalf("acme scalar: %#v", acme.Scalars)
	}
	acmeLabels := collectionByKey(t, acme, "labels")
	if acmeLabels.Total != 6 || acmeLabels.Shown != 6 || acmeLabels.Rows[0]["name"] != "Billing" || acmeLabels.Rows[0]["id"] != "Label_billing" {
		t.Fatalf("labels: %#v", acmeLabels)
	}
	acmeMessages := collectionByKey(t, acme, "messages")
	if acmeMessages.Total != 28 || acmeMessages.Shown != scenarioRowLimit || len(acmeMessages.Rows) != scenarioRowLimit {
		t.Fatalf("messages cap: total %d shown %d rows %d", acmeMessages.Total, acmeMessages.Shown, len(acmeMessages.Rows))
	}
	first := acmeMessages.Rows[0]
	if first["id"] != "msg-0001" || first["subject"] != "Charged twice for August invoice INV-4812" {
		t.Fatalf("first message: %#v", first)
	}
	if first["internalDate"] != "1787469300000" {
		t.Fatalf("internalDate: %q", first["internalDate"])
	}
	if utf8.RuneCountInString(first["body"]) > scenarioCellLimit || !strings.Contains(first["body"], "…") {
		t.Fatalf("body preview: %q", first["body"])
	}

	zones, err := manager.Scenario("cloudflare.acme-zones.v1")
	if err != nil {
		t.Fatal(err)
	}
	dns := collectionByKey(t, zones, "dns_records")
	if dns.Total != 213 || dns.Shown != scenarioRowLimit || len(dns.Columns) > scenarioColumnLimit {
		t.Fatalf("dns cap: %#v columns %d", dns.Total, len(dns.Columns))
	}

	for _, id := range []string{"gmail.missing.v1", "not-a-scenario", ""} {
		_, err := manager.Scenario(id)
		var unknown unknownScenarioError
		if !errors.As(err, &unknown) {
			t.Fatalf("scenario %q error: %v", id, err)
		}
	}
}

func TestScenarioRouteAndResourceIcons(t *testing.T) {
	manager := New(all.Registry())
	server := httptest.NewServer(manager.Handler())
	t.Cleanup(server.Close)

	response := get(t, server.URL+"/api/scenarios/gmail.acme-corp.v1")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, readBody(t, response))
	}
	var view ScenarioView
	decodeJSON(t, response, &view)
	if view.ID != "gmail.acme-corp.v1" || len(view.Collections) == 0 {
		t.Fatalf("view: %#v", view)
	}

	missing := get(t, server.URL+"/api/scenarios/gmail.missing.v1")
	missingBody := readBody(t, missing)
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status %d: %s", missing.StatusCode, missingBody)
	}

	for _, path := range []string{"/integrations/gmail.svg", "/integrations/slack.svg", "/integrations/shiprocket.png"} {
		icon := get(t, server.URL+path)
		body := readBody(t, icon)
		if icon.StatusCode != http.StatusOK || strings.Contains(body, "<html") {
			t.Fatalf("%s status %d: %s", path, icon.StatusCode, body[:min(80, len(body))])
		}
		if strings.HasSuffix(path, ".svg") && !strings.Contains(body, "<svg") {
			t.Fatalf("%s is not an svg", path)
		}
	}

	page := get(t, server.URL+"/environments")
	pageBody := readBody(t, page)
	if page.StatusCode != http.StatusOK || !strings.Contains(pageBody, "Fabricate") {
		t.Fatalf("environments page status %d", page.StatusCode)
	}
}

func collectionByKey(t *testing.T, view ScenarioView, key string) ScenarioCollection {
	t.Helper()
	for _, collection := range view.Collections {
		if collection.Key == key {
			return collection
		}
	}
	t.Fatalf("missing collection %s in %#v", key, view.Collections)
	return ScenarioCollection{}
}

func scalarValue(view ScenarioView, key string) string {
	for _, scalar := range view.Scalars {
		if scalar.Key == key {
			return scalar.Value
		}
	}
	return ""
}
