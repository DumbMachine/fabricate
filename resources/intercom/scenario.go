package intercom

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strconv"

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
		panic(fmt.Sprintf("intercom: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("intercom-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("intercom: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("intercom-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("intercom: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CurrentAdminId string                `json:"currentAdminId"`
	Admins         []fixtureAdmin        `json:"admins"`
	Contacts       []fixtureContact      `json:"contacts"`
	Conversations  []fixtureConversation `json:"conversations"`
	Parts          []fixturePart         `json:"parts"`
}

type fixtureAdmin struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type fixtureContact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role,omitempty"`
	CreatedAt int    `json:"createdAt,omitempty"`
	UpdatedAt int    `json:"updatedAt,omitempty"`
}

type fixtureConversation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	ContactID string `json:"contactId"`
	CreatedAt int    `json:"createdAt,omitempty"`
	UpdatedAt int    `json:"updatedAt,omitempty"`
}

type fixturePart struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	AuthorType     string `json:"authorType"`
	AuthorID       string `json:"authorId"`
	Body           string `json:"body"`
	CreatedAt      int    `json:"createdAt,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "intercom" || doc.ResourceVersion != "2.16" {
		return fmt.Errorf("intercom scenario: expected resource intercom 2.16, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("intercom scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("intercom scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	admins := map[string]struct{}{}
	for i, admin := range state.Admins {
		if _, err := mail.ParseAddress(admin.Email); err != nil {
			return fmt.Errorf("intercom scenario: admins[%d].email: %w", i, err)
		}
		if _, exists := admins[admin.ID]; exists {
			return fmt.Errorf("intercom scenario: duplicate admin id %q", admin.ID)
		}
		admins[admin.ID] = struct{}{}
	}
	if _, ok := admins[state.CurrentAdminId]; !ok {
		return fmt.Errorf("intercom scenario: currentAdminId %q is not a seeded admin", state.CurrentAdminId)
	}
	contacts := map[string]struct{}{}
	for i, contact := range state.Contacts {
		if _, err := mail.ParseAddress(contact.Email); err != nil {
			return fmt.Errorf("intercom scenario: contacts[%d].email: %w", i, err)
		}
		if _, exists := contacts[contact.ID]; exists {
			return fmt.Errorf("intercom scenario: duplicate contact id %q", contact.ID)
		}
		contacts[contact.ID] = struct{}{}
	}
	conversations := map[string]struct{}{}
	for i, conversation := range state.Conversations {
		if _, err := strconv.Atoi(conversation.ID); err != nil {
			return fmt.Errorf("intercom scenario: conversations[%d].id must be a decimal integer because retrieve/update path params are integers, got %q", i, conversation.ID)
		}
		if _, ok := contacts[conversation.ContactID]; !ok {
			return fmt.Errorf("intercom scenario: conversations[%d] references unknown contact %q", i, conversation.ContactID)
		}
		if _, exists := conversations[conversation.ID]; exists {
			return fmt.Errorf("intercom scenario: duplicate conversation id %q", conversation.ID)
		}
		conversations[conversation.ID] = struct{}{}
	}
	parts := map[string]struct{}{}
	for i, part := range state.Parts {
		if _, ok := conversations[part.ConversationID]; !ok {
			return fmt.Errorf("intercom scenario: parts[%d] references unknown conversation %q", i, part.ConversationID)
		}
		if _, exists := parts[part.ID]; exists {
			return fmt.Errorf("intercom scenario: duplicate part id %q", part.ID)
		}
		parts[part.ID] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("intercom scenario: initialize: %w", err)
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
		return fmt.Errorf("intercom scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"conversation_parts", "conversations", "contacts", "admins", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("intercom scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentAdminId', ?)", state.CurrentAdminId); err != nil {
		return err
	}
	for _, admin := range state.Admins {
		if _, err := tx.ExecContext(ctx, "INSERT INTO admins(id, name, email) VALUES(?, ?, ?)", admin.ID, admin.Name, admin.Email); err != nil {
			return fmt.Errorf("intercom scenario: insert admin %s: %w", admin.ID, err)
		}
	}
	for _, contact := range state.Contacts {
		role := contact.Role
		if role == "" {
			role = "user"
		}
		created, updated := defaultUnix(contact.CreatedAt), defaultUnix(contact.UpdatedAt)
		if contact.UpdatedAt == 0 {
			updated = created
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO contacts(id, name, email, role, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
			contact.ID, contact.Name, contact.Email, role, created, updated); err != nil {
			return fmt.Errorf("intercom scenario: insert contact %s: %w", contact.ID, err)
		}
	}
	for _, conversation := range state.Conversations {
		created, updated := defaultUnix(conversation.CreatedAt), defaultUnix(conversation.UpdatedAt)
		if conversation.UpdatedAt == 0 {
			updated = created
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id, title, state, contact_id, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
			conversation.ID, conversation.Title, conversation.State, conversation.ContactID, created, updated); err != nil {
			return fmt.Errorf("intercom scenario: insert conversation %s: %w", conversation.ID, err)
		}
	}
	for _, part := range state.Parts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_parts(id, conversation_id, author_type, author_id, body, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
			part.ID, part.ConversationID, part.AuthorType, part.AuthorID, part.Body, defaultUnix(part.CreatedAt)); err != nil {
			return fmt.Errorf("intercom scenario: insert part %s: %w", part.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("intercom scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentAdminId'").Scan(&state.CurrentAdminId); err != nil {
		return scenario.Document{}, fmt.Errorf("intercom scenario: dump current admin: %w", err)
	}
	adminRows, err := db.QueryContext(ctx, "SELECT id, name, email FROM admins ORDER BY id")
	if err != nil {
		return scenario.Document{}, err
	}
	for adminRows.Next() {
		var admin fixtureAdmin
		if err := adminRows.Scan(&admin.ID, &admin.Name, &admin.Email); err != nil {
			adminRows.Close()
			return scenario.Document{}, err
		}
		state.Admins = append(state.Admins, admin)
	}
	if err := adminRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	contactRows, err := db.QueryContext(ctx, "SELECT id, name, email, role, created_at, updated_at FROM contacts ORDER BY id")
	if err != nil {
		return scenario.Document{}, err
	}
	for contactRows.Next() {
		var contact fixtureContact
		if err := contactRows.Scan(&contact.ID, &contact.Name, &contact.Email, &contact.Role, &contact.CreatedAt, &contact.UpdatedAt); err != nil {
			contactRows.Close()
			return scenario.Document{}, err
		}
		state.Contacts = append(state.Contacts, contact)
	}
	if err := contactRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	conversationRows, err := db.QueryContext(ctx, "SELECT id, title, state, contact_id, created_at, updated_at FROM conversations ORDER BY id")
	if err != nil {
		return scenario.Document{}, err
	}
	for conversationRows.Next() {
		var conversation fixtureConversation
		if err := conversationRows.Scan(&conversation.ID, &conversation.Title, &conversation.State, &conversation.ContactID, &conversation.CreatedAt, &conversation.UpdatedAt); err != nil {
			conversationRows.Close()
			return scenario.Document{}, err
		}
		state.Conversations = append(state.Conversations, conversation)
	}
	if err := conversationRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	partRows, err := db.QueryContext(ctx, "SELECT id, conversation_id, author_type, author_id, body, created_at FROM conversation_parts ORDER BY id")
	if err != nil {
		return scenario.Document{}, err
	}
	for partRows.Next() {
		var part fixturePart
		if err := partRows.Scan(&part.ID, &part.ConversationID, &part.AuthorType, &part.AuthorID, &part.Body, &part.CreatedAt); err != nil {
			partRows.Close()
			return scenario.Document{}, err
		}
		state.Parts = append(state.Parts, part)
	}
	if err := partRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if state.Admins == nil {
		state.Admins = []fixtureAdmin{}
	}
	if state.Contacts == nil {
		state.Contacts = []fixtureContact{}
	}
	if state.Conversations == nil {
		state.Conversations = []fixtureConversation{}
	}
	if state.Parts == nil {
		state.Parts = []fixturePart{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "intercom", ResourceVersion: "2.16", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("intercom scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("intercom scenario: state has trailing data")
	}
	if state.Admins == nil || state.Contacts == nil || state.Conversations == nil || state.Parts == nil {
		return fixtureState{}, fmt.Errorf("intercom scenario: admins, contacts, conversations, and parts are required arrays")
	}
	return state, nil
}

func defaultUnix(value int) int {
	if value == 0 {
		return 1754006400
	}
	return value
}

func ContractName() string { return scenario.Contract }
