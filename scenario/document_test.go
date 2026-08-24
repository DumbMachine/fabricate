package scenario

import (
	"bytes"
	"testing"
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
