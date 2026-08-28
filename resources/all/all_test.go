package all

import "testing"

func TestRegistryContainsOfficialResources(t *testing.T) {
	registry := Registry()
	want := []string{"asana", "attio", "close", "cloudflare", "gmail", "hubspot", "intercom", "mailchimp", "mailgun", "pipedrive", "resend", "sendgrid", "stripe"}
	for _, id := range want {
		if _, ok := registry.Get(id); !ok {
			t.Fatalf("%s is not registered", id)
		}
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != len(want) {
		t.Fatalf("descriptor count = %d, want %d (%+v)", len(descriptors), len(want), descriptors)
	}
	for i, id := range want {
		if descriptors[i].ID != id {
			t.Fatalf("descriptors[%d] = %s, want %s", i, descriptors[i].ID, id)
		}
	}
}
