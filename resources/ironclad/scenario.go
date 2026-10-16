package ironclad

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"
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
		panic(fmt.Sprintf("ironclad: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("ironclad-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("ironclad: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("ironclad-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("ironclad: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CurrentUser fixtureUser       `json:"currentUser"`
	Workflows   []fixtureWorkflow `json:"workflows"`
	Records     []fixtureRecord   `json:"records"`
	Comments    []fixtureComment  `json:"comments"`
	Approvals   []fixtureApproval `json:"approvals"`
}

type fixtureUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Title       string `json:"title"`
	CompanyName string `json:"companyName"`
}

type fixtureWorkflow struct {
	ID                string   `json:"id"`
	IroncladID        string   `json:"ironcladId"`
	Title             string   `json:"title"`
	Template          string   `json:"template"`
	Step              string   `json:"step"`
	Status            string   `json:"status"`
	IsCancelled       bool     `json:"isCancelled"`
	IsComplete        bool     `json:"isComplete"`
	CounterpartyName  string   `json:"counterpartyName"`
	CounterpartyEmail string   `json:"counterpartyEmail,omitempty"`
	EnvelopeID        string   `json:"docusignEnvelopeId,omitempty"`
	Filename          string   `json:"filename,omitempty"`
	RecordIDs         []string `json:"recordIds"`
	RoleID            string   `json:"roleId"`
	RoleDisplayName   string   `json:"roleDisplayName"`
	AssigneeID        string   `json:"assigneeId"`
	Created           string   `json:"created"`
	LastUpdated       string   `json:"lastUpdated"`
	CreatorID         string   `json:"creatorId"`
}

type fixtureRecord struct {
	ID               string `json:"id"`
	IroncladID       string `json:"ironcladId"`
	Type             string `json:"type"`
	Name             string `json:"name"`
	LastUpdated      string `json:"lastUpdated"`
	CounterpartyName string `json:"counterpartyName"`
	EnvelopeID       string `json:"docusignEnvelopeId,omitempty"`
	Filename         string `json:"filename,omitempty"`
	WorkflowID       string `json:"workflowId"`
	ContractStatus   string `json:"contractStatus"`
	EnhancedStatus   string `json:"enhancedStatus"`
}

type fixtureComment struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflowId"`
	Message    string `json:"commentMessage"`
	Timestamp  string `json:"timestamp"`
	AuthorID   string `json:"authorId"`
	RepliedTo  string `json:"repliedTo,omitempty"`
}

type fixtureApproval struct {
	WorkflowID     string                 `json:"workflowId"`
	ApprovalGroups []fixtureApprovalGroup `json:"approvalGroups"`
}

type fixtureApprovalGroup struct {
	Role           string `json:"role"`
	DisplayName    string `json:"displayName"`
	ReviewerType   string `json:"reviewerType"`
	ReviewerStatus string `json:"reviewerStatus"`
	Status         string `json:"status"`
	Order          int    `json:"order"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "ironclad" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("ironclad scenario: expected resource ironclad v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("ironclad scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("ironclad scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if _, err := mail.ParseAddress(state.CurrentUser.Email); err != nil {
		return fmt.Errorf("ironclad scenario: currentUser.email: %w", err)
	}
	workflows := map[string]fixtureWorkflow{}
	seen := map[string]struct{}{}
	for i, workflow := range state.Workflows {
		if _, exists := seen[workflow.ID]; exists {
			return fmt.Errorf("ironclad scenario: duplicate workflow id %q", workflow.ID)
		}
		if _, exists := seen[workflow.IroncladID]; exists {
			return fmt.Errorf("ironclad scenario: workflow %q ironcladId %q collides with another workflow identifier", workflow.ID, workflow.IroncladID)
		}
		seen[workflow.ID] = struct{}{}
		seen[workflow.IroncladID] = struct{}{}
		if err := validateWorkflow(i, workflow, state.CurrentUser.ID); err != nil {
			return err
		}
		workflows[workflow.ID] = workflow
	}
	records := map[string]fixtureRecord{}
	seenRecords := map[string]struct{}{}
	for i, record := range state.Records {
		if _, exists := seenRecords[record.ID]; exists {
			return fmt.Errorf("ironclad scenario: duplicate record id %q", record.ID)
		}
		if _, exists := seenRecords[record.IroncladID]; exists {
			return fmt.Errorf("ironclad scenario: record %q ironcladId %q collides with another record identifier", record.ID, record.IroncladID)
		}
		seenRecords[record.ID] = struct{}{}
		seenRecords[record.IroncladID] = struct{}{}
		workflow, ok := workflows[record.WorkflowID]
		if !ok {
			return fmt.Errorf("ironclad scenario: records[%d] references unknown workflow %q", i, record.WorkflowID)
		}
		if err := validateTimestamp(record.LastUpdated); err != nil {
			return fmt.Errorf("ironclad scenario: records[%d].lastUpdated: %w", i, err)
		}
		if record.CounterpartyName != workflow.CounterpartyName {
			return fmt.Errorf("ironclad scenario: record %q counterparty %q does not match workflow %q", record.ID, record.CounterpartyName, workflow.ID)
		}
		if record.EnvelopeID != "" && workflow.EnvelopeID != "" && record.EnvelopeID != workflow.EnvelopeID {
			return fmt.Errorf("ironclad scenario: record %q envelope %q does not match workflow %q", record.ID, record.EnvelopeID, workflow.ID)
		}
		if record.Filename != "" && workflow.Filename != "" && record.Filename != workflow.Filename {
			return fmt.Errorf("ironclad scenario: record %q filename %q does not match workflow %q", record.ID, record.Filename, workflow.ID)
		}
		records[record.ID] = record
	}
	for id, workflow := range workflows {
		linked := map[string]struct{}{}
		for _, recordID := range workflow.RecordIDs {
			record, ok := records[recordID]
			if !ok || record.WorkflowID != id {
				return fmt.Errorf("ironclad scenario: workflow %q recordIds entry %q is not a record of that workflow", id, recordID)
			}
			if _, exists := linked[recordID]; exists {
				return fmt.Errorf("ironclad scenario: workflow %q repeats record %q", id, recordID)
			}
			linked[recordID] = struct{}{}
		}
		for _, record := range records {
			if record.WorkflowID != id {
				continue
			}
			if _, ok := linked[record.ID]; !ok {
				return fmt.Errorf("ironclad scenario: record %q is missing from workflow %q recordIds", record.ID, id)
			}
		}
	}
	seenComments := map[string]struct{}{}
	for i, comment := range state.Comments {
		if _, exists := seenComments[comment.ID]; exists {
			return fmt.Errorf("ironclad scenario: duplicate comment id %q", comment.ID)
		}
		seenComments[comment.ID] = struct{}{}
		if _, ok := workflows[comment.WorkflowID]; !ok {
			return fmt.Errorf("ironclad scenario: comments[%d] references unknown workflow %q", i, comment.WorkflowID)
		}
		if comment.AuthorID != state.CurrentUser.ID {
			return fmt.Errorf("ironclad scenario: comments[%d] author %q is not the current user", i, comment.AuthorID)
		}
		if strings.TrimSpace(comment.Message) == "" {
			return fmt.Errorf("ironclad scenario: comments[%d] commentMessage is empty", i)
		}
		if err := validateTimestamp(comment.Timestamp); err != nil {
			return fmt.Errorf("ironclad scenario: comments[%d].timestamp: %w", i, err)
		}
	}
	seenApprovals := map[string]struct{}{}
	for i, approval := range state.Approvals {
		if _, ok := workflows[approval.WorkflowID]; !ok {
			return fmt.Errorf("ironclad scenario: approvals[%d] references unknown workflow %q", i, approval.WorkflowID)
		}
		if _, exists := seenApprovals[approval.WorkflowID]; exists {
			return fmt.Errorf("ironclad scenario: duplicate approvals for workflow %q", approval.WorkflowID)
		}
		seenApprovals[approval.WorkflowID] = struct{}{}
		roles := map[string]struct{}{}
		for j, group := range approval.ApprovalGroups {
			if _, exists := roles[group.Role]; exists {
				return fmt.Errorf("ironclad scenario: approvals[%d] repeats role %q", i, group.Role)
			}
			roles[group.Role] = struct{}{}
			if group.Status == "approved" && group.ReviewerStatus != "approved" {
				return fmt.Errorf("ironclad scenario: approvals[%d].approvalGroups[%d] is approved but the reviewer is %s", i, j, group.ReviewerStatus)
			}
			if group.Status != "approved" && group.ReviewerStatus == "approved" {
				return fmt.Errorf("ironclad scenario: approvals[%d].approvalGroups[%d] reviewer is approved while the group is %s", i, j, group.Status)
			}
		}
	}
	for id := range workflows {
		if _, ok := seenApprovals[id]; !ok {
			return fmt.Errorf("ironclad scenario: workflow %q has no approvals", id)
		}
	}
	return nil
}

func validateWorkflow(index int, workflow fixtureWorkflow, currentUserID string) error {
	if workflow.CounterpartyEmail != "" {
		if _, err := mail.ParseAddress(workflow.CounterpartyEmail); err != nil {
			return fmt.Errorf("ironclad scenario: workflows[%d].counterpartyEmail: %w", index, err)
		}
	}
	if err := validateTimestamp(workflow.Created); err != nil {
		return fmt.Errorf("ironclad scenario: workflows[%d].created: %w", index, err)
	}
	if err := validateTimestamp(workflow.LastUpdated); err != nil {
		return fmt.Errorf("ironclad scenario: workflows[%d].lastUpdated: %w", index, err)
	}
	if workflow.AssigneeID != currentUserID {
		return fmt.Errorf("ironclad scenario: workflows[%d] assignee %q is not the current user", index, workflow.AssigneeID)
	}
	if workflow.CreatorID != currentUserID {
		return fmt.Errorf("ironclad scenario: workflows[%d] creator %q is not the current user", index, workflow.CreatorID)
	}
	switch workflow.Status {
	case "completed":
		if workflow.Step != "Complete" || !workflow.IsComplete || workflow.IsCancelled {
			return fmt.Errorf("ironclad scenario: completed workflow %q must be step Complete, isComplete, and not cancelled", workflow.ID)
		}
		if len(workflow.RecordIDs) == 0 {
			return fmt.Errorf("ironclad scenario: completed workflow %q requires recordIds", workflow.ID)
		}
	case "cancelled":
		if workflow.IsComplete || !workflow.IsCancelled || workflow.Step == "Complete" {
			return fmt.Errorf("ironclad scenario: cancelled workflow %q has an inconsistent step or flags", workflow.ID)
		}
	default:
		if workflow.IsComplete || workflow.IsCancelled || workflow.Step == "Complete" {
			return fmt.Errorf("ironclad scenario: %s workflow %q has an inconsistent step or flags", workflow.Status, workflow.ID)
		}
	}
	return nil
}

func validateTimestamp(value string) error {
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return err
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("ironclad scenario: initialize: %w", err)
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
		return fmt.Errorf("ironclad scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"comments", "approval_groups", "records", "workflows", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("ironclad scenario: clear %s: %w", table, err)
		}
	}
	user := state.CurrentUser
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key, value) VALUES
		('currentUserId', ?),
		('currentUserDisplayName', ?),
		('currentUserEmail', ?),
		('currentUserTitle', ?),
		('currentUserCompanyName', ?)`,
		user.ID, user.DisplayName, user.Email, user.Title, user.CompanyName); err != nil {
		return fmt.Errorf("ironclad scenario: insert current user: %w", err)
	}
	for _, workflow := range state.Workflows {
		recordIDs := workflow.RecordIDs
		if recordIDs == nil {
			recordIDs = []string{}
		}
		encoded, err := json.Marshal(recordIDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflows
			(id, ironclad_id, title, template, step, status, is_cancelled, is_complete, counterparty_name, counterparty_email, envelope_id, filename, record_ids, role_id, role_display_name, assignee_id, created, last_updated, creator_id)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			workflow.ID, workflow.IroncladID, workflow.Title, workflow.Template, workflow.Step, workflow.Status,
			boolInt(workflow.IsCancelled), boolInt(workflow.IsComplete), workflow.CounterpartyName, workflow.CounterpartyEmail,
			workflow.EnvelopeID, workflow.Filename, string(encoded), workflow.RoleID, workflow.RoleDisplayName, workflow.AssigneeID,
			workflow.Created, workflow.LastUpdated, workflow.CreatorID); err != nil {
			return fmt.Errorf("ironclad scenario: insert workflow %s: %w", workflow.ID, err)
		}
	}
	for _, record := range state.Records {
		if _, err := tx.ExecContext(ctx, `INSERT INTO records
			(id, ironclad_id, type, name, last_updated, counterparty_name, envelope_id, filename, workflow_id, contract_status, enhanced_status)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.IroncladID, record.Type, record.Name, record.LastUpdated, record.CounterpartyName,
			record.EnvelopeID, record.Filename, record.WorkflowID, record.ContractStatus, record.EnhancedStatus); err != nil {
			return fmt.Errorf("ironclad scenario: insert record %s: %w", record.ID, err)
		}
	}
	for _, comment := range state.Comments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, workflow_id, message, timestamp, author_id, replied_to)
			VALUES(?, ?, ?, ?, ?, ?)`,
			comment.ID, comment.WorkflowID, comment.Message, comment.Timestamp, comment.AuthorID, comment.RepliedTo); err != nil {
			return fmt.Errorf("ironclad scenario: insert comment %s: %w", comment.ID, err)
		}
	}
	for _, approval := range state.Approvals {
		for _, group := range approval.ApprovalGroups {
			if _, err := tx.ExecContext(ctx, `INSERT INTO approval_groups
				(workflow_id, role, display_name, reviewer_type, reviewer_status, status, sort_order)
				VALUES(?, ?, ?, ?, ?, ?, ?)`,
				approval.WorkflowID, group.Role, group.DisplayName, group.ReviewerType, group.ReviewerStatus, group.Status, group.Order); err != nil {
				return fmt.Errorf("ironclad scenario: insert approval %s/%s: %w", approval.WorkflowID, group.Role, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ironclad scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT value FROM metadata WHERE key='currentUserId'),
		(SELECT value FROM metadata WHERE key='currentUserDisplayName'),
		(SELECT value FROM metadata WHERE key='currentUserEmail'),
		(SELECT value FROM metadata WHERE key='currentUserTitle'),
		(SELECT value FROM metadata WHERE key='currentUserCompanyName')`).Scan(
		&state.CurrentUser.ID, &state.CurrentUser.DisplayName, &state.CurrentUser.Email, &state.CurrentUser.Title, &state.CurrentUser.CompanyName); err != nil {
		return scenario.Document{}, fmt.Errorf("ironclad scenario: dump current user: %w", err)
	}
	workflowRows, err := db.QueryContext(ctx, `SELECT id, ironclad_id, title, template, step, status, is_cancelled, is_complete,
		counterparty_name, counterparty_email, envelope_id, filename, record_ids, role_id, role_display_name, assignee_id, created, last_updated, creator_id
		FROM workflows ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ironclad scenario: dump workflows: %w", err)
	}
	for workflowRows.Next() {
		workflow, err := scanFixtureWorkflow(workflowRows)
		if err != nil {
			workflowRows.Close()
			return scenario.Document{}, err
		}
		state.Workflows = append(state.Workflows, workflow)
	}
	if err := workflowRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := workflowRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	recordRows, err := db.QueryContext(ctx, `SELECT id, ironclad_id, type, name, last_updated, counterparty_name, envelope_id, filename, workflow_id, contract_status, enhanced_status
		FROM records ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ironclad scenario: dump records: %w", err)
	}
	for recordRows.Next() {
		var record fixtureRecord
		if err := recordRows.Scan(&record.ID, &record.IroncladID, &record.Type, &record.Name, &record.LastUpdated, &record.CounterpartyName,
			&record.EnvelopeID, &record.Filename, &record.WorkflowID, &record.ContractStatus, &record.EnhancedStatus); err != nil {
			recordRows.Close()
			return scenario.Document{}, err
		}
		state.Records = append(state.Records, record)
	}
	if err := recordRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	commentRows, err := db.QueryContext(ctx, `SELECT id, workflow_id, message, timestamp, author_id, replied_to FROM comments ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ironclad scenario: dump comments: %w", err)
	}
	for commentRows.Next() {
		var comment fixtureComment
		if err := commentRows.Scan(&comment.ID, &comment.WorkflowID, &comment.Message, &comment.Timestamp, &comment.AuthorID, &comment.RepliedTo); err != nil {
			commentRows.Close()
			return scenario.Document{}, err
		}
		state.Comments = append(state.Comments, comment)
	}
	if err := commentRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	approvalRows, err := db.QueryContext(ctx, `SELECT workflow_id, role, display_name, reviewer_type, reviewer_status, status, sort_order
		FROM approval_groups ORDER BY workflow_id, sort_order, role`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ironclad scenario: dump approvals: %w", err)
	}
	defer approvalRows.Close()
	index := -1
	for approvalRows.Next() {
		var workflowID string
		var group fixtureApprovalGroup
		if err := approvalRows.Scan(&workflowID, &group.Role, &group.DisplayName, &group.ReviewerType, &group.ReviewerStatus, &group.Status, &group.Order); err != nil {
			return scenario.Document{}, err
		}
		if index < 0 || state.Approvals[index].WorkflowID != workflowID {
			state.Approvals = append(state.Approvals, fixtureApproval{WorkflowID: workflowID, ApprovalGroups: []fixtureApprovalGroup{}})
			index = len(state.Approvals) - 1
		}
		state.Approvals[index].ApprovalGroups = append(state.Approvals[index].ApprovalGroups, group)
	}
	if err := approvalRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Workflows == nil {
		state.Workflows = []fixtureWorkflow{}
	}
	if state.Records == nil {
		state.Records = []fixtureRecord{}
	}
	if state.Comments == nil {
		state.Comments = []fixtureComment{}
	}
	if state.Approvals == nil {
		state.Approvals = []fixtureApproval{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "ironclad", ResourceVersion: "v1", State: raw,
	}, nil
}

func scanFixtureWorkflow(row interface{ Scan(...any) error }) (fixtureWorkflow, error) {
	var workflow fixtureWorkflow
	var cancelled, complete int
	var recordIDs string
	if err := row.Scan(&workflow.ID, &workflow.IroncladID, &workflow.Title, &workflow.Template, &workflow.Step, &workflow.Status,
		&cancelled, &complete, &workflow.CounterpartyName, &workflow.CounterpartyEmail, &workflow.EnvelopeID, &workflow.Filename,
		&recordIDs, &workflow.RoleID, &workflow.RoleDisplayName, &workflow.AssigneeID, &workflow.Created, &workflow.LastUpdated, &workflow.CreatorID); err != nil {
		return fixtureWorkflow{}, err
	}
	workflow.IsCancelled = cancelled != 0
	workflow.IsComplete = complete != 0
	if err := json.Unmarshal([]byte(recordIDs), &workflow.RecordIDs); err != nil {
		return fixtureWorkflow{}, fmt.Errorf("ironclad scenario: workflow %s recordIds: %w", workflow.ID, err)
	}
	if workflow.RecordIDs == nil {
		workflow.RecordIDs = []string{}
	}
	return workflow, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("ironclad scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("ironclad scenario: state has trailing data")
	}
	if state.Workflows == nil || state.Records == nil || state.Comments == nil || state.Approvals == nil {
		return fixtureState{}, fmt.Errorf("ironclad scenario: workflows, records, comments, and approvals are required arrays")
	}
	for i := range state.Workflows {
		if state.Workflows[i].RecordIDs == nil {
			return fixtureState{}, fmt.Errorf("ironclad scenario: workflows[%d].recordIds is required", i)
		}
	}
	for i := range state.Approvals {
		if state.Approvals[i].ApprovalGroups == nil {
			return fixtureState{}, fmt.Errorf("ironclad scenario: approvals[%d].approvalGroups is required", i)
		}
	}
	return state, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }
