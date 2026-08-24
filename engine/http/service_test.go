package httpengine

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dumbmachine/fabricate/resources/gmail"
)

func TestStartServicePublishesGmailListener(t *testing.T) {
	resource := gmail.NewResource()
	doc, err := resource.Scenario("gmail.acme-corp.v1")
	if err != nil {
		t.Fatal(err)
	}
	service, err := StartService(context.Background(), "mail", filepath.Join(t.TempDir(), "mail"), resource, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	req, _ := http.NewRequest(http.MethodGet, service.URL+"/gmail/v1/users/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+service.Token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
