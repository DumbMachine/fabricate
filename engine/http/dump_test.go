package httpengine

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dumbmachine/fabricate/resources/gmail"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestServiceDumpMatchesLoadedScenario(t *testing.T) {
	resource := gmail.NewResource()
	doc, err := resource.Scenario("gmail.minimal.v1")
	if err != nil {
		t.Fatal(err)
	}
	service, err := StartService(context.Background(), "mail", filepath.Join(t.TempDir(), "mail"), resource, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	dumped, err := service.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before, err := scenario.CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	after, err := scenario.CanonicalJSON(dumped)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("dump changed the baseline:\nbefore=%s\nafter=%s", before, after)
	}

	req, _ := http.NewRequest(http.MethodGet, service.URL+"/gmail/v1/users/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+service.Token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
