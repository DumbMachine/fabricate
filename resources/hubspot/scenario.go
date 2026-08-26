package hubspot

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
		panic(fmt.Sprintf("hubspot: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("hubspot-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("hubspot: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("hubspot-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("hubspot: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Contacts  []fixtureObject `json:"contacts"`
	Companies []fixtureObject `json:"companies"`
	Deals     []fixtureObject `json:"deals"`
	Tickets   []fixtureObject `json:"tickets"`
}

type fixtureObject struct {
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties"`
	Archived   bool              `json:"archived,omitempty"`
	CreatedAt  string            `json:"createdAt,omitempty"`
	UpdatedAt  string            `json:"updatedAt,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "hubspot" || doc.ResourceVersion != "v3" {
		return fmt.Errorf("hubspot scenario: expected resource hubspot v3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("hubspot scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("hubspot scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, item := range []struct {
		kind    string
		objects []fixtureObject
	}{
		{"contacts", state.Contacts},
		{"companies", state.Companies},
		{"deals", state.Deals},
		{"tickets", state.Tickets},
	} {
		for i, object := range item.objects {
			if object.ID == "" || object.Properties == nil {
				return fmt.Errorf("hubspot scenario: %s[%d] requires id and properties", item.kind, i)
			}
			key := item.kind + "/" + object.ID
			if _, exists := seen[key]; exists {
				return fmt.Errorf("hubspot scenario: duplicate %s id %q", item.kind, object.ID)
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("hubspot scenario: initialize: %w", err)
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
		return fmt.Errorf("hubspot scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM objects"); err != nil {
		return fmt.Errorf("hubspot scenario: clear objects: %w", err)
	}
	for _, item := range []struct {
		kind    string
		objects []fixtureObject
	}{
		{"contacts", state.Contacts},
		{"companies", state.Companies},
		{"deals", state.Deals},
		{"tickets", state.Tickets},
	} {
		for _, object := range item.objects {
			raw, err := json.Marshal(object.Properties)
			if err != nil {
				return err
			}
			createdAt := defaultTime(object.CreatedAt)
			updatedAt := defaultTime(object.UpdatedAt)
			if object.UpdatedAt == "" {
				updatedAt = createdAt
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO objects(object_type, id, properties, archived, created_at, updated_at)
				VALUES(?, ?, ?, ?, ?, ?)`, item.kind, object.ID, string(raw), boolInt(object.Archived), createdAt, updatedAt); err != nil {
				return fmt.Errorf("hubspot scenario: insert %s %s: %w", item.kind, object.ID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("hubspot scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Contacts: []fixtureObject{}, Companies: []fixtureObject{},
		Deals: []fixtureObject{}, Tickets: []fixtureObject{},
	}
	rows, err := db.QueryContext(ctx, `SELECT object_type, id, properties, archived, created_at, updated_at
		FROM objects ORDER BY object_type, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("hubspot scenario: dump objects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var objectType, id, raw, createdAt, updatedAt string
		var archived int
		if err := rows.Scan(&objectType, &id, &raw, &archived, &createdAt, &updatedAt); err != nil {
			return scenario.Document{}, err
		}
		object := fixtureObject{ID: id, CreatedAt: createdAt, UpdatedAt: updatedAt, Archived: archived != 0}
		if err := json.Unmarshal([]byte(raw), &object.Properties); err != nil {
			return scenario.Document{}, err
		}
		if object.Properties == nil {
			object.Properties = map[string]string{}
		}
		switch objectType {
		case "contacts":
			state.Contacts = append(state.Contacts, object)
		case "companies":
			state.Companies = append(state.Companies, object)
		case "deals":
			state.Deals = append(state.Deals, object)
		case "tickets":
			state.Tickets = append(state.Tickets, object)
		default:
			return scenario.Document{}, fmt.Errorf("hubspot scenario: unknown object type %q", objectType)
		}
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
		Resource: "hubspot", ResourceVersion: "v3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("hubspot scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("hubspot scenario: state has trailing data")
	}
	if state.Contacts == nil || state.Companies == nil || state.Deals == nil || state.Tickets == nil {
		return fixtureState{}, fmt.Errorf("hubspot scenario: contacts, companies, deals, and tickets are required arrays")
	}
	return state, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func defaultTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return "2026-08-01T00:00:00Z"
	}
	return value
}

func ContractName() string { return scenario.Contract }
