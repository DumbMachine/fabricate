package vanta

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("vanta: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("vanta-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("vanta: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("vanta-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("vanta: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Vendors         []map[string]any `json:"vendors"`
	SecurityReviews []map[string]any `json:"securityReviews"`
	Tests           []map[string]any `json:"tests"`
	Entities        []map[string]any `json:"entities"`
	Documents       []map[string]any `json:"documents"`
	Uploads         []map[string]any `json:"uploads"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "vanta" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("vanta scenario: expected resource vanta v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("vanta scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("vanta scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	vendors, err := indexIDs(state.Vendors, "vendor")
	if err != nil {
		return err
	}
	if _, err := indexIDs(state.SecurityReviews, "security review"); err != nil {
		return err
	}
	tests, err := indexIDs(state.Tests, "test")
	if err != nil {
		return err
	}
	if _, err := indexIDs(state.Entities, "entity"); err != nil {
		return err
	}
	documents, err := indexIDs(state.Documents, "document")
	if err != nil {
		return err
	}
	if _, err := indexIDs(state.Uploads, "upload"); err != nil {
		return err
	}
	if err := requireRef(state.SecurityReviews, "vendorId", vendors, "security review"); err != nil {
		return err
	}
	if err := requireRef(state.Entities, "testId", tests, "entity"); err != nil {
		return err
	}
	if err := requireRef(state.Uploads, "documentId", documents, "upload"); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("vanta scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vanta scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"uploads", "documents", "entities", "tests", "security_reviews", "vendors"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("vanta scenario: clear %s: %w", table, err)
		}
	}
	if err := insertBodies(ctx, tx, "INSERT INTO vendors(id, body) VALUES(?, ?)", state.Vendors, ""); err != nil {
		return err
	}
	if err := insertBodies(ctx, tx, "INSERT INTO security_reviews(id, vendor_id, body) VALUES(?, ?, ?)", state.SecurityReviews, "vendorId"); err != nil {
		return err
	}
	if err := insertBodies(ctx, tx, "INSERT INTO tests(id, body) VALUES(?, ?)", state.Tests, ""); err != nil {
		return err
	}
	if err := insertBodies(ctx, tx, "INSERT INTO entities(id, test_id, body) VALUES(?, ?, ?)", state.Entities, "testId"); err != nil {
		return err
	}
	if err := insertBodies(ctx, tx, "INSERT INTO documents(id, body) VALUES(?, ?)", state.Documents, ""); err != nil {
		return err
	}
	if err := insertBodies(ctx, tx, "INSERT INTO uploads(id, document_id, body) VALUES(?, ?, ?)", state.Uploads, "documentId"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("vanta scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Vendors:         []map[string]any{},
		SecurityReviews: []map[string]any{},
		Tests:           []map[string]any{},
		Entities:        []map[string]any{},
		Documents:       []map[string]any{},
		Uploads:         []map[string]any{},
	}
	var err error
	if state.Vendors, err = queryBodies(ctx, db, "SELECT body FROM vendors ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump vendors: %w", err)
	}
	if state.SecurityReviews, err = queryBodies(ctx, db, "SELECT body FROM security_reviews ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump security reviews: %w", err)
	}
	if state.Tests, err = queryBodies(ctx, db, "SELECT body FROM tests ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump tests: %w", err)
	}
	if state.Entities, err = queryBodies(ctx, db, "SELECT body FROM entities ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump entities: %w", err)
	}
	if state.Documents, err = queryBodies(ctx, db, "SELECT body FROM documents ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump documents: %w", err)
	}
	if state.Uploads, err = queryBodies(ctx, db, "SELECT body FROM uploads ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("vanta scenario: dump uploads: %w", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "vanta", ResourceVersion: "v1", State: raw,
	}, nil
}

func insertBodies(ctx context.Context, tx *sql.Tx, query string, items []map[string]any, refKey string) error {
	for _, item := range items {
		id, _ := item["id"].(string)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		args := []any{id}
		if refKey != "" {
			ref, _ := item[refKey].(string)
			args = append(args, ref)
		}
		args = append(args, string(raw))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("vanta scenario: insert %s: %w", id, err)
		}
	}
	return nil
}

func queryBodies(ctx context.Context, db *sql.DB, query string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("vanta scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("vanta scenario: state has trailing data")
	}
	if state.Vendors == nil || state.SecurityReviews == nil || state.Tests == nil || state.Entities == nil || state.Documents == nil || state.Uploads == nil {
		return fixtureState{}, fmt.Errorf("vanta scenario: required arrays are missing")
	}
	return state, nil
}

func decodeMap(raw string) (map[string]any, error) {
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, err
	}
	return item, nil
}

func indexIDs(items []map[string]any, label string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id, ok := item["id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("vanta scenario: %s id must be a non-empty string", label)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("vanta scenario: duplicate %s id %q", label, id)
		}
		seen[id] = struct{}{}
	}
	return seen, nil
}

func requireRef(items []map[string]any, field string, known map[string]struct{}, label string) error {
	for _, item := range items {
		id, _ := item["id"].(string)
		ref, ok := item[field].(string)
		if !ok || ref == "" {
			return fmt.Errorf("vanta scenario: %s %q missing %s", label, id, field)
		}
		if _, exists := known[ref]; !exists {
			return fmt.Errorf("vanta scenario: %s %q references unknown %s %q", label, id, field, ref)
		}
	}
	return nil
}

func ContractName() string { return scenario.Contract }
