// Package scenario defines Fabricate's immutable, resource-specific baseline
// documents. Runtime state is deliberately kept outside this package.
package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
)

const Contract = "fabricate.scenario"

type Document struct {
	Contract        string          `json:"$contract"`
	ContractVersion int             `json:"$contractVersion"`
	ID              string          `json:"$id"`
	Resource        string          `json:"$resource"`
	ResourceVersion string          `json:"$resourceVersion"`
	State           json.RawMessage `json:"state"`
}

type Metadata struct {
	ID              string
	Resource        string
	ResourceVersion string
}

func Parse(raw []byte) (Document, error) {
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("scenario: decode: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return Document{}, err
	}
	if err := doc.ValidateEnvelope(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (d Document) ValidateEnvelope() error {
	switch {
	case d.Contract != Contract:
		return fmt.Errorf("scenario: $contract must be %q", Contract)
	case d.ContractVersion != 1:
		return fmt.Errorf("scenario: unsupported $contractVersion %d", d.ContractVersion)
	case d.ID == "":
		return fmt.Errorf("scenario: $id is required")
	case d.Resource == "":
		return fmt.Errorf("scenario: $resource is required")
	case d.ResourceVersion == "":
		return fmt.Errorf("scenario: $resourceVersion is required")
	case len(d.State) == 0 || bytes.Equal(bytes.TrimSpace(d.State), []byte("null")):
		return fmt.Errorf("scenario: state is required")
	}
	var state any
	if err := json.Unmarshal(d.State, &state); err != nil {
		return fmt.Errorf("scenario: state: %w", err)
	}
	if _, ok := state.(map[string]any); !ok {
		return fmt.Errorf("scenario: state must be an object")
	}
	return nil
}

func (d Document) Metadata() Metadata {
	return Metadata{ID: d.ID, Resource: d.Resource, ResourceVersion: d.ResourceVersion}
}

// Embedded returns the scenario documents stored in an embedded scenarios directory.
func Embedded(files fs.FS) ([]Document, error) {
	entries, err := fs.ReadDir(files, "scenarios")
	if err != nil {
		return nil, fmt.Errorf("scenario: list embedded scenarios: %w", err)
	}
	docs := make([]Document, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(files, "scenarios/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("scenario: read embedded scenario %s: %w", entry.Name(), err)
		}
		doc, err := Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("scenario: parse embedded scenario %s: %w", entry.Name(), err)
		}
		if _, dup := seen[doc.ID]; dup {
			return nil, fmt.Errorf("scenario: duplicate embedded scenario %q", doc.ID)
		}
		seen[doc.ID] = struct{}{}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return docs, nil
}

// IDs returns document IDs in the given order.
func IDs(docs []Document) []string {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ID
	}
	return ids
}

// Lookup returns the document with the given ID.
func Lookup(docs []Document, id string) (Document, bool) {
	for _, doc := range docs {
		if doc.ID == id {
			return doc, true
		}
	}
	return Document{}, false
}

// LookupEmbedded loads embedded documents once and returns the named scenario.
func LookupEmbedded(files fs.FS, id, resourceID string) (Document, error) {
	docs, err := Embedded(files)
	if err != nil {
		return Document{}, err
	}
	doc, ok := Lookup(docs, id)
	if !ok {
		return Document{}, fmt.Errorf("%s: unknown scenario %q", resourceID, id)
	}
	return doc, nil
}

// CanonicalJSON returns stable JSON suitable for hashing and storing. Go's
// JSON encoder sorts object keys; compacting also normalizes insignificant
// whitespace in the raw state document.
func CanonicalJSON(d Document) ([]byte, error) {
	if err := d.ValidateEnvelope(); err != nil {
		return nil, err
	}
	var value any
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("scenario: marshal: %w", err)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("scenario: normalize: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("scenario: canonicalize: %w", err)
	}
	return append(canonical, '\n'), nil
}

func Digest(canonicalJSON []byte) string {
	sum := sha256.Sum256(canonicalJSON)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("scenario: trailing data: %w", err)
	}
	return fmt.Errorf("scenario: multiple JSON values")
}
