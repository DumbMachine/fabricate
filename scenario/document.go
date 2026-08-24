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
