package ironclad

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/ironclad/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const apiPrefix = "/public/api/v1"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("ironclad: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("ironclad: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("ironclad: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("ironclad: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			header := input.RequestValidationInput.Request.Header.Get("Authorization")
			if header != "Bearer "+token {
				return input.NewError(errors.New("invalid synthetic bearer token"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			code := "INVALID_REQUEST"
			if status == http.StatusUnauthorized {
				code = "UNAUTHORIZED"
			}
			writeError(w, status, code, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListAllWorkflows(ctx context.Context, request generated.ListAllWorkflowsRequestObject) (generated.ListAllWorkflowsResponseObject, error) {
	workflows, err := s.listWorkflows(ctx, request.Params.Status, request.Params.Template)
	if err != nil {
		return nil, err
	}
	user, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	number, size, start, end := window(request.Params.Page, request.Params.PageSize, len(workflows))
	list := make([]generated.Workflow, 0, end-start)
	for _, workflow := range workflows[start:end] {
		list = append(list, workflowResponse(workflow, user))
	}
	return generated.ListAllWorkflows200JSONResponse{Page: number, PageSize: size, Count: len(workflows), List: list}, nil
}

func (s *server) RetrieveAWorkflow(ctx context.Context, request generated.RetrieveAWorkflowRequestObject) (generated.RetrieveAWorkflowResponseObject, error) {
	workflow, user, err := s.workflowForRead(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveAWorkflow404JSONResponse(apiError("NOT_FOUND", "workflow does not exist", "workflowId")), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveAWorkflow200JSONResponse(workflowResponse(workflow, user)), nil
}

func (s *server) LaunchANewWorkflow(ctx context.Context, request generated.LaunchANewWorkflowRequestObject) (generated.LaunchANewWorkflowResponseObject, error) {
	if request.Body == nil {
		return generated.LaunchANewWorkflow400JSONResponse(apiError("MISSING_PARAM", "request body is required", "attributes")), nil
	}
	template := strings.TrimSpace(request.Body.Template)
	name := strings.TrimSpace(request.Body.Attributes.CounterpartyName)
	if template == "" || name == "" {
		return generated.LaunchANewWorkflow400JSONResponse(apiError("MISSING_PARAM", "template and counterpartyName are required", "counterpartyName")), nil
	}
	email := ""
	if request.Body.Attributes.CounterpartyEmail != nil {
		email = strings.TrimSpace(*request.Body.Attributes.CounterpartyEmail)
		if email != "" {
			if _, err := mail.ParseAddress(email); err != nil {
				return generated.LaunchANewWorkflow400JSONResponse(apiError("INVALID_PARAM", "counterpartyEmail is invalid", "counterpartyEmail")), nil
			}
		}
	}
	envelope := ""
	if request.Body.Attributes.DocusignEnvelopeId != nil {
		envelope = strings.TrimSpace(*request.Body.Attributes.DocusignEnvelopeId)
	}
	user, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := s.ids.Next(ctx, "ironclad.workflow")
	if err != nil {
		return nil, err
	}
	readable, err := s.ids.Next(ctx, "ironclad.readable")
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	recordIDs, err := json.Marshal([]string{})
	if err != nil {
		return nil, err
	}
	workflow := fixtureWorkflow{
		ID: id, IroncladID: "IC-" + readable, Title: launchTitle(template, name), Template: template,
		Step: "Review", Status: "active", CounterpartyName: name, CounterpartyEmail: email, EnvelopeID: envelope,
		RecordIDs: []string{}, RoleID: "legal", RoleDisplayName: "Legal", AssigneeID: user.ID,
		Created: now, LastUpdated: now, CreatorID: user.ID,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflows
		(id, ironclad_id, title, template, step, status, is_cancelled, is_complete, counterparty_name, counterparty_email, envelope_id, filename, record_ids, role_id, role_display_name, assignee_id, created, last_updated, creator_id)
		VALUES(?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?)`,
		workflow.ID, workflow.IroncladID, workflow.Title, workflow.Template, workflow.Step, workflow.Status,
		workflow.CounterpartyName, workflow.CounterpartyEmail, workflow.EnvelopeID, string(recordIDs),
		workflow.RoleID, workflow.RoleDisplayName, workflow.AssigneeID, workflow.Created, workflow.LastUpdated, workflow.CreatorID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO approval_groups
		(workflow_id, role, display_name, reviewer_type, reviewer_status, status, sort_order)
		VALUES(?, 'legal', 'Legal', 'role', 'pending', 'active', 1)`, workflow.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.LaunchANewWorkflow200JSONResponse(workflowResponse(workflow, user)), nil
}

func (s *server) ListAllCommentsInAWorkflow(ctx context.Context, request generated.ListAllCommentsInAWorkflowRequestObject) (generated.ListAllCommentsInAWorkflowResponseObject, error) {
	if _, _, err := s.workflowForRead(ctx, request.Id); errors.Is(err, sql.ErrNoRows) {
		return generated.ListAllCommentsInAWorkflow404JSONResponse(apiError("NOT_FOUND", "workflow does not exist", "workflowId")), nil
	} else if err != nil {
		return nil, err
	}
	workflow, err := s.findWorkflow(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	comments, err := s.listComments(ctx, workflow.ID)
	if err != nil {
		return nil, err
	}
	user, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	number, size, start, end := window(request.Params.Page, request.Params.PageSize, len(comments))
	list := make([]generated.Comment, 0, end-start)
	for _, comment := range comments[start:end] {
		list = append(list, commentResponse(comment, user))
	}
	return generated.ListAllCommentsInAWorkflow200JSONResponse{Page: number, PageSize: size, Count: len(comments), List: list}, nil
}

func (s *server) CreateACommentOnAWorkflow(ctx context.Context, request generated.CreateACommentOnAWorkflowRequestObject) (generated.CreateACommentOnAWorkflowResponseObject, error) {
	workflow, err := s.findWorkflow(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateACommentOnAWorkflow404JSONResponse(apiError("NOT_FOUND", "workflow does not exist", "workflowId")), nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil || strings.TrimSpace(request.Body.Comment) == "" {
		return generated.CreateACommentOnAWorkflow400JSONResponse(apiError("MISSING_PARAM", "comment is required", "comment")), nil
	}
	repliedTo := ""
	if request.Body.RepliedToActivityFeedMessageId != nil {
		repliedTo = strings.TrimSpace(*request.Body.RepliedToActivityFeedMessageId)
	}
	if repliedTo != "" {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE id=? AND workflow_id=?`, repliedTo, workflow.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			return generated.CreateACommentOnAWorkflow400JSONResponse(apiError("INVALID_PARAM", "comment to reply to does not exist", "repliedToActivityFeedMessageId")), nil
		}
	}
	user, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := s.ids.Next(ctx, "ironclad.comment")
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, workflow_id, message, timestamp, author_id, replied_to) VALUES(?, ?, ?, ?, ?, ?)`,
		id, workflow.ID, request.Body.Comment, now, user.ID, repliedTo); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflows SET last_updated=? WHERE id=?`, now, workflow.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.CreateACommentOnAWorkflow200JSONResponse(commentResponse(fixtureComment{
		ID: id, WorkflowID: workflow.ID, Message: request.Body.Comment, Timestamp: now, AuthorID: user.ID, RepliedTo: repliedTo,
	}, user)), nil
}

func (s *server) ListAllWorkflowApprovals(ctx context.Context, request generated.ListAllWorkflowApprovalsRequestObject) (generated.ListAllWorkflowApprovalsResponseObject, error) {
	workflow, user, err := s.workflowForRead(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ListAllWorkflowApprovals404JSONResponse(apiError("NOT_FOUND", "workflow does not exist", "workflowId")), nil
	}
	if err != nil {
		return nil, err
	}
	groups, err := s.listApprovals(ctx, workflow.ID)
	if err != nil {
		return nil, err
	}
	rendered := make([]generated.ApprovalGroup, 0, len(groups))
	for _, group := range groups {
		rendered = append(rendered, generated.ApprovalGroup{
			Order:  group.Order,
			Status: generated.ApprovalGroupStatus(group.Status),
			Reviewers: []generated.ApprovalReviewer{{
				Role: group.Role, DisplayName: group.DisplayName, ReviewerType: group.ReviewerType,
				Status: generated.ApprovalReviewerStatus(group.ReviewerStatus),
			}},
		})
	}
	roles := workflowRoles(workflow, user)
	return generated.ListAllWorkflowApprovals200JSONResponse{
		WorkflowId: workflow.ID, Title: workflow.Title, ApprovalGroups: rendered, Roles: roles,
	}, nil
}

func (s *server) ListAllRecords(ctx context.Context, request generated.ListAllRecordsRequestObject) (generated.ListAllRecordsResponseObject, error) {
	records, err := s.listRecords(ctx)
	if err != nil {
		return nil, err
	}
	number, size, start, end := window(request.Params.Page, request.Params.PageSize, len(records))
	list := make([]generated.Record, 0, end-start)
	for _, record := range records[start:end] {
		list = append(list, recordResponse(record))
	}
	return generated.ListAllRecords200JSONResponse{Page: number, PageSize: size, Count: len(records), List: list}, nil
}

func (s *server) RetrieveARecord(ctx context.Context, request generated.RetrieveARecordRequestObject) (generated.RetrieveARecordResponseObject, error) {
	record, err := s.findRecord(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveARecord404JSONResponse(apiError("NOT_FOUND", "record does not exist", "recordId")), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveARecord200JSONResponse(recordResponse(record)), nil
}

func (s *server) UpdateRecordMetadata(ctx context.Context, request generated.UpdateRecordMetadataRequestObject) (generated.UpdateRecordMetadataResponseObject, error) {
	record, err := s.findRecord(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UpdateRecordMetadata404JSONResponse(apiError("NOT_FOUND", "record does not exist", "recordId")), nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.UpdateRecordMetadata400JSONResponse(apiError("MISSING_PARAM", "request body is required", "name")), nil
	}
	changed := false
	if request.Body.Name != nil {
		name := strings.TrimSpace(*request.Body.Name)
		if name == "" {
			return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "name is empty", "name")), nil
		}
		record.Record.Name = name
		changed = true
	}
	if request.Body.RemoveProperties != nil {
		for _, property := range *request.Body.RemoveProperties {
			switch property {
			case "counterpartyName":
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "counterpartyName cannot be removed", "removeProperties")), nil
			case "docusignEnvelopeId":
				record.Record.EnvelopeID = ""
				changed = true
			default:
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "unknown record property "+property, "removeProperties")), nil
			}
		}
	}
	if request.Body.AddProperties != nil {
		if request.Body.AddProperties.CounterpartyName != nil {
			if request.Body.AddProperties.CounterpartyName.Type != generated.String {
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "counterpartyName type must be string", "addProperties")), nil
			}
			value := strings.TrimSpace(request.Body.AddProperties.CounterpartyName.Value)
			if value == "" {
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "counterpartyName is empty", "addProperties")), nil
			}
			record.Record.CounterpartyName = value
			changed = true
		}
		if request.Body.AddProperties.DocusignEnvelopeId != nil {
			if request.Body.AddProperties.DocusignEnvelopeId.Type != generated.String {
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "docusignEnvelopeId type must be string", "addProperties")), nil
			}
			value := strings.TrimSpace(request.Body.AddProperties.DocusignEnvelopeId.Value)
			if value == "" {
				return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "docusignEnvelopeId is empty", "addProperties")), nil
			}
			record.Record.EnvelopeID = value
			changed = true
		}
	}
	if !changed {
		return generated.UpdateRecordMetadata400JSONResponse(apiError("INVALID_PARAM", "no updates", "addProperties")), nil
	}
	record.Record.LastUpdated = s.clock.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE records SET name=?, counterparty_name=?, envelope_id=?, last_updated=? WHERE id=?`,
		record.Record.Name, record.Record.CounterpartyName, record.Record.EnvelopeID, record.Record.LastUpdated, record.Record.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflows SET counterparty_name=?, envelope_id=? WHERE id=?`,
		record.Record.CounterpartyName, record.Record.EnvelopeID, record.Record.WorkflowID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.UpdateRecordMetadata200JSONResponse(recordResponse(record)), nil
}

type storedRecord struct {
	Record         fixtureRecord
	WorkflowStatus string
}

func (s *server) loadUser(ctx context.Context) (fixtureUser, error) {
	var user fixtureUser
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT value FROM metadata WHERE key='currentUserId'),
		(SELECT value FROM metadata WHERE key='currentUserDisplayName'),
		(SELECT value FROM metadata WHERE key='currentUserEmail'),
		(SELECT value FROM metadata WHERE key='currentUserTitle'),
		(SELECT value FROM metadata WHERE key='currentUserCompanyName')`).Scan(
		&user.ID, &user.DisplayName, &user.Email, &user.Title, &user.CompanyName)
	if err != nil {
		return fixtureUser{}, err
	}
	return user, nil
}

func (s *server) findWorkflow(ctx context.Context, id string) (fixtureWorkflow, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, ironclad_id, title, template, step, status, is_cancelled, is_complete,
		counterparty_name, counterparty_email, envelope_id, filename, record_ids, role_id, role_display_name, assignee_id, created, last_updated, creator_id
		FROM workflows WHERE id=? OR ironclad_id=?`, id, id)
	return scanFixtureWorkflow(row)
}

func (s *server) workflowForRead(ctx context.Context, id string) (fixtureWorkflow, fixtureUser, error) {
	workflow, err := s.findWorkflow(ctx, id)
	if err != nil {
		return fixtureWorkflow{}, fixtureUser{}, err
	}
	user, err := s.loadUser(ctx)
	if err != nil {
		return fixtureWorkflow{}, fixtureUser{}, err
	}
	return workflow, user, nil
}

func (s *server) listWorkflows(ctx context.Context, statuses *[]generated.ListAllWorkflowsParamsStatus, template *string) ([]fixtureWorkflow, error) {
	query := `SELECT id, ironclad_id, title, template, step, status, is_cancelled, is_complete,
		counterparty_name, counterparty_email, envelope_id, filename, record_ids, role_id, role_display_name, assignee_id, created, last_updated, creator_id
		FROM workflows`
	args := []any{}
	clauses := []string{}
	wanted := map[string]struct{}{"active": {}}
	if statuses != nil && len(*statuses) > 0 {
		wanted = map[string]struct{}{}
		for _, status := range *statuses {
			for _, part := range strings.Split(string(status), ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					wanted[part] = struct{}{}
				}
			}
		}
	}
	if len(wanted) > 0 {
		placeholders := make([]string, 0, len(wanted))
		for status := range wanted {
			placeholders = append(placeholders, "?")
			args = append(args, status)
		}
		clauses = append(clauses, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if template != nil && strings.TrimSpace(*template) != "" {
		clauses = append(clauses, "template=?")
		args = append(args, strings.TrimSpace(*template))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workflows := []fixtureWorkflow{}
	for rows.Next() {
		workflow, err := scanFixtureWorkflow(rows)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, rows.Err()
}

func (s *server) listComments(ctx context.Context, workflowID string) ([]fixtureComment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, workflow_id, message, timestamp, author_id, replied_to
		FROM comments WHERE workflow_id=? ORDER BY timestamp, id`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []fixtureComment{}
	for rows.Next() {
		var comment fixtureComment
		if err := rows.Scan(&comment.ID, &comment.WorkflowID, &comment.Message, &comment.Timestamp, &comment.AuthorID, &comment.RepliedTo); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (s *server) listApprovals(ctx context.Context, workflowID string) ([]fixtureApprovalGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role, display_name, reviewer_type, reviewer_status, status, sort_order
		FROM approval_groups WHERE workflow_id=? ORDER BY sort_order, role`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []fixtureApprovalGroup{}
	for rows.Next() {
		var group fixtureApprovalGroup
		if err := rows.Scan(&group.Role, &group.DisplayName, &group.ReviewerType, &group.ReviewerStatus, &group.Status, &group.Order); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *server) listRecords(ctx context.Context) ([]storedRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.ironclad_id, r.type, r.name, r.last_updated, r.counterparty_name, r.envelope_id, r.filename, r.workflow_id, r.contract_status, r.enhanced_status, w.status
		FROM records r JOIN workflows w ON w.id=r.workflow_id ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []storedRecord{}
	for rows.Next() {
		record, err := scanStoredRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *server) findRecord(ctx context.Context, id string) (storedRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT r.id, r.ironclad_id, r.type, r.name, r.last_updated, r.counterparty_name, r.envelope_id, r.filename, r.workflow_id, r.contract_status, r.enhanced_status, w.status
		FROM records r JOIN workflows w ON w.id=r.workflow_id WHERE r.id=? OR r.ironclad_id=?`, id, id)
	return scanStoredRecord(row)
}

func scanStoredRecord(row interface{ Scan(...any) error }) (storedRecord, error) {
	var record storedRecord
	err := row.Scan(&record.Record.ID, &record.Record.IroncladID, &record.Record.Type, &record.Record.Name, &record.Record.LastUpdated,
		&record.Record.CounterpartyName, &record.Record.EnvelopeID, &record.Record.Filename, &record.Record.WorkflowID,
		&record.Record.ContractStatus, &record.Record.EnhancedStatus, &record.WorkflowStatus)
	return record, err
}

func workflowResponse(workflow fixtureWorkflow, user fixtureUser) generated.Workflow {
	recordIDs := workflow.RecordIDs
	if recordIDs == nil {
		recordIDs = []string{}
	}
	return generated.Workflow{
		Id: workflow.ID, IroncladId: workflow.IroncladID, Title: workflow.Title, Template: workflow.Template,
		Step: generated.WorkflowStep(workflow.Step), Status: generated.WorkflowStatus(workflow.Status),
		IsCancelled: workflow.IsCancelled, IsComplete: workflow.IsComplete,
		IsRevertibleToReview: workflow.Step == "Sign" || workflow.Step == "Archive",
		Creator:              generated.WorkflowCreator{Id: user.ID, DisplayName: user.DisplayName, Email: user.Email, Title: user.Title},
		Created:              workflow.Created, LastUpdated: workflow.LastUpdated,
		Schema: workflowSchema(workflow), Attributes: workflowAttributes(workflow),
		Roles: workflowRoles(workflow, user), Approvals: approvalState(workflow), Signatures: signatureState(workflow),
		RecordIds: recordIDs,
	}
}

func workflowSchema(workflow fixtureWorkflow) generated.WorkflowAttributeMap {
	schema := generated.WorkflowAttributeMap{
		CounterpartyName: attributeSchema("string", "Counterparty Name", "counterpartyName", false),
	}
	if workflow.CounterpartyEmail != "" {
		email := attributeSchema("string", "Counterparty Email", "counterpartyEmail", false)
		schema.CounterpartyEmail = &email
	}
	if workflow.EnvelopeID != "" {
		envelope := attributeSchema("string", "DocuSign Envelope", "docusignEnvelopeId", false)
		schema.DocusignEnvelopeId = &envelope
	}
	if workflow.Filename != "" {
		draft := attributeSchema("array", "Draft", "draft", true)
		schema.Draft = &draft
	}
	return schema
}

func workflowAttributes(workflow fixtureWorkflow) generated.WorkflowAttributes {
	attributes := generated.WorkflowAttributes{CounterpartyName: workflow.CounterpartyName}
	if workflow.CounterpartyEmail != "" {
		email := workflow.CounterpartyEmail
		attributes.CounterpartyEmail = &email
	}
	if workflow.EnvelopeID != "" {
		envelope := workflow.EnvelopeID
		attributes.DocusignEnvelopeId = &envelope
	}
	if workflow.Filename != "" {
		key := documentKey(workflow.Status)
		documents := []generated.WorkflowDocument{{
			Version: "v1", VersionNumber: 1, Filename: workflow.Filename, Key: key,
			Download: fmt.Sprintf("%s/workflows/%s/document/%s/download", apiPrefix, workflow.ID, key),
		}}
		attributes.Draft = &documents
	}
	return attributes
}

func workflowRoles(workflow fixtureWorkflow, user fixtureUser) []generated.WorkflowRole {
	return []generated.WorkflowRole{{
		Id: workflow.RoleID, DisplayName: workflow.RoleDisplayName,
		Assignees: []generated.WorkflowAssignee{{UserName: user.DisplayName, UserId: user.ID, Email: user.Email}},
	}}
}

func approvalState(workflow fixtureWorkflow) generated.WorkflowState {
	switch workflow.Status {
	case "completed":
		return generated.WorkflowState{State: generated.WorkflowStateStateCompleted}
	case "cancelled":
		message := "This workflow has been cancelled"
		return generated.WorkflowState{State: generated.WorkflowStateStateNotApplicable, Message: &message}
	default:
		url := fmt.Sprintf("%s/workflows/%s/approvals", apiPrefix, workflow.ID)
		return generated.WorkflowState{State: generated.WorkflowStateStateInProgress, Url: &url}
	}
}

func signatureState(workflow fixtureWorkflow) generated.WorkflowState {
	switch workflow.Status {
	case "completed":
		return generated.WorkflowState{State: generated.WorkflowStateStateCompleted}
	case "cancelled":
		message := "This workflow has been cancelled"
		return generated.WorkflowState{State: generated.WorkflowStateStateNotApplicable, Message: &message}
	default:
		return generated.WorkflowState{State: generated.WorkflowStateStateNotStarted}
	}
}

func commentResponse(comment fixtureComment, user fixtureUser) generated.Comment {
	return generated.Comment{
		Id: comment.ID, CommentMessage: comment.Message, Timestamp: comment.Timestamp, IsExternal: false,
		Author: generated.CommentAuthor{
			Type: generated.InternalUser, CompanyName: user.CompanyName, DisplayName: user.DisplayName,
			Email: user.Email, UserId: user.ID,
		},
		MentionedUserDetails: []generated.MentionedUser{},
		AddedParticipants:    []string{},
		Reactions:            []generated.CommentReaction{},
	}
}

func recordResponse(record storedRecord) generated.Record {
	properties := generated.RecordProperties{
		CounterpartyName: generated.StringProperty{Type: generated.String, Value: record.Record.CounterpartyName},
	}
	if record.Record.EnvelopeID != "" {
		properties.DocusignEnvelopeId = &generated.StringProperty{Type: generated.String, Value: record.Record.EnvelopeID}
	}
	return generated.Record{
		Id: record.Record.ID, IroncladId: record.Record.IroncladID, Type: record.Record.Type, Name: record.Record.Name,
		LastUpdated: record.Record.LastUpdated, Properties: properties, Attachments: recordAttachments(record),
		Links:  []generated.RecordLink{},
		Source: generated.RecordSource{Type: generated.RecordSourceTypeWorkflow, WorkflowId: record.Record.WorkflowID},
		ContractStatus: generated.ContractStatus{
			Status: generated.ContractStatusStatus(record.Record.ContractStatus), EnhancedStatus: record.Record.EnhancedStatus,
		},
	}
}

func recordAttachments(record storedRecord) generated.RecordAttachments {
	if record.Record.Filename == "" {
		return generated.RecordAttachments{}
	}
	key := documentKey(record.WorkflowStatus)
	contentType := "application/octet-stream"
	if strings.HasSuffix(strings.ToLower(record.Record.Filename), ".pdf") {
		contentType = "application/pdf"
	}
	attachment := generated.Attachment{
		Filename: record.Record.Filename, ContentType: &contentType,
		Href: fmt.Sprintf("https://na1.ironcladapp.com%s/records/%s/attachments/%s", apiPrefix, record.Record.ID, key),
	}
	if key == "signedCopy" {
		return generated.RecordAttachments{SignedCopy: &attachment}
	}
	return generated.RecordAttachments{Draft: &attachment}
}

func attributeSchema(kind, displayName, key string, readOnly bool) generated.AttributeSchema {
	return generated.AttributeSchema{Type: kind, DisplayName: displayName, PropertyKey: key, ReadOnly: readOnly}
}

func documentKey(status string) string {
	if status == "completed" {
		return "signedCopy"
	}
	return "draft"
}

func launchTitle(template, name string) string {
	label := template
	switch template {
	case "tmpl-msa":
		label = "MSA"
	case "tmpl-cancellation":
		label = "Cancellation"
	}
	return label + " with " + name
}

func window(page, pageSize *int, total int) (number, size, start, end int) {
	size = 20
	if pageSize != nil && *pageSize > 0 {
		size = *pageSize
	}
	if page != nil && *page > 0 {
		number = *page
	}
	start = number * size
	if start > total {
		start = total
	}
	end = start + size
	if end > total {
		end = total
	}
	return number, size, start, end
}

func apiError(code, message, param string) generated.ErrorModel {
	model := generated.ErrorModel{Code: code, Message: message}
	if param != "" {
		model.Param = &param
	}
	return model
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(code, message, ""))
}
