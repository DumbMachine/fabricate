package all

import "testing"

func TestRegistryContainsGmailOnce(t *testing.T) {
	registry := Registry()
	if _, ok := registry.Get("gmail"); !ok {
		t.Fatal("gmail is not registered")
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != "gmail" {
		t.Fatalf("descriptors = %+v", descriptors)
	}
}
