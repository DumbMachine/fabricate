package attio

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
		panic(fmt.Sprintf("attio: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("attio-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("attio: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("attio-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("attio: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Objects          []map[string]any `json:"objects"`
	Records          []map[string]any `json:"records"`
	WorkspaceMembers []map[string]any `json:"workspaceMembers"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "attio" || doc.ResourceVersion != "v2" {
		return fmt.Errorf("attio scenario: expected resource attio v2, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("attio scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("attio scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for i, object := range state.Objects {
		slug, _ := object["api_slug"].(string)
		if slug == "" {
			return fmt.Errorf("attio scenario: objects[%d] requires api_slug", i)
		}
		if _, exists := seen["object/"+slug]; exists {
			return fmt.Errorf("attio scenario: duplicate object slug %q", slug)
		}
		seen["object/"+slug] = struct{}{}
	}
	for i, record := range state.Records {
		objectSlug, _ := record["object"].(string)
		id := recordIDFrom(record)
		if objectSlug == "" || id == "" {
			return fmt.Errorf("attio scenario: records[%d] requires object and id.record_id", i)
		}
		key := "record/" + objectSlug + "/" + id
		if _, exists := seen[key]; exists {
			return fmt.Errorf("attio scenario: duplicate record %s", key)
		}
		seen[key] = struct{}{}
	}
	for i, member := range state.WorkspaceMembers {
		id := nestedString(member, "id", "workspace_member_id")
		if id == "" {
			return fmt.Errorf("attio scenario: workspaceMembers[%d] requires id.workspace_member_id", i)
		}
		if _, exists := seen["member/"+id]; exists {
			return fmt.Errorf("attio scenario: duplicate workspace member %q", id)
		}
		seen["member/"+id] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("attio scenario: initialize: %w", err)
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
		return fmt.Errorf("attio scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{"DELETE FROM records", "DELETE FROM objects", "DELETE FROM workspace_members"} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("attio scenario: clear: %w", err)
		}
	}
	for _, object := range state.Objects {
		slug, _ := object["api_slug"].(string)
		raw, err := json.Marshal(object)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO objects(api_slug, body) VALUES(?, ?)`, slug, string(raw)); err != nil {
			return fmt.Errorf("attio scenario: insert object %s: %w", slug, err)
		}
	}
	for _, record := range state.Records {
		objectSlug, _ := record["object"].(string)
		id := recordIDFrom(record)
		body := recordBody(record)
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO records(object_slug, record_id, body) VALUES(?, ?, ?)`, objectSlug, id, string(raw)); err != nil {
			return fmt.Errorf("attio scenario: insert record %s/%s: %w", objectSlug, id, err)
		}
	}
	for _, member := range state.WorkspaceMembers {
		id := nestedString(member, "id", "workspace_member_id")
		raw, err := json.Marshal(member)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
			return fmt.Errorf("attio scenario: insert member %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("attio scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state := fixtureState{
		Objects:          []map[string]any{},
		Records:          []map[string]any{},
		WorkspaceMembers: []map[string]any{},
	}
	objectRows, err := db.QueryContext(ctx, `SELECT api_slug, body FROM objects ORDER BY api_slug`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("attio scenario: dump objects: %w", err)
	}
	for objectRows.Next() {
		var slug, raw string
		if err := objectRows.Scan(&slug, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.Objects = append(state.Objects, item)
	}
	if err := objectRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = objectRows.Close()
	recordRows, err := db.QueryContext(ctx, `SELECT object_slug, record_id, body FROM records ORDER BY object_slug, record_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("attio scenario: dump records: %w", err)
	}
	for recordRows.Next() {
		var objectSlug, id, raw string
		if err := recordRows.Scan(&objectSlug, &id, &raw); err != nil {
			return scenario.Document{}, err
		}
		body, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		item := map[string]any{"object": objectSlug}
		for key, value := range body {
			item[key] = value
		}
		state.Records = append(state.Records, item)
	}
	if err := recordRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = recordRows.Close()
	memberRows, err := db.QueryContext(ctx, `SELECT id, body FROM workspace_members ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("attio scenario: dump members: %w", err)
	}
	for memberRows.Next() {
		var id, raw string
		if err := memberRows.Scan(&id, &raw); err != nil {
			return scenario.Document{}, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return scenario.Document{}, err
		}
		state.WorkspaceMembers = append(state.WorkspaceMembers, item)
	}
	if err := memberRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	_ = memberRows.Close()
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "attio", ResourceVersion: "v2", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("attio scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("attio scenario: state has trailing data")
	}
	if state.Objects == nil || state.Records == nil || state.WorkspaceMembers == nil {
		return fixtureState{}, fmt.Errorf("attio scenario: objects, records, and workspaceMembers are required arrays")
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

func recordIDFrom(record map[string]any) string {
	if id := nestedString(record, "id", "record_id"); id != "" {
		return id
	}
	id, _ := record["record_id"].(string)
	return id
}

func recordBody(record map[string]any) map[string]any {
	body := map[string]any{}
	for key, value := range record {
		if key == "object" {
			continue
		}
		body[key] = value
	}
	return body
}

func nestedString(item map[string]any, keys ...string) string {
	current := any(item)
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return value
}

func ContractName() string { return scenario.Contract }
