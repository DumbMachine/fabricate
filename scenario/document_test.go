package scenario

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseIsStrictAndCanonical(t *testing.T) {
	a := []byte(`{"$contract":"fabricate.scenario","$contractVersion":1,"$id":"gmail.minimal.v1","$resource":"gmail","$resourceVersion":"v1","state":{"z":1,"a":2}}`)
	b := []byte(`{ "state": {"a":2,"z":1}, "$resourceVersion":"v1", "$resource":"gmail", "$id":"gmail.minimal.v1", "$contractVersion":1, "$contract":"fabricate.scenario" }`)

	adoc, err := Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	bdoc, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	ac, _ := CanonicalJSON(adoc)
	bc, _ := CanonicalJSON(bdoc)
	if !bytes.Equal(ac, bc) || Digest(ac) != Digest(bc) {
		t.Fatalf("canonical forms differ:\n%s\n%s", ac, bc)
	}
}

func TestParseRejectsUnknownEnvelopeFields(t *testing.T) {
	_, err := Parse([]byte(`{"$contract":"fabricate.scenario","$contractVersion":1,"$id":"x","$resource":"gmail","$resourceVersion":"v1","state":{},"runtimePort":1234}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestEmbeddedLoadsSortedDocuments(t *testing.T) {
	files := fstest.MapFS{
		"scenarios/b.json": {Data: []byte(`{"$contract":"fabricate.scenario","$contractVersion":1,"$id":"gmail.b.v1","$resource":"gmail","$resourceVersion":"v1","state":{"n":1}}`)},
		"scenarios/a.json": {Data: []byte(`{"$contract":"fabricate.scenario","$contractVersion":1,"$id":"gmail.a.v1","$resource":"gmail","$resourceVersion":"v1","state":{"n":1}}`)},
	}
	docs, err := Embedded(files)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(IDs(docs), ","); got != "gmail.a.v1,gmail.b.v1" {
		t.Fatalf("ids = %q", got)
	}
	if doc, ok := Lookup(docs, "gmail.b.v1"); !ok || doc.ID != "gmail.b.v1" {
		t.Fatalf("lookup = %#v ok=%t", doc, ok)
	}
	if _, err := LookupEmbedded(files, "missing", "gmail"); err == nil || !strings.Contains(err.Error(), `gmail: unknown scenario "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
