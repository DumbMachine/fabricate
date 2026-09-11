package environment

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestDumpAndEvalGmailTrash(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	registry := all.Registry()
	goldDir := t.TempDir()
	passDir := t.TempDir()
	failDir := t.TempDir()

	gold := startGmail(t)
	trashMessage(t, gold, "msg-0028")
	goldSnap, err := gold.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(goldDir, goldSnap); err != nil {
		t.Fatal(err)
	}
	_ = gold.Close(context.Background())

	pass := startGmail(t)
	trashMessage(t, pass, "msg-0028")
	passSnap, err := pass.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(passDir, passSnap); err != nil {
		t.Fatal(err)
	}
	_ = pass.Close(context.Background())

	loaded, err := ReadSnapshot(goldDir)
	if err != nil {
		t.Fatal(err)
	}
	diff := DiffSnapshots(loaded, passSnap)
	if !diff.Match {
		t.Fatalf("oracle replay should match gold: %#v", diff)
	}

	fail := startGmail(t)
	failSnap, err := fail.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(failDir, failSnap); err != nil {
		t.Fatal(err)
	}
	_ = fail.Close(context.Background())
	miss := DiffSnapshots(loaded, failSnap)
	if miss.Match {
		t.Fatal("baseline dump unexpectedly matched gold")
	}
	found := false
	for _, service := range miss.Services {
		for _, pointer := range service.Paths {
			if strings.Contains(pointer, "msg-0028") || strings.Contains(pointer, "labelIds") || strings.Contains(pointer, "messages") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected a message/label diff, got %#v", miss)
	}

	doc := loaded.Services["support-mail"].Document
	resource, ok := registry.Get("gmail")
	if !ok {
		t.Fatal("gmail resource missing")
	}
	if err := resource.Scenarios().Validate(context.Background(), doc); err != nil {
		t.Fatalf("gold dump is not a valid scenario: %v", err)
	}
	canonical, _ := scenario.CanonicalJSON(doc)
	if scenario.Digest(canonical) != loaded.Services["support-mail"].Digest {
		t.Fatal("stored digest does not match canonical JSON")
	}
}

func TestStartLoadsLocalScenarioFile(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "resources", "gmail", "scenarios", "minimal.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	scenarioPath := filepath.Join(dir, "inbox.json")
	if err := os.WriteFile(scenarioPath, src, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata: {name: local-mail}
services:
  mail: {resource: gmail, scenario: ./inbox.json}
`)
	yamlPath := filepath.Join(dir, "environment.yaml")
	if err := os.WriteFile(yamlPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	snap, err := runtime.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services["mail"].Scenario != "gmail.minimal.v1" {
		t.Fatalf("dumped scenario = %s", snap.Services["mail"].Scenario)
	}
}

func startGmail(t *testing.T) *Runtime {
	t.Helper()
	spec, err := Parse([]byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata: {name: acme-gmail}
services:
  support-mail: {resource: gmail, scenario: gmail.acme-corp.v1}
`))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func trashMessage(t *testing.T, runtime *Runtime, id string) {
	t.Helper()
	service := runtime.Services["support-mail"]
	req, err := http.NewRequest(http.MethodPost, service.URL+"/gmail/v1/users/me/messages/"+id+"/trash", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+service.Token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trash = %d", response.StatusCode)
	}
}
