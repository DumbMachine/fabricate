package mailgun

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
		panic(fmt.Sprintf("mailgun: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("mailgun-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("mailgun: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("mailgun-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("mailgun: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Domains  []map[string]any `json:"domains"`
	Events   []map[string]any `json:"events"`
	Messages []map[string]any `json:"messages"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "mailgun" || doc.ResourceVersion != "v3" {
		return fmt.Errorf("mailgun scenario: expected resource mailgun v3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("mailgun scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("mailgun scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("mailgun scenario: initialize: %w", err)
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
		return fmt.Errorf("mailgun scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM domains"); err != nil {
		return fmt.Errorf("mailgun scenario: clear domains: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM events"); err != nil {
		return fmt.Errorf("mailgun scenario: clear events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages"); err != nil {
		return fmt.Errorf("mailgun scenario: clear messages: %w", err)
	}
	for _, item := range state.Domains {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domains(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailgun scenario: insert domains %s: %w", id, err)
		}
	}
	for _, item := range state.Events {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailgun scenario: insert events %s: %w", id, err)
		}
	}
	for _, item := range state.Messages {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailgun scenario: insert messages %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mailgun scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Domains:  []map[string]any{},
		Events:   []map[string]any{},
		Messages: []map[string]any{},
	}
	domainsRows, err := db.QueryContext(ctx, `SELECT id, body FROM domains ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailgun scenario: dump domains: %w", err)
	}
	for domainsRows.Next() {
		var id, raw string
		if err := domainsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Domains = append(state.Domains, item)
	}
	if err := domainsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = domainsRows.Close()
	eventsRows, err := db.QueryContext(ctx, `SELECT id, body FROM events ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailgun scenario: dump events: %w", err)
	}
	for eventsRows.Next() {
		var id, raw string
		if err := eventsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Events = append(state.Events, item)
	}
	if err := eventsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = eventsRows.Close()
	messagesRows, err := db.QueryContext(ctx, `SELECT id, body FROM messages ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailgun scenario: dump messages: %w", err)
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
		Resource: "mailgun", ResourceVersion: "v3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("mailgun scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("mailgun scenario: state has trailing data")
	}
	if state.Domains == nil || state.Events == nil || state.Messages == nil {
		return fixtureState{}, fmt.Errorf("mailgun scenario: required arrays are missing")
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
