package gmail

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

// builtInScenarios keeps the reference worlds inside the fab binary so
// foreground runs do not depend on the source checkout being present.
//
//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("gmail: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("gmail-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("gmail: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("gmail-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("gmail: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

var systemLabels = []fixtureLabel{
	{ID: "INBOX", Name: "INBOX", Type: "system"},
	{ID: "UNREAD", Name: "UNREAD", Type: "system"},
	{ID: "SENT", Name: "SENT", Type: "system"},
	{ID: "TRASH", Name: "TRASH", Type: "system"},
	{ID: "SPAM", Name: "SPAM", Type: "system"},
	{ID: "STARRED", Name: "STARRED", Type: "system"},
	{ID: "IMPORTANT", Name: "IMPORTANT", Type: "system"},
}

type scenarioCodec struct{}

type fixtureState struct {
	EmailAddress string           `json:"emailAddress"`
	Labels       []fixtureLabel   `json:"labels"`
	Messages     []fixtureMessage `json:"messages"`
}

type fixtureLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type fixtureMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate int64    `json:"internalDate"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "gmail" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("gmail scenario: expected resource gmail v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("gmail scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("gmail scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if _, err := mail.ParseAddress(state.EmailAddress); err != nil {
		return fmt.Errorf("gmail scenario: emailAddress: %w", err)
	}
	labels := make(map[string]struct{}, len(systemLabels)+len(state.Labels))
	for _, label := range systemLabels {
		labels[label.ID] = struct{}{}
	}
	for i, label := range state.Labels {
		if label.ID == "" || label.Name == "" || label.Type != "user" {
			return fmt.Errorf("gmail scenario: labels[%d] must have id, name, and type user", i)
		}
		if _, exists := labels[label.ID]; exists {
			return fmt.Errorf("gmail scenario: duplicate label id %q", label.ID)
		}
		labels[label.ID] = struct{}{}
	}
	messages := make(map[string]struct{}, len(state.Messages))
	for i, message := range state.Messages {
		if message.ID == "" || message.ThreadID == "" || message.From == "" || message.To == "" {
			return fmt.Errorf("gmail scenario: messages[%d] requires id, threadId, from, and to", i)
		}
		if message.InternalDate < 0 {
			return fmt.Errorf("gmail scenario: messages[%d].internalDate must be non-negative", i)
		}
		if _, exists := messages[message.ID]; exists {
			return fmt.Errorf("gmail scenario: duplicate message id %q", message.ID)
		}
		messages[message.ID] = struct{}{}
		seenLabels := map[string]struct{}{}
		for _, labelID := range message.LabelIDs {
			if _, exists := labels[labelID]; !exists {
				return fmt.Errorf("gmail scenario: message %q references unknown label %q", message.ID, labelID)
			}
			if _, exists := seenLabels[labelID]; exists {
				return fmt.Errorf("gmail scenario: message %q repeats label %q", message.ID, labelID)
			}
			seenLabels[labelID] = struct{}{}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("gmail scenario: initialize: %w", err)
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
		return fmt.Errorf("gmail scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"messages", "labels", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("gmail scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('emailAddress', ?), ('historyId', '1')", state.EmailAddress); err != nil {
		return fmt.Errorf("gmail scenario: insert metadata: %w", err)
	}
	for _, label := range append(append([]fixtureLabel{}, systemLabels...), state.Labels...) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO labels(id, name, type) VALUES(?, ?, ?)", label.ID, label.Name, label.Type); err != nil {
			return fmt.Errorf("gmail scenario: insert label %s: %w", label.ID, err)
		}
	}
	for _, message := range state.Messages {
		labels, _ := json.Marshal(message.LabelIDs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages
			(id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, message.ID, message.ThreadID, message.From,
			message.To, message.Subject, message.Body, message.InternalDate, string(labels)); err != nil {
			return fmt.Errorf("gmail scenario: insert message %s: %w", message.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gmail scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='emailAddress'").Scan(&state.EmailAddress); err != nil {
		return scenario.Document{}, fmt.Errorf("gmail scenario: dump email: %w", err)
	}
	labelRows, err := db.QueryContext(ctx, "SELECT id, name, type FROM labels WHERE type='user' ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("gmail scenario: dump labels: %w", err)
	}
	for labelRows.Next() {
		var label fixtureLabel
		if err := labelRows.Scan(&label.ID, &label.Name, &label.Type); err != nil {
			labelRows.Close()
			return scenario.Document{}, err
		}
		state.Labels = append(state.Labels, label)
	}
	if err := labelRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	messageRows, err := db.QueryContext(ctx, `SELECT id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids
		FROM messages ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("gmail scenario: dump messages: %w", err)
	}
	defer messageRows.Close()
	for messageRows.Next() {
		var message fixtureMessage
		var labels string
		if err := messageRows.Scan(&message.ID, &message.ThreadID, &message.From, &message.To,
			&message.Subject, &message.Body, &message.InternalDate, &labels); err != nil {
			return scenario.Document{}, err
		}
		if err := json.Unmarshal([]byte(labels), &message.LabelIDs); err != nil {
			return scenario.Document{}, fmt.Errorf("gmail scenario: dump message %s labels: %w", message.ID, err)
		}
		state.Messages = append(state.Messages, message)
	}
	if err := messageRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Labels == nil {
		state.Labels = []fixtureLabel{}
	}
	if state.Messages == nil {
		state.Messages = []fixtureMessage{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "gmail", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("gmail scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("gmail scenario: state has trailing data")
	}
	if state.Labels == nil || state.Messages == nil {
		return fixtureState{}, fmt.Errorf("gmail scenario: labels and messages are required arrays")
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }
