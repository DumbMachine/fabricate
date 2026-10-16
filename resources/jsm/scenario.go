package jsm

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
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
		panic(fmt.Sprintf("jsm: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("jsm-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("jsm: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("jsm-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("jsm: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CurrentUserAccountID string               `json:"currentUserAccountId"`
	ServiceDesks         []fixtureServiceDesk `json:"serviceDesks"`
	RequestTypes         []fixtureRequestType `json:"requestTypes"`
	Users                []fixtureUser        `json:"users"`
	Requests             []fixtureRequest     `json:"requests"`
	Comments             []fixtureComment     `json:"comments"`
}

type fixtureServiceDesk struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
}

type fixtureRequestType struct {
	ID            string `json:"id"`
	ServiceDeskID string `json:"serviceDeskId"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	HelpText      string `json:"helpText"`
	IssueTypeID   string `json:"issueTypeId"`
}

type fixtureUser struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	DisplayName  string `json:"displayName"`
	Active       bool   `json:"active"`
	TimeZone     string `json:"timeZone"`
}

type fixtureRequest struct {
	IssueID               string   `json:"issueId"`
	IssueKey              string   `json:"issueKey"`
	ServiceDeskID         string   `json:"serviceDeskId"`
	RequestTypeID         string   `json:"requestTypeId"`
	Summary               string   `json:"summary"`
	Description           string   `json:"description"`
	ReporterAccountID     string   `json:"reporterAccountId"`
	Status                string   `json:"status"`
	StatusCategory        string   `json:"statusCategory"`
	CreatedAt             string   `json:"createdAt"`
	UpdatedAt             string   `json:"updatedAt"`
	ParticipantAccountIDs []string `json:"participantAccountIds"`
}

type fixtureComment struct {
	ID              string `json:"id"`
	IssueID         string `json:"issueId"`
	Body            string `json:"body"`
	Public          bool   `json:"public"`
	AuthorAccountID string `json:"authorAccountId"`
	CreatedAt       string `json:"createdAt"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "jsm" || doc.ResourceVersion != "3" {
		return fmt.Errorf("jsm scenario: expected resource jsm 3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("jsm scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("jsm scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	desks := map[string]fixtureServiceDesk{}
	projectIDs := map[string]struct{}{}
	projectKeys := map[string]struct{}{}
	for _, desk := range state.ServiceDesks {
		if _, exists := desks[desk.ID]; exists {
			return fmt.Errorf("jsm scenario: duplicate service desk id %q", desk.ID)
		}
		if _, exists := projectIDs[desk.ProjectID]; exists {
			return fmt.Errorf("jsm scenario: duplicate project id %q", desk.ProjectID)
		}
		if _, exists := projectKeys[desk.ProjectKey]; exists {
			return fmt.Errorf("jsm scenario: duplicate project key %q", desk.ProjectKey)
		}
		desks[desk.ID] = desk
		projectIDs[desk.ProjectID] = struct{}{}
		projectKeys[desk.ProjectKey] = struct{}{}
	}
	types := map[string]fixtureRequestType{}
	for _, kind := range state.RequestTypes {
		if _, exists := types[kind.ID]; exists {
			return fmt.Errorf("jsm scenario: duplicate request type id %q", kind.ID)
		}
		if _, exists := desks[kind.ServiceDeskID]; !exists {
			return fmt.Errorf("jsm scenario: request type %q references unknown service desk %q", kind.ID, kind.ServiceDeskID)
		}
		types[kind.ID] = kind
	}
	users := map[string]fixtureUser{}
	emails := map[string]struct{}{}
	for _, user := range state.Users {
		if _, exists := users[user.AccountID]; exists {
			return fmt.Errorf("jsm scenario: duplicate account id %q", user.AccountID)
		}
		if _, err := mail.ParseAddress(user.EmailAddress); err != nil {
			return fmt.Errorf("jsm scenario: user %q email: %w", user.AccountID, err)
		}
		if _, exists := emails[user.EmailAddress]; exists {
			return fmt.Errorf("jsm scenario: duplicate email %q", user.EmailAddress)
		}
		users[user.AccountID] = user
		emails[user.EmailAddress] = struct{}{}
	}
	if _, exists := users[state.CurrentUserAccountID]; !exists {
		return fmt.Errorf("jsm scenario: current user %q is not in users", state.CurrentUserAccountID)
	}
	requests := map[string]fixtureRequest{}
	keys := map[string]struct{}{}
	for _, request := range state.Requests {
		if _, exists := requests[request.IssueID]; exists {
			return fmt.Errorf("jsm scenario: duplicate issue id %q", request.IssueID)
		}
		if _, exists := keys[request.IssueKey]; exists {
			return fmt.Errorf("jsm scenario: duplicate issue key %q", request.IssueKey)
		}
		kind, exists := types[request.RequestTypeID]
		if !exists {
			return fmt.Errorf("jsm scenario: request %q references unknown request type %q", request.IssueKey, request.RequestTypeID)
		}
		if kind.ServiceDeskID != request.ServiceDeskID {
			return fmt.Errorf("jsm scenario: request %q request type is not on service desk %q", request.IssueKey, request.ServiceDeskID)
		}
		if _, exists := desks[request.ServiceDeskID]; !exists {
			return fmt.Errorf("jsm scenario: request %q references unknown service desk %q", request.IssueKey, request.ServiceDeskID)
		}
		if _, exists := users[request.ReporterAccountID]; !exists {
			return fmt.Errorf("jsm scenario: request %q references unknown reporter %q", request.IssueKey, request.ReporterAccountID)
		}
		if _, err := time.Parse(time.RFC3339, request.CreatedAt); err != nil {
			return fmt.Errorf("jsm scenario: request %q createdAt: %w", request.IssueKey, err)
		}
		if _, err := time.Parse(time.RFC3339, request.UpdatedAt); err != nil {
			return fmt.Errorf("jsm scenario: request %q updatedAt: %w", request.IssueKey, err)
		}
		seen := map[string]struct{}{}
		for _, accountID := range request.ParticipantAccountIDs {
			if _, exists := users[accountID]; !exists {
				return fmt.Errorf("jsm scenario: request %q references unknown participant %q", request.IssueKey, accountID)
			}
			if _, exists := seen[accountID]; exists {
				return fmt.Errorf("jsm scenario: request %q repeats participant %q", request.IssueKey, accountID)
			}
			seen[accountID] = struct{}{}
		}
		requests[request.IssueID] = request
		keys[request.IssueKey] = struct{}{}
	}
	commentIDs := map[string]struct{}{}
	for _, comment := range state.Comments {
		if _, exists := commentIDs[comment.ID]; exists {
			return fmt.Errorf("jsm scenario: duplicate comment id %q", comment.ID)
		}
		if _, exists := requests[comment.IssueID]; !exists {
			return fmt.Errorf("jsm scenario: comment %q references unknown issue %q", comment.ID, comment.IssueID)
		}
		if _, exists := users[comment.AuthorAccountID]; !exists {
			return fmt.Errorf("jsm scenario: comment %q references unknown author %q", comment.ID, comment.AuthorAccountID)
		}
		if _, err := time.Parse(time.RFC3339, comment.CreatedAt); err != nil {
			return fmt.Errorf("jsm scenario: comment %q createdAt: %w", comment.ID, err)
		}
		commentIDs[comment.ID] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("jsm scenario: initialize: %w", err)
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
		return fmt.Errorf("jsm scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"comments", "requests", "request_types", "users", "service_desks", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("jsm scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentUserAccountId', ?)", state.CurrentUserAccountID); err != nil {
		return fmt.Errorf("jsm scenario: insert metadata: %w", err)
	}
	for _, desk := range state.ServiceDesks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_desks(id, project_id, project_key, project_name) VALUES(?, ?, ?, ?)`,
			desk.ID, desk.ProjectID, desk.ProjectKey, desk.ProjectName); err != nil {
			return fmt.Errorf("jsm scenario: insert service desk %s: %w", desk.ID, err)
		}
	}
	for _, user := range state.Users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(account_id, email_address, display_name, active, time_zone) VALUES(?, ?, ?, ?, ?)`,
			user.AccountID, user.EmailAddress, user.DisplayName, boolInt(user.Active), user.TimeZone); err != nil {
			return fmt.Errorf("jsm scenario: insert user %s: %w", user.AccountID, err)
		}
	}
	for _, kind := range state.RequestTypes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO request_types(id, service_desk_id, name, description, help_text, issue_type_id) VALUES(?, ?, ?, ?, ?, ?)`,
			kind.ID, kind.ServiceDeskID, kind.Name, kind.Description, kind.HelpText, kind.IssueTypeID); err != nil {
			return fmt.Errorf("jsm scenario: insert request type %s: %w", kind.ID, err)
		}
	}
	for _, request := range state.Requests {
		participants, err := json.Marshal(request.ParticipantAccountIDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO requests(
			issue_id, issue_key, service_desk_id, request_type_id, summary, description,
			reporter_account_id, status, status_category, created_at, updated_at, participant_account_ids)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.IssueID, request.IssueKey, request.ServiceDeskID, request.RequestTypeID, request.Summary, request.Description,
			request.ReporterAccountID, request.Status, request.StatusCategory, request.CreatedAt, request.UpdatedAt, string(participants)); err != nil {
			return fmt.Errorf("jsm scenario: insert request %s: %w", request.IssueKey, err)
		}
	}
	for _, comment := range state.Comments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, is_public, author_account_id, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
			comment.ID, comment.IssueID, comment.Body, boolInt(comment.Public), comment.AuthorAccountID, comment.CreatedAt); err != nil {
			return fmt.Errorf("jsm scenario: insert comment %s: %w", comment.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("jsm scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserAccountId'").Scan(&state.CurrentUserAccountID); err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump current user: %w", err)
	}
	deskRows, err := db.QueryContext(ctx, "SELECT id, project_id, project_key, project_name FROM service_desks ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump service desks: %w", err)
	}
	for deskRows.Next() {
		var desk fixtureServiceDesk
		if err := deskRows.Scan(&desk.ID, &desk.ProjectID, &desk.ProjectKey, &desk.ProjectName); err != nil {
			deskRows.Close()
			return scenario.Document{}, err
		}
		state.ServiceDesks = append(state.ServiceDesks, desk)
	}
	if err := deskRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	typeRows, err := db.QueryContext(ctx, "SELECT id, service_desk_id, name, description, help_text, issue_type_id FROM request_types ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump request types: %w", err)
	}
	for typeRows.Next() {
		var kind fixtureRequestType
		if err := typeRows.Scan(&kind.ID, &kind.ServiceDeskID, &kind.Name, &kind.Description, &kind.HelpText, &kind.IssueTypeID); err != nil {
			typeRows.Close()
			return scenario.Document{}, err
		}
		state.RequestTypes = append(state.RequestTypes, kind)
	}
	if err := typeRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	userRows, err := db.QueryContext(ctx, "SELECT account_id, email_address, display_name, active, time_zone FROM users ORDER BY account_id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump users: %w", err)
	}
	for userRows.Next() {
		var user fixtureUser
		var active int
		if err := userRows.Scan(&user.AccountID, &user.EmailAddress, &user.DisplayName, &active, &user.TimeZone); err != nil {
			userRows.Close()
			return scenario.Document{}, err
		}
		user.Active = active != 0
		state.Users = append(state.Users, user)
	}
	if err := userRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	requestRows, err := db.QueryContext(ctx, `SELECT issue_id, issue_key, service_desk_id, request_type_id, summary, description,
		reporter_account_id, status, status_category, created_at, updated_at, participant_account_ids
		FROM requests ORDER BY issue_key`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump requests: %w", err)
	}
	for requestRows.Next() {
		var request fixtureRequest
		var participants string
		if err := requestRows.Scan(&request.IssueID, &request.IssueKey, &request.ServiceDeskID, &request.RequestTypeID,
			&request.Summary, &request.Description, &request.ReporterAccountID, &request.Status, &request.StatusCategory,
			&request.CreatedAt, &request.UpdatedAt, &participants); err != nil {
			requestRows.Close()
			return scenario.Document{}, err
		}
		ids, err := decodeIDs(participants)
		if err != nil {
			requestRows.Close()
			return scenario.Document{}, fmt.Errorf("jsm scenario: dump request %s participants: %w", request.IssueKey, err)
		}
		request.ParticipantAccountIDs = ids
		state.Requests = append(state.Requests, request)
	}
	if err := requestRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	commentRows, err := db.QueryContext(ctx, "SELECT id, issue_id, body, is_public, author_account_id, created_at FROM comments ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("jsm scenario: dump comments: %w", err)
	}
	defer commentRows.Close()
	for commentRows.Next() {
		var comment fixtureComment
		var public int
		if err := commentRows.Scan(&comment.ID, &comment.IssueID, &comment.Body, &public, &comment.AuthorAccountID, &comment.CreatedAt); err != nil {
			return scenario.Document{}, err
		}
		comment.Public = public != 0
		state.Comments = append(state.Comments, comment)
	}
	if err := commentRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	state.ServiceDesks = emptyServiceDesks(state.ServiceDesks)
	state.RequestTypes = emptyRequestTypes(state.RequestTypes)
	state.Users = emptyUsers(state.Users)
	state.Requests = emptyRequests(state.Requests)
	state.Comments = emptyComments(state.Comments)
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "jsm", ResourceVersion: "3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("jsm scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("jsm scenario: state has trailing data")
	}
	if state.ServiceDesks == nil || state.RequestTypes == nil || state.Users == nil || state.Requests == nil || state.Comments == nil {
		return fixtureState{}, fmt.Errorf("jsm scenario: serviceDesks, requestTypes, users, requests, and comments are required arrays")
	}
	for i := range state.Requests {
		if state.Requests[i].ParticipantAccountIDs == nil {
			return fixtureState{}, fmt.Errorf("jsm scenario: requests[%d].participantAccountIds is required", i)
		}
	}
	return state, nil
}

func decodeIDs(raw string) ([]string, error) {
	if raw == "" || raw == "null" {
		return []string{}, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	if ids == nil {
		return []string{}, nil
	}
	return ids, nil
}

func emptyServiceDesks(values []fixtureServiceDesk) []fixtureServiceDesk {
	if values == nil {
		return []fixtureServiceDesk{}
	}
	return values
}

func emptyRequestTypes(values []fixtureRequestType) []fixtureRequestType {
	if values == nil {
		return []fixtureRequestType{}
	}
	return values
}

func emptyUsers(values []fixtureUser) []fixtureUser {
	if values == nil {
		return []fixtureUser{}
	}
	return values
}

func emptyRequests(values []fixtureRequest) []fixtureRequest {
	if values == nil {
		return []fixtureRequest{}
	}
	for i := range values {
		if values[i].ParticipantAccountIDs == nil {
			values[i].ParticipantAccountIDs = []string{}
		}
	}
	return values
}

func emptyComments(values []fixtureComment) []fixtureComment {
	if values == nil {
		return []fixtureComment{}
	}
	return values
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }
