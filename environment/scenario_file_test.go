package environment_test

import (
	"encoding/json"
	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/resources/all"
	"os"
	"path/filepath"
	"testing"
)

func TestScenarioFileRoundTripAndIdentity(t *testing.T) {
	for _, id := range []string{"linkedin", "x"} {
		t.Run(id, func(t *testing.T) {
			r, _ := all.Registry().Get(id)
			doc, e := r.Scenario(id + ".acme-social.v1")
			if e != nil {
				t.Fatal(e)
			}
			root := t.TempDir()
			raw, _ := json.Marshal(doc)
			if e = os.WriteFile(filepath.Join(root, "state.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			manifest := `{"apiVersion":"fabricate.dev/v1alpha1","kind":"Environment","metadata":{"name":"round-trip"},"services":{"provider":{"resource":"` + id + `","scenario":"` + doc.ID + `","scenario_file":"state.json"}}}`
			path := filepath.Join(root, "environment.yaml")
			os.WriteFile(path, []byte(manifest), 0600)
			spec, e := environment.Load(path)
			if e != nil {
				t.Fatal(e)
			}
			s := spec.Services["provider"]
			if !filepath.IsAbs(s.ScenarioFile) {
				t.Fatal("relative scenario path not resolved")
			}
			loaded, e := environment.ResolveScenario(r, s)
			if e != nil || loaded.ID != doc.ID {
				t.Fatalf("%+v %v", loaded, e)
			}
			s.Resource = "unsupported"
			if _, e = environment.ResolveScenario(r, s); e == nil {
				t.Fatal("mismatched resource accepted")
			}
		})
	}
}
