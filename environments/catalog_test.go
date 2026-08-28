package environments

import "testing"

func TestCatalogLoadsOfficialEnvironment(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 {
		t.Fatalf("catalog has %d environments", len(names))
	}
	spec, err := Load("acme-support-desk")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Metadata.Name != "acme-support-desk" || len(spec.Services) != 4 {
		t.Fatalf("unexpected environment: %#v", spec)
	}
}
