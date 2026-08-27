package resend

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
		panic(fmt.Sprintf("resend: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("resend-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("resend: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("resend-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("resend: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Emails  []map[string]any `json:"emails"`
	Domains []map[string]any `json:"domains"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "resend" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("resend scenario: expected resource resend v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("resend scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("resend scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("resend scenario: initialize: %w", err)
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
		return fmt.Errorf("resend scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM emails"); err != nil {
		return fmt.Errorf("resend scenario: clear emails: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM domains"); err != nil {
		return fmt.Errorf("resend scenario: clear domains: %w", err)
	}
	for _, item := range state.Emails {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO emails(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("resend scenario: insert emails %s: %w", id, err)
		}
	}
	for _, item := range state.Domains {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domains(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("resend scenario: insert domains %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resend scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Emails:  []map[string]any{},
		Domains: []map[string]any{},
	}
	emailsRows, err := db.QueryContext(ctx, `SELECT id, body FROM emails ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("resend scenario: dump emails: %w", err)
	}
	for emailsRows.Next() {
		var id, raw string
		if err := emailsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Emails = append(state.Emails, item)
	}
	if err := emailsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = emailsRows.Close()
	domainsRows, err := db.QueryContext(ctx, `SELECT id, body FROM domains ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("resend scenario: dump domains: %w", err)
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
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "resend", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("resend scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("resend scenario: state has trailing data")
	}
	if state.Emails == nil || state.Domains == nil {
		return fixtureState{}, fmt.Errorf("resend scenario: required arrays are missing")
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
