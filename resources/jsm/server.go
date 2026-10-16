package jsm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/jsm/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const (
	apiBase            = "https://acme.atlassian.net/rest/servicedeskapi"
	siteBase           = "https://acme.atlassian.net"
	requestTypeGroupID = "1"
	summaryLabel       = "What do you need?"
	descriptionLabel   = "Why do you need this?"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("jsm: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("jsm: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("jsm: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("jsm: load OpenAPI: %w", err)
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
			key := "sd.error"
			if status == http.StatusUnauthorized {
				key = "sd.auth.unauthorized"
			}
			writeError(w, status, key, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GetServiceDesks(ctx context.Context, request generated.GetServiceDesksRequestObject) (generated.GetServiceDesksResponseObject, error) {
	desks, err := s.listDesks(ctx)
	if err != nil {
		return nil, err
	}
	page, window, err := cutPage(desks, request.Params.Start, request.Params.Limit)
	if err != nil {
		return generated.GetServiceDesksdefaultJSONResponse{Body: apiError("sd.page.invalid", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	values := make([]generated.ServiceDeskDTO, 0, len(page))
	for _, desk := range page {
		values = append(values, serviceDeskDTO(desk))
	}
	return generated.GetServiceDesks200JSONResponse{
		Size: len(values), Start: window.start, Limit: window.limit, IsLastPage: window.last,
		Values: values, UnderscoreExpands: []string{},
		UnderscoreLinks: pagedLinks("/servicedesk", window, len(values), len(desks)),
	}, nil
}

func (s *server) GetServiceDeskById(ctx context.Context, request generated.GetServiceDeskByIdRequestObject) (generated.GetServiceDeskByIdResponseObject, error) {
	desk, err := s.resolveDesk(ctx, request.ServiceDeskId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetServiceDeskByIddefaultJSONResponse{Body: apiError("sd.servicedesk.not.found", "The service desk could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetServiceDeskById200JSONResponse(serviceDeskDTO(desk)), nil
}

func (s *server) GetRequestTypes(ctx context.Context, request generated.GetRequestTypesRequestObject) (generated.GetRequestTypesResponseObject, error) {
	desk, err := s.resolveDesk(ctx, request.ServiceDeskId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestTypesdefaultJSONResponse{Body: apiError("sd.servicedesk.not.found", "The service desk could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	kinds, err := s.listRequestTypes(ctx, desk.ID)
	if err != nil {
		return nil, err
	}
	query := ""
	if request.Params.SearchQuery != nil {
		query = *request.Params.SearchQuery
	}
	filtered := make([]requestTypeRow, 0)
	for _, kind := range kinds {
		if request.Params.GroupId != nil && *request.Params.GroupId != 1 {
			continue
		}
		if request.Params.RestrictionStatus != nil && *request.Params.RestrictionStatus == generated.Restricted {
			continue
		}
		if query != "" && !matchesTerm(kind.Name+" "+kind.Description, query) {
			continue
		}
		filtered = append(filtered, kind)
	}
	page, window, err := cutPage(filtered, request.Params.Start, request.Params.Limit)
	if err != nil {
		return generated.GetRequestTypesdefaultJSONResponse{Body: apiError("sd.page.invalid", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	values := make([]generated.RequestTypeDTO, 0, len(page))
	for _, kind := range page {
		values = append(values, requestTypeDTO(kind))
	}
	path := "/servicedesk/" + url.PathEscape(desk.ID) + "/requesttype"
	return generated.GetRequestTypes200JSONResponse{
		Size: len(values), Start: window.start, Limit: window.limit, IsLastPage: window.last,
		Values: values, UnderscoreExpands: []string{},
		UnderscoreLinks: pagedLinks(path, window, len(values), len(filtered)),
	}, nil
}

func (s *server) GetRequestTypeById(ctx context.Context, request generated.GetRequestTypeByIdRequestObject) (generated.GetRequestTypeByIdResponseObject, error) {
	desk, err := s.resolveDesk(ctx, request.ServiceDeskId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestTypeByIddefaultJSONResponse{Body: apiError("sd.servicedesk.not.found", "The service desk could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	kind, err := s.requestTypeByID(ctx, desk.ID, request.RequestTypeId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestTypeByIddefaultJSONResponse{Body: apiError("sd.request.type.not.found", "The request type could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetRequestTypeById200JSONResponse(requestTypeDTO(kind)), nil
}

func (s *server) GetCustomerRequests(ctx context.Context, request generated.GetCustomerRequestsRequestObject) (generated.GetCustomerRequestsResponseObject, error) {
	if request.Params.RequestTypeId != nil && request.Params.ServiceDeskId == nil {
		return generated.GetCustomerRequestsdefaultJSONResponse{Body: apiError("sd.request.filter.invalid", "serviceDeskId is required when requestTypeId is set."), StatusCode: http.StatusBadRequest}, nil
	}
	current, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	var deskID string
	if request.Params.ServiceDeskId != nil {
		desk, err := s.deskByID(ctx, fmt.Sprintf("%d", *request.Params.ServiceDeskId))
		if errors.Is(err, sql.ErrNoRows) {
			return generated.GetCustomerRequestsdefaultJSONResponse{Body: apiError("sd.servicedesk.not.found", "The service desk could not be found."), StatusCode: http.StatusNotFound}, nil
		}
		if err != nil {
			return nil, err
		}
		deskID = desk.ID
	}
	var typeID string
	if request.Params.RequestTypeId != nil {
		typeID = fmt.Sprintf("%d", *request.Params.RequestTypeId)
	}
	rows, err := s.listRequests(ctx)
	if err != nil {
		return nil, err
	}
	term := ""
	if request.Params.SearchTerm != nil {
		term = *request.Params.SearchTerm
	}
	filtered := make([]requestRow, 0)
	for _, row := range rows {
		if deskID != "" && row.ServiceDeskID != deskID {
			continue
		}
		if typeID != "" && row.RequestTypeID != typeID {
			continue
		}
		if !matchesOwnership(row, current.AccountID, request.Params.RequestOwnership) {
			continue
		}
		if !matchesStatus(row.StatusCategory, request.Params.RequestStatus) {
			continue
		}
		if !matchesTerm(row.Summary, term) {
			continue
		}
		filtered = append(filtered, row)
	}
	sortRequests(filtered)
	page, window, err := cutPage(filtered, request.Params.Start, request.Params.Limit)
	if err != nil {
		return generated.GetCustomerRequestsdefaultJSONResponse{Body: apiError("sd.page.invalid", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	expand := expandSet(request.Params.Expand)
	values := make([]generated.CustomerRequestDTO, 0, len(page))
	for _, row := range page {
		dto, err := s.requestDTO(ctx, row, expand)
		if err != nil {
			return nil, err
		}
		values = append(values, dto)
	}
	return generated.GetCustomerRequests200JSONResponse{
		Size: len(values), Start: window.start, Limit: window.limit, IsLastPage: window.last,
		Values: values, UnderscoreExpands: requestExpandList(),
		UnderscoreLinks: pagedLinks("/request", window, len(values), len(filtered)),
	}, nil
}

func (s *server) CreateCustomerRequest(ctx context.Context, request generated.CreateCustomerRequestRequestObject) (generated.CreateCustomerRequestResponseObject, error) {
	if request.Body == nil {
		return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.invalid", "Request body is required."), StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body
	if body.IsAdfRequest != nil && *body.IsAdfRequest {
		return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.adf.unsupported", "Atlassian Document Format is not supported on this surface."), StatusCode: http.StatusBadRequest}, nil
	}
	for field := range body.RequestFieldValues {
		if field != "summary" && field != "description" {
			return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.field.unsupported", "Unsupported field "+field+"."), StatusCode: http.StatusBadRequest}, nil
		}
	}
	summary := strings.TrimSpace(body.RequestFieldValues["summary"])
	if summary == "" {
		return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.summary.required", "summary is required."), StatusCode: http.StatusBadRequest}, nil
	}
	description := body.RequestFieldValues["description"]
	desk, err := s.resolveDesk(ctx, body.ServiceDeskId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.servicedesk.not.found", "The service desk could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.requestTypeByID(ctx, desk.ID, body.RequestTypeId); errors.Is(err, sql.ErrNoRows) {
		return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.type.not.found", "The request type could not be found."), StatusCode: http.StatusBadRequest}, nil
	} else if err != nil {
		return nil, err
	}
	reporter, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if body.RaiseOnBehalfOf != nil && strings.TrimSpace(*body.RaiseOnBehalfOf) != "" {
		reporter, err = s.userByIDOrEmail(ctx, strings.TrimSpace(*body.RaiseOnBehalfOf))
		if errors.Is(err, sql.ErrNoRows) {
			return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError("sd.request.customer.not.found", "The customer could not be found."), StatusCode: http.StatusBadRequest}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	participants, err := s.participantIDs(ctx, body.RequestParticipants)
	if err != nil {
		var rejected requestError
		if errors.As(err, &rejected) {
			return generated.CreateCustomerRequestdefaultJSONResponse{Body: apiError(rejected.Key, rejected.Message), StatusCode: rejected.Status}, nil
		}
		return nil, err
	}
	issueID, err := s.ids.Next(ctx, "jsm.request")
	if err != nil {
		return nil, fmt.Errorf("jsm: allocate request ID: %w", err)
	}
	encoded, err := json.Marshal(participants)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	row := requestRow{
		IssueID: issueID, IssueKey: desk.ProjectKey + "-" + issueID, ServiceDeskID: desk.ID, RequestTypeID: body.RequestTypeId,
		Summary: summary, Description: description, ReporterAccountID: reporter.AccountID,
		Status: "Waiting for Support", StatusCategory: string(generated.NEW), CreatedAt: now, UpdatedAt: now,
		ParticipantAccountIDs: participants,
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO requests(
		issue_id, issue_key, service_desk_id, request_type_id, summary, description,
		reporter_account_id, status, status_category, created_at, updated_at, participant_account_ids)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.IssueID, row.IssueKey, row.ServiceDeskID, row.RequestTypeID, row.Summary, row.Description,
		row.ReporterAccountID, row.Status, row.StatusCategory, row.CreatedAt, row.UpdatedAt, string(encoded)); err != nil {
		return nil, err
	}
	dto, err := s.requestDTO(ctx, row, nil)
	if err != nil {
		return nil, err
	}
	return generated.CreateCustomerRequest201JSONResponse(dto), nil
}

func (s *server) GetCustomerRequestByIdOrKey(ctx context.Context, request generated.GetCustomerRequestByIdOrKeyRequestObject) (generated.GetCustomerRequestByIdOrKeyResponseObject, error) {
	row, err := s.requestByRef(ctx, request.IssueIdOrKey)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetCustomerRequestByIdOrKeydefaultJSONResponse{Body: apiError("sd.request.not.found", "The customer request could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	dto, err := s.requestDTO(ctx, row, expandSet(request.Params.Expand))
	if err != nil {
		return nil, err
	}
	return generated.GetCustomerRequestByIdOrKey200JSONResponse(dto), nil
}

func (s *server) GetRequestComments(ctx context.Context, request generated.GetRequestCommentsRequestObject) (generated.GetRequestCommentsResponseObject, error) {
	row, err := s.requestByRef(ctx, request.IssueIdOrKey)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestCommentsdefaultJSONResponse{Body: apiError("sd.request.not.found", "The customer request could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	comments, err := s.listComments(ctx, row.IssueID)
	if err != nil {
		return nil, err
	}
	includePublic := request.Params.Public == nil || *request.Params.Public
	includeInternal := request.Params.Internal == nil || *request.Params.Internal
	filtered := make([]commentRow, 0)
	for _, comment := range comments {
		if comment.Public && includePublic || !comment.Public && includeInternal {
			filtered = append(filtered, comment)
		}
	}
	page, window, err := cutPage(filtered, request.Params.Start, request.Params.Limit)
	if err != nil {
		return generated.GetRequestCommentsdefaultJSONResponse{Body: apiError("sd.page.invalid", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	expand := expandSet(request.Params.Expand)
	values := make([]generated.CommentDTO, 0, len(page))
	for _, comment := range page {
		dto, err := s.commentDTO(ctx, row, comment, expand)
		if err != nil {
			return nil, err
		}
		values = append(values, dto)
	}
	path := "/request/" + url.PathEscape(row.IssueID) + "/comment"
	return generated.GetRequestComments200JSONResponse{
		Size: len(values), Start: window.start, Limit: window.limit, IsLastPage: window.last,
		Values: values, UnderscoreExpands: []string{},
		UnderscoreLinks: pagedLinks(path, window, len(values), len(filtered)),
	}, nil
}

func (s *server) CreateRequestComment(ctx context.Context, request generated.CreateRequestCommentRequestObject) (generated.CreateRequestCommentResponseObject, error) {
	if request.Body == nil || strings.TrimSpace(request.Body.Body) == "" {
		return generated.CreateRequestCommentdefaultJSONResponse{Body: apiError("sd.comment.body.required", "Comment body is required."), StatusCode: http.StatusBadRequest}, nil
	}
	row, err := s.requestByRef(ctx, request.IssueIdOrKey)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateRequestCommentdefaultJSONResponse{Body: apiError("sd.request.not.found", "The customer request could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	author, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	commentID, err := s.ids.Next(ctx, "jsm.comment")
	if err != nil {
		return nil, fmt.Errorf("jsm: allocate comment ID: %w", err)
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	public := true
	if request.Body.Public != nil {
		public = *request.Body.Public
	}
	comment := commentRow{ID: commentID, IssueID: row.IssueID, Body: request.Body.Body, Public: public, AuthorAccountID: author.AccountID, CreatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, issue_id, body, is_public, author_account_id, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		comment.ID, comment.IssueID, comment.Body, boolInt(comment.Public), comment.AuthorAccountID, comment.CreatedAt); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE requests SET updated_at=? WHERE issue_id=?", now, row.IssueID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	dto, err := s.commentDTO(ctx, row, comment, nil)
	if err != nil {
		return nil, err
	}
	return generated.CreateRequestComment201JSONResponse(dto), nil
}

func (s *server) GetRequestCommentById(ctx context.Context, request generated.GetRequestCommentByIdRequestObject) (generated.GetRequestCommentByIdResponseObject, error) {
	row, err := s.requestByRef(ctx, request.IssueIdOrKey)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestCommentByIddefaultJSONResponse{Body: apiError("sd.request.not.found", "The customer request could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	comment, err := s.commentByID(ctx, row.IssueID, request.CommentId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetRequestCommentByIddefaultJSONResponse{Body: apiError("sd.comment.not.found", "The comment could not be found."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	dto, err := s.commentDTO(ctx, row, comment, expandSet(request.Params.Expand))
	if err != nil {
		return nil, err
	}
	return generated.GetRequestCommentById200JSONResponse(dto), nil
}

type deskRow struct {
	ID, ProjectID, ProjectKey, ProjectName string
}

type requestTypeRow struct {
	ID, ServiceDeskID, Name, Description, HelpText, IssueTypeID string
}

type userRow struct {
	AccountID, Email, DisplayName, TimeZone string
	Active                                  bool
}

type requestRow struct {
	IssueID, IssueKey, ServiceDeskID, RequestTypeID string
	Summary, Description, ReporterAccountID         string
	Status, StatusCategory, CreatedAt, UpdatedAt    string
	ParticipantAccountIDs                           []string
}

type commentRow struct {
	ID, IssueID, Body, AuthorAccountID, CreatedAt string
	Public                                        bool
}

type pageWindow struct {
	start, limit int
	last         bool
}

func (s *server) listDesks(ctx context.Context) ([]deskRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, project_id, project_key, project_name FROM service_desks ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []deskRow{}
	for rows.Next() {
		var desk deskRow
		if err := rows.Scan(&desk.ID, &desk.ProjectID, &desk.ProjectKey, &desk.ProjectName); err != nil {
			return nil, err
		}
		out = append(out, desk)
	}
	return out, rows.Err()
}

func (s *server) deskByID(ctx context.Context, id string) (deskRow, error) {
	var desk deskRow
	err := s.db.QueryRowContext(ctx, "SELECT id, project_id, project_key, project_name FROM service_desks WHERE id=?", id).
		Scan(&desk.ID, &desk.ProjectID, &desk.ProjectKey, &desk.ProjectName)
	return desk, err
}

func (s *server) resolveDesk(ctx context.Context, raw string) (deskRow, error) {
	desks, err := s.listDesks(ctx)
	if err != nil {
		return deskRow{}, err
	}
	raw = strings.TrimSpace(raw)
	for _, desk := range desks {
		if desk.ID == raw || desk.ProjectKey == raw || desk.ProjectID == raw {
			return desk, nil
		}
	}
	kind, value, ok := strings.Cut(raw, ":")
	if ok {
		for _, desk := range desks {
			switch kind {
			case "serviceDeskId":
				if desk.ID == value {
					return desk, nil
				}
			case "projectKey":
				if desk.ProjectKey == value {
					return desk, nil
				}
			case "projectId":
				if desk.ProjectID == value {
					return desk, nil
				}
			}
		}
	}
	return deskRow{}, sql.ErrNoRows
}

func (s *server) listRequestTypes(ctx context.Context, deskID string) ([]requestTypeRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, service_desk_id, name, description, help_text, issue_type_id
		FROM request_types WHERE service_desk_id=? ORDER BY id`, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []requestTypeRow{}
	for rows.Next() {
		var kind requestTypeRow
		if err := rows.Scan(&kind.ID, &kind.ServiceDeskID, &kind.Name, &kind.Description, &kind.HelpText, &kind.IssueTypeID); err != nil {
			return nil, err
		}
		out = append(out, kind)
	}
	return out, rows.Err()
}

func (s *server) requestTypeByID(ctx context.Context, deskID, id string) (requestTypeRow, error) {
	var kind requestTypeRow
	err := s.db.QueryRowContext(ctx, `SELECT id, service_desk_id, name, description, help_text, issue_type_id
		FROM request_types WHERE service_desk_id=? AND id=?`, deskID, id).
		Scan(&kind.ID, &kind.ServiceDeskID, &kind.Name, &kind.Description, &kind.HelpText, &kind.IssueTypeID)
	return kind, err
}

func (s *server) currentUser(ctx context.Context) (userRow, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserAccountId'").Scan(&id); err != nil {
		return userRow{}, err
	}
	return s.userByID(ctx, id)
}

func (s *server) userByID(ctx context.Context, id string) (userRow, error) {
	var user userRow
	var active int
	err := s.db.QueryRowContext(ctx, "SELECT account_id, email_address, display_name, active, time_zone FROM users WHERE account_id=?", id).
		Scan(&user.AccountID, &user.Email, &user.DisplayName, &active, &user.TimeZone)
	user.Active = active != 0
	return user, err
}

func (s *server) userByIDOrEmail(ctx context.Context, ref string) (userRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, email_address, display_name, active, time_zone
		FROM users WHERE account_id=? OR email_address=?`, ref, ref)
	if err != nil {
		return userRow{}, err
	}
	defer rows.Close()
	var found []userRow
	for rows.Next() {
		var user userRow
		var active int
		if err := rows.Scan(&user.AccountID, &user.Email, &user.DisplayName, &active, &user.TimeZone); err != nil {
			return userRow{}, err
		}
		user.Active = active != 0
		found = append(found, user)
	}
	if err := rows.Err(); err != nil {
		return userRow{}, err
	}
	if len(found) == 0 {
		return userRow{}, sql.ErrNoRows
	}
	return found[0], nil
}

func (s *server) listRequests(ctx context.Context) ([]requestRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id, issue_key, service_desk_id, request_type_id, summary, description,
		reporter_account_id, status, status_category, created_at, updated_at, participant_account_ids FROM requests`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []requestRow{}
	for rows.Next() {
		row, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) requestByRef(ctx context.Context, ref string) (requestRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue_id, issue_key, service_desk_id, request_type_id, summary, description,
		reporter_account_id, status, status_category, created_at, updated_at, participant_account_ids
		FROM requests WHERE issue_id=? OR issue_key=?`, ref, ref)
	if err != nil {
		return requestRow{}, err
	}
	defer rows.Close()
	var found []requestRow
	for rows.Next() {
		row, err := scanRequest(rows)
		if err != nil {
			return requestRow{}, err
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		return requestRow{}, err
	}
	for _, row := range found {
		if row.IssueID == ref {
			return row, nil
		}
	}
	for _, row := range found {
		if row.IssueKey == ref {
			return row, nil
		}
	}
	return requestRow{}, sql.ErrNoRows
}

func scanRequest(row interface{ Scan(...any) error }) (requestRow, error) {
	var request requestRow
	var participants string
	if err := row.Scan(&request.IssueID, &request.IssueKey, &request.ServiceDeskID, &request.RequestTypeID,
		&request.Summary, &request.Description, &request.ReporterAccountID, &request.Status, &request.StatusCategory,
		&request.CreatedAt, &request.UpdatedAt, &participants); err != nil {
		return requestRow{}, err
	}
	ids, err := decodeIDs(participants)
	if err != nil {
		return requestRow{}, err
	}
	request.ParticipantAccountIDs = ids
	return request, nil
}

func (s *server) listComments(ctx context.Context, issueID string) ([]commentRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, body, is_public, author_account_id, created_at
		FROM comments WHERE issue_id=? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []commentRow{}
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, comment)
	}
	return out, rows.Err()
}

func (s *server) commentByID(ctx context.Context, issueID, commentID string) (commentRow, error) {
	return scanComment(s.db.QueryRowContext(ctx, `SELECT id, issue_id, body, is_public, author_account_id, created_at
		FROM comments WHERE issue_id=? AND id=?`, issueID, commentID))
}

func scanComment(row interface{ Scan(...any) error }) (commentRow, error) {
	var comment commentRow
	var public int
	if err := row.Scan(&comment.ID, &comment.IssueID, &comment.Body, &public, &comment.AuthorAccountID, &comment.CreatedAt); err != nil {
		return commentRow{}, err
	}
	comment.Public = public != 0
	return comment, nil
}

func (s *server) participantIDs(ctx context.Context, values *[]string) ([]string, error) {
	if values == nil {
		return []string{}, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(*values))
	for _, id := range *values {
		id = strings.TrimSpace(id)
		if _, exists := seen[id]; exists {
			return nil, requestError{Status: http.StatusBadRequest, Key: "sd.request.participant.duplicate", Message: "Duplicate request participant " + id + "."}
		}
		if _, err := s.userByID(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return nil, requestError{Status: http.StatusBadRequest, Key: "sd.request.participant.not.found", Message: "Request participant " + id + " could not be found."}
		} else if err != nil {
			return nil, err
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func (s *server) requestDTO(ctx context.Context, row requestRow, expand map[string]bool) (generated.CustomerRequestDTO, error) {
	reporter, err := s.userByID(ctx, row.ReporterAccountID)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	created, err := dateDTO(row.CreatedAt)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	statusAt, err := dateDTO(row.UpdatedAt)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	category := generated.CustomerRequestStatusDTOStatusCategory(row.StatusCategory)
	dto := generated.CustomerRequestDTO{
		IssueId: row.IssueID, IssueKey: row.IssueKey, Summary: row.Summary,
		RequestTypeId: row.RequestTypeID, ServiceDeskId: row.ServiceDeskID,
		CreatedDate: created, Reporter: userDTO(reporter), RequestFieldValues: fieldValues(row.Summary, row.Description),
		CurrentStatus:     generated.CustomerRequestStatusDTO{Status: row.Status, StatusCategory: category, StatusDate: statusAt},
		UnderscoreExpands: requestExpandList(),
		UnderscoreLinks: generated.CustomerRequestLinkDTO{
			Self:     apiBase + "/request/" + url.PathEscape(row.IssueID),
			JiraRest: siteBase + "/rest/api/3/issue/" + url.PathEscape(row.IssueID),
			Web:      siteBase + "/servicedesk/customer/portal/" + url.PathEscape(row.ServiceDeskID) + "/" + url.PathEscape(row.IssueKey),
			Agent:    siteBase + "/browse/" + url.PathEscape(row.IssueKey),
		},
	}
	if expand["serviceDesk"] {
		desk, err := s.deskByID(ctx, row.ServiceDeskID)
		if err != nil {
			return generated.CustomerRequestDTO{}, err
		}
		copied := serviceDeskDTO(desk)
		dto.ServiceDesk = &copied
	}
	if expand["requestType"] {
		kind, err := s.requestTypeByID(ctx, row.ServiceDeskID, row.RequestTypeID)
		if err != nil {
			return generated.CustomerRequestDTO{}, err
		}
		copied := requestTypeDTO(kind)
		dto.RequestType = &copied
	}
	return dto, nil
}

func (s *server) commentDTO(ctx context.Context, request requestRow, comment commentRow, expand map[string]bool) (generated.CommentDTO, error) {
	author, err := s.userByID(ctx, comment.AuthorAccountID)
	if err != nil {
		return generated.CommentDTO{}, err
	}
	created, err := dateDTO(comment.CreatedAt)
	if err != nil {
		return generated.CommentDTO{}, err
	}
	dto := generated.CommentDTO{
		Id: comment.ID, Body: comment.Body, Public: comment.Public, Author: userDTO(author), Created: created,
		UnderscoreExpands: commentExpandList(),
		UnderscoreLinks:   generated.SelfLinkDTO{Self: apiBase + "/request/" + url.PathEscape(request.IssueID) + "/comment/" + url.PathEscape(comment.ID)},
	}
	if expand["renderedBody"] {
		dto.RenderedBody = renderedHTML(comment.Body)
	}
	return dto, nil
}

func serviceDeskDTO(desk deskRow) generated.ServiceDeskDTO {
	return generated.ServiceDeskDTO{
		Id: desk.ID, ProjectId: desk.ProjectID, ProjectKey: desk.ProjectKey, ProjectName: desk.ProjectName,
		ProjectTypeKey:  "service_desk",
		UnderscoreLinks: generated.SelfLinkDTO{Self: apiBase + "/servicedesk/" + url.PathEscape(desk.ID)},
	}
}

func requestTypeDTO(kind requestTypeRow) generated.RequestTypeDTO {
	return generated.RequestTypeDTO{
		Id: kind.ID, Name: kind.Name, Description: kind.Description, HelpText: kind.HelpText,
		IssueTypeId: kind.IssueTypeID, ServiceDeskId: kind.ServiceDeskID, PortalId: kind.ServiceDeskID,
		GroupIds: []string{requestTypeGroupID}, RestrictionStatus: generated.OPEN,
		UnderscoreExpands: []string{"field"},
		UnderscoreLinks:   generated.SelfLinkDTO{Self: apiBase + "/servicedesk/" + url.PathEscape(kind.ServiceDeskID) + "/requesttype/" + url.PathEscape(kind.ID)},
	}
}

func userDTO(user userRow) generated.UserDTO {
	name := user.AccountID
	link := siteBase + "/rest/api/3/user?accountId=" + url.QueryEscape(user.AccountID)
	return generated.UserDTO{
		AccountId: user.AccountID, DisplayName: user.DisplayName, Active: user.Active,
		EmailAddress: user.Email, TimeZone: user.TimeZone, Name: &name, Key: &name,
		UnderscoreLinks: generated.UserLinkDTO{JiraRest: link, Self: link},
	}
}

func fieldValues(summary, description string) []generated.CustomerRequestFieldValueDTO {
	values := []generated.CustomerRequestFieldValueDTO{{FieldId: "summary", Label: summaryLabel, Value: summary}}
	if description != "" {
		values = append(values, generated.CustomerRequestFieldValueDTO{
			FieldId: "description", Label: descriptionLabel, Value: description, RenderedValue: renderedHTML(description),
		})
	}
	return values
}

func renderedHTML(text string) *struct {
	Html string `json:"html"`
} {
	value := struct {
		Html string `json:"html"`
	}{Html: "<p>" + html.EscapeString(text) + "</p>"}
	return &value
}

func dateDTO(raw string) (generated.DateDTO, error) {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return generated.DateDTO{}, fmt.Errorf("jsm: parse time %q: %w", raw, err)
	}
	parsed = parsed.UTC()
	return generated.DateDTO{
		EpochMillis: parsed.UnixMilli(),
		Friendly:    parsed.Format("Monday 3:04 PM"),
		Iso8601:     parsed.Format("2006-01-02T15:04:05-0700"),
		Jira:        parsed.Format("2006-01-02T15:04:05.000-0700"),
	}, nil
}

func requestExpandList() []string {
	return []string{"participant", "status", "sla", "requestType", "serviceDesk", "attachment", "action", "comment"}
}

func commentExpandList() []string { return []string{"attachment", "renderedBody"} }

func expandSet(values *[]string) map[string]bool {
	set := map[string]bool{}
	if values == nil {
		return set
	}
	for _, value := range *values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				set[part] = true
			}
		}
	}
	return set
}

func matchesOwnership(row requestRow, accountID string, modes *[]generated.GetCustomerRequestsParamsRequestOwnership) bool {
	selected := []generated.GetCustomerRequestsParamsRequestOwnership{
		generated.GetCustomerRequestsParamsRequestOwnershipOWNEDREQUESTS,
		generated.GetCustomerRequestsParamsRequestOwnershipPARTICIPATEDREQUESTS,
		generated.GetCustomerRequestsParamsRequestOwnershipALLORGANIZATIONS,
	}
	if modes != nil {
		selected = *modes
	}
	for _, mode := range selected {
		switch mode {
		case generated.GetCustomerRequestsParamsRequestOwnershipALLREQUESTS:
			return true
		case generated.GetCustomerRequestsParamsRequestOwnershipOWNEDREQUESTS:
			if row.ReporterAccountID == accountID {
				return true
			}
		case generated.GetCustomerRequestsParamsRequestOwnershipPARTICIPATEDREQUESTS:
			if contains(row.ParticipantAccountIDs, accountID) {
				return true
			}
		}
	}
	return false
}

func matchesStatus(category string, filter *generated.GetCustomerRequestsParamsRequestStatus) bool {
	if filter == nil || *filter == generated.GetCustomerRequestsParamsRequestStatusALLREQUESTS {
		return true
	}
	closed := category == string(generated.DONE)
	switch *filter {
	case generated.GetCustomerRequestsParamsRequestStatusCLOSEDREQUESTS:
		return closed
	case generated.GetCustomerRequestsParamsRequestStatusOPENREQUESTS:
		return !closed
	default:
		return false
	}
}

func matchesTerm(summary, term string) bool {
	term = strings.TrimSpace(strings.ToLower(term))
	if term == "" {
		return true
	}
	rest := strings.ToLower(summary)
	for _, part := range strings.Split(term, "*") {
		if part == "" {
			continue
		}
		index := strings.Index(rest, part)
		if index < 0 {
			return false
		}
		rest = rest[index+len(part):]
	}
	return true
}

func sortRequests(rows []requestRow) {
	sort.Slice(rows, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, rows[i].UpdatedAt)
		right, rightErr := time.Parse(time.RFC3339, rows[j].UpdatedAt)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		if rows[i].UpdatedAt != rows[j].UpdatedAt {
			return rows[i].UpdatedAt > rows[j].UpdatedAt
		}
		return rows[i].IssueKey < rows[j].IssueKey
	})
}

func cutPage[T any](items []T, start, limit *int) ([]T, pageWindow, error) {
	window := pageWindow{start: 0, limit: 50, last: true}
	if start != nil {
		window.start = *start
	}
	if limit != nil {
		window.limit = *limit
	}
	if window.start < 0 || window.limit < 1 || window.limit > 100 {
		return nil, window, fmt.Errorf("invalid start or limit")
	}
	if items == nil {
		items = []T{}
	}
	if window.start > len(items) {
		return nil, window, fmt.Errorf("start is past the end of the collection")
	}
	end := window.start + window.limit
	if end >= len(items) {
		end = len(items)
		window.last = true
	} else {
		window.last = false
	}
	return items[window.start:end], window, nil
}

func pagedLinks(path string, window pageWindow, pageSize, total int) generated.PagedLinkDTO {
	links := generated.PagedLinkDTO{
		Base: apiBase, Context: "",
		Self: fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, window.start, window.limit),
	}
	if window.start+pageSize < total {
		next := fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, window.start+pageSize, window.limit)
		links.Next = &next
	}
	if window.start > 0 {
		prevStart := window.start - window.limit
		if prevStart < 0 {
			prevStart = 0
		}
		prev := fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, prevStart, window.limit)
		links.Prev = &prev
	}
	return links
}

type requestError struct {
	Status  int
	Key     string
	Message string
}

func (e requestError) Error() string { return e.Message }

func apiError(key, message string) generated.ErrorResponse {
	return generated.ErrorResponse{
		ErrorMessage: message,
		I18nErrorMessage: generated.I18nErrorMessage{
			I18nKey: key, Parameters: []string{},
		},
	}
}

func writeError(w http.ResponseWriter, status int, key, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(key, message))
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
