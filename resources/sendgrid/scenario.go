package sendgrid

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
		panic(fmt.Sprintf("sendgrid: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("sendgrid-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("sendgrid: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("sendgrid-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("sendgrid: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Batches  []map[string]any `json:"batches"`
	Messages []map[string]any `json:"messages"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "sendgrid" || doc.ResourceVersion != "v3" {
		return fmt.Errorf("sendgrid scenario: expected resource sendgrid v3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("sendgrid scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("sendgrid scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("sendgrid scenario: initialize: %w", err)
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
		return fmt.Errorf("sendgrid scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM batches"); err != nil {
		return fmt.Errorf("sendgrid scenario: clear batches: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages"); err != nil {
		return fmt.Errorf("sendgrid scenario: clear messages: %w", err)
	}
	for _, item := range state.Batches {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO batches(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("sendgrid scenario: insert batches %s: %w", id, err)
		}
	}
	for _, item := range state.Messages {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("sendgrid scenario: insert messages %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sendgrid scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Batches:  []map[string]any{},
		Messages: []map[string]any{},
	}
	batchesRows, err := db.QueryContext(ctx, `SELECT id, body FROM batches ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("sendgrid scenario: dump batches: %w", err)
	}
	for batchesRows.Next() {
		var id, raw string
		if err := batchesRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Batches = append(state.Batches, item)
	}
	if err := batchesRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = batchesRows.Close()
	messagesRows, err := db.QueryContext(ctx, `SELECT id, body FROM messages ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("sendgrid scenario: dump messages: %w", err)
	}
	for messagesRows.Next() {
		var id, raw string
		if err := messagesRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Messages = append(state.Messages, item)
	}
	if err := messagesRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = messagesRows.Close()
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "sendgrid", ResourceVersion: "v3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("sendgrid scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("sendgrid scenario: state has trailing data")
	}
	if state.Batches == nil || state.Messages == nil {
		return fixtureState{}, fmt.Errorf("sendgrid scenario: required arrays are missing")
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
