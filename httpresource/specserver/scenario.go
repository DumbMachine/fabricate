package specserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Codec struct {
	resource string
	schema   *jsonschema.Schema
}

func NewCodec(resource string, schemaJSON []byte) httpresource.ScenarioCodec {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		panic(fmt.Sprintf("%s: parse embedded scenario schema: %v", resource, err))
	}
	compiler := jsonschema.NewCompiler()
	name := resource + "-scenario.json"
	if err := compiler.AddResource(name, parsed); err != nil {
		panic(fmt.Sprintf("%s: add embedded scenario schema: %v", resource, err))
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		panic(fmt.Sprintf("%s: compile embedded scenario schema: %v", resource, err))
	}
	return Codec{resource: resource, schema: compiled}
}

func (c Codec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != c.resource || doc.ResourceVersion != "v1" {
		return fmt.Errorf("%s scenario: expected resource %s v1, got %s %s", c.resource, c.resource, doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("%s scenario: decode state for schema validation: %w", c.resource, err)
	}
	if err := c.schema.Validate(instance); err != nil {
		return fmt.Errorf("%s scenario: schema validation: %w", c.resource, err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (c Codec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("%s scenario: initialize: %w", c.resource, err)
	}
	return nil
}

func (c Codec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := c.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s scenario: begin load: %w", c.resource, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM documents"); err != nil {
		return fmt.Errorf("%s scenario: clear documents: %w", c.resource, err)
	}
	names := make([]string, 0, len(state))
	for name := range state {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, collection := range names {
		for _, item := range state[collection] {
			id := DocumentID(item)
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO documents(collection, id, body) VALUES(?, ?, ?)`, collection, id, string(raw)); err != nil {
				return fmt.Errorf("%s scenario: insert %s %s: %w", c.resource, collection, id, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s scenario: commit load: %w", c.resource, err)
	}
	return nil
}

func (c Codec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := map[string][]map[string]any{}
	rows, err := db.QueryContext(ctx, `SELECT collection, id, body FROM documents ORDER BY collection, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("%s scenario: dump: %w", c.resource, err)
	}
	defer rows.Close()
	for rows.Next() {
		var collection, id, raw string
		if err := rows.Scan(&collection, &id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeObject(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state[collection] = append(state[collection], item)
	}
	if err := rows.Err(); err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: c.resource, ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (map[string][]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var state map[string][]map[string]any
	if err := dec.Decode(&state); err != nil {
		return nil, fmt.Errorf("scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("scenario: state has trailing data")
	}
	if state == nil {
		return nil, fmt.Errorf("scenario: state object is required")
	}
	return state, nil
}
