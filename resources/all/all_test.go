package all

import "testing"

func TestRegistryContainsOfficialResources(t *testing.T) {
	registry := Registry()
	for _, id := range []string{"asana", "gmail", "hubspot", "intercom"} {
		if _, ok := registry.Get(id); !ok {
			t.Fatalf("%s is not registered", id)
		}
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 4 || descriptors[0].ID != "asana" || descriptors[1].ID != "gmail" || descriptors[2].ID != "hubspot" || descriptors[3].ID != "intercom" {
		t.Fatalf("descriptors = %+v", descriptors)
	}
}
