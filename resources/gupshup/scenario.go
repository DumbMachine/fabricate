package gupshup

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

// builtInScenarios keeps the reference worlds inside the fab binary so
// foreground runs do not depend on the source checkout being present.
//
//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("gupshup: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("gupshup-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("gupshup: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("gupshup-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("gupshup: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	App       fixtureApp        `json:"app"`
	Templates []fixtureTemplate `json:"templates"`
	Messages  []fixtureMessage  `json:"messages"`
}

type fixtureApp struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Phone    string          `json:"phone"`
	About    string          `json:"about"`
	Business fixtureBusiness `json:"business"`
	Profile  fixtureProfile  `json:"profile"`
}

type fixtureBusiness struct {
	Name          string `json:"name"`
	AddressLine1  string `json:"addressLine1"`
	AddressLine2  string `json:"addressLine2"`
	City          string `json:"city"`
	State         string `json:"state"`
	Country       string `json:"country"`
	PinCode       string `json:"pinCode"`
	ContactName   string `json:"contactName"`
	ContactNumber string `json:"contactNumber"`
	Email         string `json:"email"`
	Website       string `json:"website"`
	Vertical      string `json:"vertical"`
	EmailVerified bool   `json:"emailVerified"`
	TncAccepted   bool   `json:"tncAccepted"`
}

type fixtureProfile struct {
	AddressLine1 string `json:"addressLine1"`
	AddressLine2 string `json:"addressLine2"`
	City         string `json:"city"`
	Country      string `json:"country"`
	Desc         string `json:"desc"`
	PinCode      string `json:"pinCode"`
	ProfileEmail string `json:"profileEmail"`
	State        string `json:"state"`
	Vertical     string `json:"vertical"`
	Website1     string `json:"website1"`
	Website2     string `json:"website2"`
}

type fixtureTemplate struct {
	ID           string `json:"id"`
	ElementName  string `json:"elementName"`
	Category     string `json:"category"`
	Status       string `json:"status"`
	LanguageCode string `json:"languageCode"`
	TemplateType string `json:"templateType"`
	Data         string `json:"data"`
}

type fixtureMessage struct {
	MessageID   string `json:"messageId"`
	Direction   string `json:"direction"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	SenderName  string `json:"senderName"`
	ContextGsID string `json:"contextGsId"`
	Read        bool   `json:"read"`
	Timestamp   int64  `json:"timestamp"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "gupshup" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("gupshup scenario: expected resource gupshup v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("gupshup scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("gupshup scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if state.App.ID == "" || state.App.Name == "" || state.App.Phone == "" {
		return fmt.Errorf("gupshup scenario: app requires id, name, and phone")
	}
	templates := make(map[string]struct{}, len(state.Templates))
	for i, template := range state.Templates {
		if _, exists := templates[template.ID]; exists {
			return fmt.Errorf("gupshup scenario: duplicate template id %q", template.ID)
		}
		templates[template.ID] = struct{}{}
		if template.ElementName == "" {
			return fmt.Errorf("gupshup scenario: templates[%d] requires elementName", i)
		}
	}
	messages := make(map[string]struct{}, len(state.Messages))
	for _, message := range state.Messages {
		if _, exists := messages[message.MessageID]; exists {
			return fmt.Errorf("gupshup scenario: duplicate message id %q", message.MessageID)
		}
		messages[message.MessageID] = struct{}{}
	}
	for _, message := range state.Messages {
		if message.ContextGsID == "" {
			continue
		}
		if _, exists := messages[message.ContextGsID]; !exists {
			return fmt.Errorf("gupshup scenario: message %q references unknown contextGsId %q", message.MessageID, message.ContextGsID)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("gupshup scenario: initialize: %w", err)
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
		return fmt.Errorf("gupshup scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"messages", "templates", "app"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("gupshup scenario: clear %s: %w", table, err)
		}
	}
	if err := insertApp(ctx, tx, state.App); err != nil {
		return err
	}
	for i, template := range state.Templates {
		if err := insertTemplate(ctx, tx, template, i); err != nil {
			return err
		}
	}
	for i, message := range state.Messages {
		if err := insertMessage(ctx, tx, message, i); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gupshup scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	app, err := loadApp(ctx, db)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("gupshup scenario: dump app: %w", err)
	}
	templates, err := loadTemplates(ctx, db)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("gupshup scenario: dump templates: %w", err)
	}
	messages, err := loadMessagesByPosition(ctx, db)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("gupshup scenario: dump messages: %w", err)
	}
	state := fixtureState{App: app, Templates: templates, Messages: messages}
	if state.Templates == nil {
		state.Templates = []fixtureTemplate{}
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
		Resource: "gupshup", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("gupshup scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("gupshup scenario: state has trailing data")
	}
	if state.Templates == nil || state.Messages == nil {
		return fixtureState{}, fmt.Errorf("gupshup scenario: templates and messages are required arrays")
	}
	return state, nil
}

type sqlExec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type sqlQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertApp(ctx context.Context, exec sqlExec, app fixtureApp) error {
	business, err := json.Marshal(app.Business)
	if err != nil {
		return err
	}
	profile, err := json.Marshal(app.Profile)
	if err != nil {
		return err
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO app(id, name, phone, about, business_json, profile_json) VALUES(?, ?, ?, ?, ?, ?)`,
		app.ID, app.Name, app.Phone, app.About, string(business), string(profile))
	if err != nil {
		return fmt.Errorf("gupshup scenario: insert app: %w", err)
	}
	return nil
}

func insertTemplate(ctx context.Context, exec sqlExec, template fixtureTemplate, position int) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO templates(id, element_name, category, status, language_code, template_type, data, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		template.ID, template.ElementName, template.Category, template.Status, template.LanguageCode, template.TemplateType, template.Data, position)
	if err != nil {
		return fmt.Errorf("gupshup scenario: insert template %s: %w", template.ID, err)
	}
	return nil
}

func insertMessage(ctx context.Context, exec sqlExec, message fixtureMessage, position int) error {
	read := 0
	if message.Read {
		read = 1
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO messages(
		message_id, direction, source, destination, type, text, status, sender_name, context_gs_id, read, timestamp, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.MessageID, message.Direction, message.Source, message.Destination, message.Type, message.Text,
		message.Status, message.SenderName, message.ContextGsID, read, message.Timestamp, position)
	if err != nil {
		return fmt.Errorf("gupshup scenario: insert message %s: %w", message.MessageID, err)
	}
	return nil
}

func loadApp(ctx context.Context, query sqlQuery) (fixtureApp, error) {
	var app fixtureApp
	var business, profile string
	err := query.QueryRowContext(ctx, `SELECT id, name, phone, about, business_json, profile_json FROM app`).Scan(
		&app.ID, &app.Name, &app.Phone, &app.About, &business, &profile)
	if err != nil {
		return fixtureApp{}, err
	}
	if err := json.Unmarshal([]byte(business), &app.Business); err != nil {
		return fixtureApp{}, fmt.Errorf("gupshup scenario: decode business: %w", err)
	}
	if err := json.Unmarshal([]byte(profile), &app.Profile); err != nil {
		return fixtureApp{}, fmt.Errorf("gupshup scenario: decode profile: %w", err)
	}
	return app, nil
}

func loadTemplates(ctx context.Context, query sqlQuery) ([]fixtureTemplate, error) {
	rows, err := query.QueryContext(ctx, `SELECT id, element_name, category, status, language_code, template_type, data
		FROM templates ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := []fixtureTemplate{}
	for rows.Next() {
		var template fixtureTemplate
		if err := rows.Scan(&template.ID, &template.ElementName, &template.Category, &template.Status, &template.LanguageCode, &template.TemplateType, &template.Data); err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

func loadMessagesByPosition(ctx context.Context, query sqlQuery) ([]fixtureMessage, error) {
	return queryMessages(ctx, query, `SELECT `+messageColumns+` FROM messages ORDER BY position, message_id`)
}

func loadMessagesByTime(ctx context.Context, query sqlQuery) ([]fixtureMessage, error) {
	return queryMessages(ctx, query, `SELECT `+messageColumns+` FROM messages ORDER BY timestamp, message_id`)
}

const messageColumns = "message_id, direction, source, destination, type, text, status, sender_name, context_gs_id, read, timestamp"

func queryMessages(ctx context.Context, query sqlQuery, statement string) ([]fixtureMessage, error) {
	rows, err := query.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []fixtureMessage{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func getMessage(ctx context.Context, query sqlQuery, id string) (fixtureMessage, error) {
	return scanMessage(query.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE message_id=?`, id))
}

func scanMessage(row interface{ Scan(...any) error }) (fixtureMessage, error) {
	var message fixtureMessage
	var read int
	err := row.Scan(&message.MessageID, &message.Direction, &message.Source, &message.Destination, &message.Type,
		&message.Text, &message.Status, &message.SenderName, &message.ContextGsID, &read, &message.Timestamp)
	if err != nil {
		return fixtureMessage{}, err
	}
	message.Read = read == 1
	return message, nil
}

func nextMessagePosition(ctx context.Context, query sqlQuery) (int, error) {
	var position int
	err := query.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM messages`).Scan(&position)
	return position, err
}

func ContractName() string { return scenario.Contract }
