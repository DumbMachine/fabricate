package pipedrive

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
		panic(fmt.Sprintf("pipedrive: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("pipedrive-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("pipedrive: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("pipedrive-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("pipedrive: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Persons       []map[string]any `json:"persons"`
	Organizations []map[string]any `json:"organizations"`
	Deals         []map[string]any `json:"deals"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "pipedrive" || doc.ResourceVersion != "v2" {
		return fmt.Errorf("pipedrive scenario: expected resource pipedrive v2, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("pipedrive scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("pipedrive scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("pipedrive scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pipedrive scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM persons"); err != nil {
		return fmt.Errorf("pipedrive scenario: clear persons: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM organizations"); err != nil {
		return fmt.Errorf("pipedrive scenario: clear organizations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM deals"); err != nil {
		return fmt.Errorf("pipedrive scenario: clear deals: %w", err)
	}
	for _, item := range state.Persons {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO persons(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("pipedrive scenario: insert persons %s: %w", id, err)
		}
	}
	for _, item := range state.Organizations {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO organizations(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("pipedrive scenario: insert organizations %s: %w", id, err)
		}
	}
	for _, item := range state.Deals {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deals(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("pipedrive scenario: insert deals %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pipedrive scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Persons:       []map[string]any{},
		Organizations: []map[string]any{},
		Deals:         []map[string]any{},
	}
	personsRows, err := db.QueryContext(ctx, `SELECT id, body FROM persons ORDER BY CAST(id AS INTEGER), id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("pipedrive scenario: dump persons: %w", err)
	}
	for personsRows.Next() {
		var id, raw string
		if err := personsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Persons = append(state.Persons, item)
	}
	if err := personsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = personsRows.Close()
	organizationsRows, err := db.QueryContext(ctx, `SELECT id, body FROM organizations ORDER BY CAST(id AS INTEGER), id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("pipedrive scenario: dump organizations: %w", err)
	}
	for organizationsRows.Next() {
		var id, raw string
		if err := organizationsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Organizations = append(state.Organizations, item)
	}
	if err := organizationsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = organizationsRows.Close()
	dealsRows, err := db.QueryContext(ctx, `SELECT id, body FROM deals ORDER BY CAST(id AS INTEGER), id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("pipedrive scenario: dump deals: %w", err)
	}
	for dealsRows.Next() {
		var id, raw string
		if err := dealsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Deals = append(state.Deals, item)
	}
	if err := dealsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = dealsRows.Close()
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "pipedrive", ResourceVersion: "v2", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("pipedrive scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("pipedrive scenario: state has trailing data")
	}
	if state.Persons == nil || state.Organizations == nil || state.Deals == nil {
		return fixtureState{}, fmt.Errorf("pipedrive scenario: required arrays are missing")
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

func itemID(item map[string]any) string {
	switch value := item["id"].(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	case json.Number:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}

func ContractName() string { return scenario.Contract }
