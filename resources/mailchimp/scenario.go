package mailchimp

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
		panic(fmt.Sprintf("mailchimp: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("mailchimp-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("mailchimp: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("mailchimp-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("mailchimp: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Lists     []map[string]any `json:"lists"`
	Members   []map[string]any `json:"members"`
	Campaigns []map[string]any `json:"campaigns"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "mailchimp" || doc.ResourceVersion != "3.0" {
		return fmt.Errorf("mailchimp scenario: expected resource mailchimp 3.0, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("mailchimp scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("mailchimp scenario: schema validation: %w", err)
	}
	if _, err := decodeState(doc.State); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("mailchimp scenario: initialize: %w", err)
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
		return fmt.Errorf("mailchimp scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM lists"); err != nil {
		return fmt.Errorf("mailchimp scenario: clear lists: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM members"); err != nil {
		return fmt.Errorf("mailchimp scenario: clear members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM campaigns"); err != nil {
		return fmt.Errorf("mailchimp scenario: clear campaigns: %w", err)
	}
	for _, item := range state.Lists {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO lists(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailchimp scenario: insert lists %s: %w", id, err)
		}
	}
	for _, item := range state.Members {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO members(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailchimp scenario: insert members %s: %w", id, err)
		}
	}
	for _, item := range state.Campaigns {
		id := itemID(item)
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO campaigns(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("mailchimp scenario: insert campaigns %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mailchimp scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Lists:     []map[string]any{},
		Members:   []map[string]any{},
		Campaigns: []map[string]any{},
	}
	listsRows, err := db.QueryContext(ctx, `SELECT id, body FROM lists ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailchimp scenario: dump lists: %w", err)
	}
	for listsRows.Next() {
		var id, raw string
		if err := listsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Lists = append(state.Lists, item)
	}
	if err := listsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = listsRows.Close()
	membersRows, err := db.QueryContext(ctx, `SELECT id, body FROM members ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailchimp scenario: dump members: %w", err)
	}
	for membersRows.Next() {
		var id, raw string
		if err := membersRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Members = append(state.Members, item)
	}
	if err := membersRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = membersRows.Close()
	campaignsRows, err := db.QueryContext(ctx, `SELECT id, body FROM campaigns ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailchimp scenario: dump campaigns: %w", err)
	}
	for campaignsRows.Next() {
		var id, raw string
		if err := campaignsRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Campaigns = append(state.Campaigns, item)
	}
	if err := campaignsRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = campaignsRows.Close()
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "mailchimp", ResourceVersion: "3.0", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("mailchimp scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("mailchimp scenario: state has trailing data")
	}
	if state.Lists == nil || state.Members == nil || state.Campaigns == nil {
		return fixtureState{}, fmt.Errorf("mailchimp scenario: required arrays are missing")
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
