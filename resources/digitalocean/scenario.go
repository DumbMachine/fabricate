package digitalocean

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
		panic(fmt.Sprintf("digitalocean: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("digitalocean-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("digitalocean: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("digitalocean-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("digitalocean: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Droplets []map[string]any `json:"droplets"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "digitalocean" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("digitalocean scenario: expected resource digitalocean v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("digitalocean scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("digitalocean scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("digitalocean scenario: initialize: %w", err)
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
		return fmt.Errorf("digitalocean scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM droplets"); err != nil {
		return fmt.Errorf("digitalocean scenario: clear droplets: %w", err)
	}
	for _, item := range state.Droplets {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO droplets(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("digitalocean scenario: insert droplets %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("digitalocean scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{Droplets: []map[string]any{}}
	rows, err := db.QueryContext(ctx, `SELECT id, body FROM droplets ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("digitalocean scenario: dump droplets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Droplets = append(state.Droplets, item)
	}
	if err := rows.Err(); err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "digitalocean", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("digitalocean scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("digitalocean scenario: state has trailing data")
	}
	if state.Droplets == nil {
		return fixtureState{}, fmt.Errorf("digitalocean scenario: required arrays are missing")
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
	for _, key := range []string{"id", "number", "gid"} {
		switch value := item[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case float64:
			return fmt.Sprintf("%.0f", value)
		case json.Number:
			return value.String()
		}
	}
	return fmt.Sprint(item["id"])
}

func ContractName() string { return scenario.Contract }
