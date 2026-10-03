package docusign

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"time"

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
		panic(fmt.Sprintf("docusign: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("docusign-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("docusign: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("docusign-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("docusign: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Account   fixtureAccount    `json:"account"`
	Users     []fixtureUser     `json:"users"`
	Envelopes []fixtureEnvelope `json:"envelopes"`
}

type fixtureAccount struct {
	AccountId         string `json:"accountId"`
	AccountName       string `json:"accountName"`
	ExternalAccountId string `json:"externalAccountId"`
	CurrencyCode      string `json:"currencyCode"`
	PlanName          string `json:"planName"`
	CreatedDate       string `json:"createdDate"`
}

type fixtureUser struct {
	UserId     string `json:"userId"`
	UserName   string `json:"userName"`
	Email      string `json:"email"`
	UserStatus string `json:"userStatus"`
	IsAdmin    string `json:"isAdmin"`
}

type fixtureEnvelope struct {
	EnvelopeId            string            `json:"envelopeId"`
	Status                string            `json:"status"`
	EmailSubject          string            `json:"emailSubject"`
	EmailBlurb            string            `json:"emailBlurb"`
	CreatedDateTime       string            `json:"createdDateTime"`
	SentDateTime          string            `json:"sentDateTime"`
	DeliveredDateTime     string            `json:"deliveredDateTime"`
	CompletedDateTime     string            `json:"completedDateTime"`
	StatusChangedDateTime string            `json:"statusChangedDateTime"`
	VoidedDateTime        string            `json:"voidedDateTime"`
	VoidedReason          string            `json:"voidedReason"`
	SenderUserId          string            `json:"senderUserId"`
	Documents             []fixtureDocument `json:"documents"`
	Signers               []fixtureSigner   `json:"signers"`
}

type fixtureDocument struct {
	DocumentId     string `json:"documentId"`
	Name           string `json:"name"`
	FileExtension  string `json:"fileExtension"`
	Order          string `json:"order"`
	DocumentBase64 string `json:"documentBase64"`
}

type fixtureSigner struct {
	RecipientId       string `json:"recipientId"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	RoutingOrder      string `json:"routingOrder"`
	Status            string `json:"status"`
	SentDateTime      string `json:"sentDateTime"`
	DeliveredDateTime string `json:"deliveredDateTime"`
	SignedDateTime    string `json:"signedDateTime"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "docusign" || doc.ResourceVersion != "v2.1" {
		return fmt.Errorf("docusign scenario: expected resource docusign v2.1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("docusign scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("docusign scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if err := validTimestamp(state.Account.CreatedDate, false); err != nil {
		return fmt.Errorf("docusign scenario: account.createdDate: %w", err)
	}
	users := map[string]struct{}{}
	for i, user := range state.Users {
		if _, err := mail.ParseAddress(user.Email); err != nil {
			return fmt.Errorf("docusign scenario: users[%d].email: %w", i, err)
		}
		if _, exists := users[user.UserId]; exists {
			return fmt.Errorf("docusign scenario: duplicate user id %q", user.UserId)
		}
		users[user.UserId] = struct{}{}
	}
	envelopes := map[string]struct{}{}
	for i, envelope := range state.Envelopes {
		if _, exists := envelopes[envelope.EnvelopeId]; exists {
			return fmt.Errorf("docusign scenario: duplicate envelope id %q", envelope.EnvelopeId)
		}
		envelopes[envelope.EnvelopeId] = struct{}{}
		if err := validEnvelopeTimes(envelope); err != nil {
			return fmt.Errorf("docusign scenario: envelopes[%d]: %w", i, err)
		}
		if envelope.SenderUserId != "" {
			if _, ok := users[envelope.SenderUserId]; !ok {
				return fmt.Errorf("docusign scenario: envelopes[%d] references unknown sender %q", i, envelope.SenderUserId)
			}
		}
		if envelope.Status != "created" && (len(envelope.Documents) == 0 || len(envelope.Signers) == 0) {
			return fmt.Errorf("docusign scenario: envelopes[%d] status %s requires documents and signers", i, envelope.Status)
		}
		documents := map[string]struct{}{}
		for j, document := range envelope.Documents {
			if _, exists := documents[document.DocumentId]; exists {
				return fmt.Errorf("docusign scenario: envelopes[%d] duplicate document id %q", i, document.DocumentId)
			}
			documents[document.DocumentId] = struct{}{}
			if document.DocumentBase64 != "" {
				if _, err := base64.StdEncoding.DecodeString(document.DocumentBase64); err != nil {
					return fmt.Errorf("docusign scenario: envelopes[%d].documents[%d].documentBase64: %w", i, j, err)
				}
			}
		}
		signers := map[string]struct{}{}
		for j, signer := range envelope.Signers {
			if _, err := mail.ParseAddress(signer.Email); err != nil {
				return fmt.Errorf("docusign scenario: envelopes[%d].signers[%d].email: %w", i, j, err)
			}
			if _, err := strconv.Atoi(signer.RoutingOrder); err != nil {
				return fmt.Errorf("docusign scenario: envelopes[%d].signers[%d].routingOrder: %w", i, j, err)
			}
			if _, exists := signers[signer.RecipientId]; exists {
				return fmt.Errorf("docusign scenario: envelopes[%d] duplicate recipient id %q", i, signer.RecipientId)
			}
			signers[signer.RecipientId] = struct{}{}
			if err := validSignerTimes(signer); err != nil {
				return fmt.Errorf("docusign scenario: envelopes[%d].signers[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("docusign scenario: initialize: %w", err)
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
		return fmt.Errorf("docusign scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"signers", "documents", "envelopes", "users", "account"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("docusign scenario: clear %s: %w", table, err)
		}
	}
	account := state.Account
	if _, err := tx.ExecContext(ctx, `INSERT INTO account
		(account_id, account_name, external_account_id, currency_code, plan_name, created_date)
		VALUES(?, ?, ?, ?, ?, ?)`,
		account.AccountId, account.AccountName, account.ExternalAccountId, account.CurrencyCode, account.PlanName, account.CreatedDate); err != nil {
		return fmt.Errorf("docusign scenario: insert account: %w", err)
	}
	for _, user := range state.Users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(user_id, user_name, email, user_status, is_admin) VALUES(?, ?, ?, ?, ?)`,
			user.UserId, user.UserName, user.Email, user.UserStatus, user.IsAdmin); err != nil {
			return fmt.Errorf("docusign scenario: insert user %s: %w", user.UserId, err)
		}
	}
	for _, envelope := range state.Envelopes {
		if err := insertEnvelopeTx(ctx, tx, envelope); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("docusign scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	account, err := loadAccount(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	users, err := loadUsers(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	envelopes, err := loadEnvelopes(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	if users == nil {
		users = []fixtureUser{}
	}
	if envelopes == nil {
		envelopes = []fixtureEnvelope{}
	}
	raw, err := json.Marshal(fixtureState{Account: account, Users: users, Envelopes: envelopes})
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "docusign", ResourceVersion: "v2.1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("docusign scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("docusign scenario: state has trailing data")
	}
	if state.Users == nil || state.Envelopes == nil {
		return fixtureState{}, fmt.Errorf("docusign scenario: users and envelopes are required arrays")
	}
	for i := range state.Envelopes {
		if state.Envelopes[i].Documents == nil || state.Envelopes[i].Signers == nil {
			return fixtureState{}, fmt.Errorf("docusign scenario: envelopes[%d] documents and signers are required arrays", i)
		}
	}
	return state, nil
}

func loadAccount(ctx context.Context, db *sql.DB) (fixtureAccount, error) {
	var account fixtureAccount
	err := db.QueryRowContext(ctx, `SELECT account_id, account_name, external_account_id, currency_code, plan_name, created_date FROM account`).
		Scan(&account.AccountId, &account.AccountName, &account.ExternalAccountId, &account.CurrencyCode, &account.PlanName, &account.CreatedDate)
	if err != nil {
		return fixtureAccount{}, fmt.Errorf("docusign scenario: load account: %w", err)
	}
	return account, nil
}

func loadUsers(ctx context.Context, db *sql.DB) ([]fixtureUser, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, user_name, email, user_status, is_admin FROM users ORDER BY user_id`)
	if err != nil {
		return nil, fmt.Errorf("docusign scenario: load users: %w", err)
	}
	defer rows.Close()
	users := []fixtureUser{}
	for rows.Next() {
		var user fixtureUser
		if err := rows.Scan(&user.UserId, &user.UserName, &user.Email, &user.UserStatus, &user.IsAdmin); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func loadUser(ctx context.Context, db *sql.DB, id string) (fixtureUser, error) {
	var user fixtureUser
	err := db.QueryRowContext(ctx, `SELECT user_id, user_name, email, user_status, is_admin FROM users WHERE user_id=?`, id).
		Scan(&user.UserId, &user.UserName, &user.Email, &user.UserStatus, &user.IsAdmin)
	if err != nil {
		return fixtureUser{}, err
	}
	return user, nil
}

func loadEnvelopes(ctx context.Context, db *sql.DB) ([]fixtureEnvelope, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+envelopeColumns+` FROM envelopes ORDER BY envelope_id`)
	if err != nil {
		return nil, fmt.Errorf("docusign scenario: load envelopes: %w", err)
	}
	defer rows.Close()
	envelopes := []fixtureEnvelope{}
	for rows.Next() {
		envelope, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range envelopes {
		documents, err := loadDocuments(ctx, db, envelopes[i].EnvelopeId)
		if err != nil {
			return nil, err
		}
		signers, err := loadSigners(ctx, db, envelopes[i].EnvelopeId)
		if err != nil {
			return nil, err
		}
		envelopes[i].Documents = documents
		envelopes[i].Signers = signers
	}
	return envelopes, nil
}

func loadEnvelope(ctx context.Context, db *sql.DB, id string) (fixtureEnvelope, error) {
	envelope, err := scanEnvelope(db.QueryRowContext(ctx, `SELECT `+envelopeColumns+` FROM envelopes WHERE envelope_id=?`, id))
	if err != nil {
		return fixtureEnvelope{}, err
	}
	envelope.Documents, err = loadDocuments(ctx, db, id)
	if err != nil {
		return fixtureEnvelope{}, err
	}
	envelope.Signers, err = loadSigners(ctx, db, id)
	if err != nil {
		return fixtureEnvelope{}, err
	}
	return envelope, nil
}

const envelopeColumns = `envelope_id, status, email_subject, email_blurb, created_date_time, sent_date_time, delivered_date_time, completed_date_time, status_changed_date_time, voided_date_time, voided_reason, sender_user_id`

func scanEnvelope(row interface{ Scan(...any) error }) (fixtureEnvelope, error) {
	var envelope fixtureEnvelope
	err := row.Scan(&envelope.EnvelopeId, &envelope.Status, &envelope.EmailSubject, &envelope.EmailBlurb,
		&envelope.CreatedDateTime, &envelope.SentDateTime, &envelope.DeliveredDateTime, &envelope.CompletedDateTime,
		&envelope.StatusChangedDateTime, &envelope.VoidedDateTime, &envelope.VoidedReason, &envelope.SenderUserId)
	return envelope, err
}

func loadDocuments(ctx context.Context, db *sql.DB, envelopeID string) ([]fixtureDocument, error) {
	rows, err := db.QueryContext(ctx, `SELECT document_id, name, file_extension, doc_order, document_base64
		FROM documents WHERE envelope_id=? ORDER BY document_id`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := []fixtureDocument{}
	for rows.Next() {
		var document fixtureDocument
		if err := rows.Scan(&document.DocumentId, &document.Name, &document.FileExtension, &document.Order, &document.DocumentBase64); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func loadSigners(ctx context.Context, db *sql.DB, envelopeID string) ([]fixtureSigner, error) {
	rows, err := db.QueryContext(ctx, `SELECT recipient_id, name, email, routing_order, status, sent_date_time, delivered_date_time, signed_date_time
		FROM signers WHERE envelope_id=? ORDER BY recipient_id`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	signers := []fixtureSigner{}
	for rows.Next() {
		var signer fixtureSigner
		if err := rows.Scan(&signer.RecipientId, &signer.Name, &signer.Email, &signer.RoutingOrder, &signer.Status,
			&signer.SentDateTime, &signer.DeliveredDateTime, &signer.SignedDateTime); err != nil {
			return nil, err
		}
		signers = append(signers, signer)
	}
	return signers, rows.Err()
}

func insertEnvelope(ctx context.Context, db *sql.DB, envelope fixtureEnvelope) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertEnvelopeTx(ctx, tx, envelope); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEnvelopeTx(ctx context.Context, tx *sql.Tx, envelope fixtureEnvelope) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO envelopes
		(envelope_id, status, email_subject, email_blurb, created_date_time, sent_date_time, delivered_date_time, completed_date_time, status_changed_date_time, voided_date_time, voided_reason, sender_user_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		envelope.EnvelopeId, envelope.Status, envelope.EmailSubject, envelope.EmailBlurb,
		envelope.CreatedDateTime, envelope.SentDateTime, envelope.DeliveredDateTime, envelope.CompletedDateTime,
		envelope.StatusChangedDateTime, envelope.VoidedDateTime, envelope.VoidedReason, envelope.SenderUserId); err != nil {
		return fmt.Errorf("docusign scenario: insert envelope %s: %w", envelope.EnvelopeId, err)
	}
	for _, document := range envelope.Documents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO documents
			(envelope_id, document_id, name, file_extension, doc_order, document_base64)
			VALUES(?, ?, ?, ?, ?, ?)`,
			envelope.EnvelopeId, document.DocumentId, document.Name, document.FileExtension, document.Order, document.DocumentBase64); err != nil {
			return fmt.Errorf("docusign scenario: insert document %s: %w", document.DocumentId, err)
		}
	}
	for _, signer := range envelope.Signers {
		if err := insertSignerTx(ctx, tx, envelope.EnvelopeId, signer); err != nil {
			return err
		}
	}
	return nil
}

func insertSignerTx(ctx context.Context, tx *sql.Tx, envelopeID string, signer fixtureSigner) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO signers
		(envelope_id, recipient_id, name, email, routing_order, status, sent_date_time, delivered_date_time, signed_date_time)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		envelopeID, signer.RecipientId, signer.Name, signer.Email, signer.RoutingOrder, signer.Status,
		signer.SentDateTime, signer.DeliveredDateTime, signer.SignedDateTime); err != nil {
		return fmt.Errorf("docusign scenario: insert signer %s: %w", signer.RecipientId, err)
	}
	return nil
}

func updateEnvelope(ctx context.Context, db *sql.DB, envelope fixtureEnvelope) error {
	_, err := db.ExecContext(ctx, `UPDATE envelopes SET
		status=?, email_subject=?, email_blurb=?, created_date_time=?, sent_date_time=?, delivered_date_time=?,
		completed_date_time=?, status_changed_date_time=?, voided_date_time=?, voided_reason=?, sender_user_id=?
		WHERE envelope_id=?`,
		envelope.Status, envelope.EmailSubject, envelope.EmailBlurb, envelope.CreatedDateTime, envelope.SentDateTime,
		envelope.DeliveredDateTime, envelope.CompletedDateTime, envelope.StatusChangedDateTime, envelope.VoidedDateTime,
		envelope.VoidedReason, envelope.SenderUserId, envelope.EnvelopeId)
	return err
}

func updateSigners(ctx context.Context, db *sql.DB, envelopeID string, signers []fixtureSigner) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, signer := range signers {
		if _, err := tx.ExecContext(ctx, `UPDATE signers SET name=?, email=?, routing_order=?, status=?, sent_date_time=?, delivered_date_time=?, signed_date_time=?
			WHERE envelope_id=? AND recipient_id=?`,
			signer.Name, signer.Email, signer.RoutingOrder, signer.Status, signer.SentDateTime, signer.DeliveredDateTime, signer.SignedDateTime,
			envelopeID, signer.RecipientId); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func defaultSenderID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT user_id FROM users ORDER BY user_id LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func validEnvelopeTimes(envelope fixtureEnvelope) error {
	fields := []struct {
		name  string
		value string
	}{
		{"createdDateTime", envelope.CreatedDateTime},
		{"sentDateTime", envelope.SentDateTime},
		{"deliveredDateTime", envelope.DeliveredDateTime},
		{"completedDateTime", envelope.CompletedDateTime},
		{"statusChangedDateTime", envelope.StatusChangedDateTime},
		{"voidedDateTime", envelope.VoidedDateTime},
	}
	for _, field := range fields {
		if err := validTimestamp(field.value, true); err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}
	return nil
}

func validSignerTimes(signer fixtureSigner) error {
	fields := []struct {
		name  string
		value string
	}{
		{"sentDateTime", signer.SentDateTime},
		{"deliveredDateTime", signer.DeliveredDateTime},
		{"signedDateTime", signer.SignedDateTime},
	}
	for _, field := range fields {
		if err := validTimestamp(field.value, true); err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}
	return nil
}

func validTimestamp(value string, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("required")
	}
	if _, err := parseAPITime(value); err != nil {
		return err
	}
	return nil
}

func parseAPITime(value string) (time.Time, error) {
	layouts := []string{
		"2006-01-02T15:04:05.0000000Z",
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func formatAPITime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.0000000Z")
}

func ContractName() string { return scenario.Contract }
