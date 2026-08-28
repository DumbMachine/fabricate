package cli

import (
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
)

func TestResolveRunSpecLoadsOfficialEnvironment(t *testing.T) {
	spec, err := resolveRunSpec("acme-gmail", "", all.Registry())
	if err != nil {
		t.Fatal(err)
	}
	service := spec.Services["support-mail"]
	if spec.Metadata.Name != "acme-gmail" || service.Resource != "gmail" || service.Scenario != "gmail.acme-corp.v1" {
		t.Fatalf("unexpected environment: %#v", spec)
	}
}

func TestResolveRunSpecBuildsSingleServiceEnvironment(t *testing.T) {
	spec, err := resolveRunSpec("gmail", "gmail.minimal.v1", all.Registry())
	if err != nil {
		t.Fatal(err)
	}
	service := spec.Services["gmail"]
	if spec.Metadata.Name != "gmail" || service.Resource != "gmail" || service.Scenario != "gmail.minimal.v1" {
		t.Fatalf("unexpected environment: %#v", spec)
	}
}

func TestResolveRunSpecRequiresScenarioForService(t *testing.T) {
	_, err := resolveRunSpec("gmail", "", all.Registry())
	if err == nil {
		t.Fatal("expected missing scenario error")
	}
	message := err.Error()
	for _, want := range []string{"requires --scenario", "gmail.acme-corp.v1", "gmail.minimal.v1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
}

func TestResolveRunSpecRejectsUnknownScenarioWithOptions(t *testing.T) {
	_, err := resolveRunSpec("gmail", "missing", all.Registry())
	if err == nil || !strings.Contains(err.Error(), "choose one of: gmail.acme-corp.v1, gmail.minimal.v1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTargetAndCommand(t *testing.T) {
	target, command, err := runTargetAndCommand([]string{"acme-gmail", "go", "test", "./..."}, "")
	if err != nil || target != "acme-gmail" || strings.Join(command, " ") != "go test ./..." {
		t.Fatalf("target=%q command=%q err=%v", target, command, err)
	}

	target, command, err = runTargetAndCommand([]string{"go", "test"}, "./environment.yaml")
	if err != nil || target != "./environment.yaml" || strings.Join(command, " ") != "go test" {
		t.Fatalf("legacy target=%q command=%q err=%v", target, command, err)
	}
}
