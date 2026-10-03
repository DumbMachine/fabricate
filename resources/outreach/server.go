package outreach

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/outreach/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const apiBase = "https://api.outreach.io/api/v2"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("outreach: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("outreach: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("outreach: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("outreach: load OpenAPI: %w", err)
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
			if status == http.StatusUnauthorized {
				writeError(w, status, "unauthorized", "Unauthorized", "Invalid bearer token.")
				return
			}
			writeError(w, status, "badRequest", "Bad Request", err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListSequences(ctx context.Context, request generated.ListSequencesRequestObject) (generated.ListSequencesResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, enabled, sequence_type, share_type, tags, created_at, updated_at
		FROM sequences ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.SequenceResource{}
	for rows.Next() {
		sequence, err := scanSequence(rows)
		if err != nil {
			return nil, err
		}
		if id := deref(request.Params.FilterId); id != "" && sequence.ID != id {
			continue
		}
		if name := deref(request.Params.FilterName); name != "" && sequence.Name != name {
			continue
		}
		items = append(items, sequence.resource())
	}
	return generated.ListSequences200ApplicationVndAPIPlusJSONResponse{Data: items, Meta: meta(len(items))}, rows.Err()
}

func (s *server) GetSequence(ctx context.Context, request generated.GetSequenceRequestObject) (generated.GetSequenceResponseObject, error) {
	sequence, err := s.loadSequence(ctx, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetSequence404ApplicationVndAPIPlusJSONResponse{
			ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(notFound("sequence", string(request.Id))),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetSequence200ApplicationVndAPIPlusJSONResponse{Data: sequence.resource()}, nil
}

func (s *server) CreateSequence(ctx context.Context, request generated.CreateSequenceRequestObject) (generated.CreateSequenceResponseObject, error) {
	if request.Body == nil {
		return reject[generated.CreateSequence422ApplicationVndAPIPlusJSONResponse](invalid("data is required")), nil
	}
	attrs := request.Body.Data.Attributes
	name := strings.TrimSpace(attrs.Name)
	if name == "" {
		return reject[generated.CreateSequence422ApplicationVndAPIPlusJSONResponse](invalid("name is required")), nil
	}
	id, err := s.ids.Next(ctx, "outreach.sequence")
	if err != nil {
		return nil, err
	}
	now := s.now()
	sequence := storedSequence{
		ID: id, Name: name, Description: str(attrs.Description), Enabled: true,
		SequenceType: string(generated.Interval), ShareType: string(generated.Shared),
		Tags: strSlice(attrs.Tags), CreatedAt: now, UpdatedAt: now,
	}
	if attrs.Enabled != nil {
		sequence.Enabled = *attrs.Enabled
	}
	if attrs.SequenceType != nil {
		sequence.SequenceType = string(*attrs.SequenceType)
	}
	if attrs.ShareType != nil {
		sequence.ShareType = string(*attrs.ShareType)
	}
	position, err := nextPosition(ctx, s.db, "sequences")
	if err != nil {
		return nil, err
	}
	tags, err := encodeStrings(sequence.Tags)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sequences
		(id, name, description, enabled, sequence_type, share_type, tags, created_at, updated_at, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sequence.ID, sequence.Name, sequence.Description, boolInt(sequence.Enabled), sequence.SequenceType,
		sequence.ShareType, tags, sequence.CreatedAt, sequence.UpdatedAt, position); err != nil {
		return nil, err
	}
	return generated.CreateSequence201ApplicationVndAPIPlusJSONResponse{Data: sequence.resource()}, nil
}

func (s *server) ListProspects(ctx context.Context, request generated.ListProspectsRequestObject) (generated.ListProspectsResponseObject, error) {
	prospects, err := s.listProspects(ctx)
	if err != nil {
		return nil, err
	}
	items := []generated.ProspectResource{}
	for _, prospect := range prospects {
		if id := deref(request.Params.FilterId); id != "" && prospect.ID != id {
			continue
		}
		if name := deref(request.Params.FilterFirstName); name != "" && prospect.FirstName != name {
			continue
		}
		if email := deref(request.Params.FilterEmails); email != "" && !contains(prospect.Emails, email) {
			continue
		}
		items = append(items, prospect.resource())
	}
	return generated.ListProspects200ApplicationVndAPIPlusJSONResponse{Data: items, Meta: meta(len(items))}, nil
}

func (s *server) GetProspect(ctx context.Context, request generated.GetProspectRequestObject) (generated.GetProspectResponseObject, error) {
	prospect, err := s.loadProspect(ctx, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetProspect404ApplicationVndAPIPlusJSONResponse{
			ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(notFound("prospect", string(request.Id))),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetProspect200ApplicationVndAPIPlusJSONResponse{Data: prospect.resource()}, nil
}

func (s *server) CreateProspect(ctx context.Context, request generated.CreateProspectRequestObject) (generated.CreateProspectResponseObject, error) {
	if request.Body == nil {
		return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("data is required")), nil
	}
	attrs := request.Body.Data.Attributes
	first := strings.TrimSpace(attrs.FirstName)
	if first == "" {
		return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("firstName is required")), nil
	}
	if len(attrs.Emails) == 0 {
		return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("emails is required")), nil
	}
	seen := map[string]struct{}{}
	for _, email := range attrs.Emails {
		if _, err := mail.ParseAddress(email); err != nil {
			return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("emails must be valid email addresses")), nil
		}
		if _, exists := seen[email]; exists {
			return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("emails must be unique")), nil
		}
		seen[email] = struct{}{}
		taken, err := s.emailTaken(ctx, email)
		if err != nil {
			return nil, err
		}
		if taken {
			return reject[generated.CreateProspect422ApplicationVndAPIPlusJSONResponse](invalid("email is already in use")), nil
		}
	}
	last := str(attrs.LastName)
	name := strings.TrimSpace(str(attrs.Name))
	if name == "" {
		name = strings.TrimSpace(first + " " + last)
	}
	id, err := s.ids.Next(ctx, "outreach.prospect")
	if err != nil {
		return nil, err
	}
	now := s.now()
	prospect := storedProspect{
		ID: id, FirstName: first, LastName: last, Name: name, Emails: attrs.Emails,
		Title: str(attrs.Title), Company: str(attrs.Company), MobilePhones: strSlice(attrs.MobilePhones),
		Tags: strSlice(attrs.Tags), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertProspect(ctx, prospect); err != nil {
		return nil, err
	}
	return generated.CreateProspect201ApplicationVndAPIPlusJSONResponse{Data: prospect.resource()}, nil
}

func (s *server) ListSequenceStates(ctx context.Context, request generated.ListSequenceStatesRequestObject) (generated.ListSequenceStatesResponseObject, error) {
	items, err := s.listSequenceStates(ctx, deref(request.Params.FilterId), deref(request.Params.FilterState), deref(request.Params.FilterProspectId), deref(request.Params.FilterSequenceId))
	if err != nil {
		return nil, err
	}
	resources := []generated.SequenceStateResource{}
	for _, item := range items {
		resources = append(resources, item.resource())
	}
	return generated.ListSequenceStates200ApplicationVndAPIPlusJSONResponse{Data: resources, Meta: meta(len(resources))}, nil
}

func (s *server) GetSequenceState(ctx context.Context, request generated.GetSequenceStateRequestObject) (generated.GetSequenceStateResponseObject, error) {
	item, err := s.loadSequenceState(ctx, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetSequenceState404ApplicationVndAPIPlusJSONResponse{
			ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(notFound("sequenceState", string(request.Id))),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetSequenceState200ApplicationVndAPIPlusJSONResponse{Data: item.resource()}, nil
}

func (s *server) CreateSequenceState(ctx context.Context, request generated.CreateSequenceStateRequestObject) (generated.CreateSequenceStateResponseObject, error) {
	if request.Body == nil {
		return reject[generated.CreateSequenceState422ApplicationVndAPIPlusJSONResponse](invalid("data is required")), nil
	}
	body := request.Body.Data
	prospectID, sequenceID, errText := relationshipPair(body.Relationships.Prospect, body.Relationships.Sequence, "prospect", "sequence")
	if errText != "" {
		return reject[generated.CreateSequenceState422ApplicationVndAPIPlusJSONResponse](invalid(errText)), nil
	}
	if _, err := s.loadProspect(ctx, prospectID); errors.Is(err, sql.ErrNoRows) {
		return reject[generated.CreateSequenceState422ApplicationVndAPIPlusJSONResponse](invalid("prospect not found")), nil
	} else if err != nil {
		return nil, err
	}
	if _, err := s.loadSequence(ctx, sequenceID); errors.Is(err, sql.ErrNoRows) {
		return reject[generated.CreateSequenceState422ApplicationVndAPIPlusJSONResponse](invalid("sequence not found")), nil
	} else if err != nil {
		return nil, err
	}
	taken, err := s.sequenceMembership(ctx, prospectID, sequenceID)
	if err != nil {
		return nil, err
	}
	if taken {
		return reject[generated.CreateSequenceState422ApplicationVndAPIPlusJSONResponse](invalid("prospect is already in the sequence")), nil
	}
	state := generated.SequenceStateNameActive
	if body.Attributes != nil && body.Attributes.State != nil {
		state = *body.Attributes.State
	}
	id, err := s.ids.Next(ctx, "outreach.sequenceState")
	if err != nil {
		return nil, err
	}
	now := s.now()
	item := storedSequenceState{
		ID: id, State: string(state), ProspectID: prospectID, SequenceID: sequenceID,
		CreatedAt: now, UpdatedAt: now, StateChangedAt: now,
	}
	position, err := nextPosition(ctx, s.db, "sequence_states")
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sequence_states
		(id, state, prospect_id, sequence_id, created_at, updated_at, state_changed_at, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.State, item.ProspectID, item.SequenceID, item.CreatedAt, item.UpdatedAt, item.StateChangedAt, position); err != nil {
		return nil, err
	}
	return generated.CreateSequenceState201ApplicationVndAPIPlusJSONResponse{Data: item.resource()}, nil
}

func (s *server) ListTasks(ctx context.Context, request generated.ListTasksRequestObject) (generated.ListTasksResponseObject, error) {
	tasks, err := s.listTasks(ctx, deref(request.Params.FilterId), deref(request.Params.FilterState), deref(request.Params.FilterProspectId), deref(request.Params.FilterSequenceId))
	if err != nil {
		return nil, err
	}
	items := []generated.TaskResource{}
	for _, task := range tasks {
		items = append(items, task.resource())
	}
	return generated.ListTasks200ApplicationVndAPIPlusJSONResponse{Data: items, Meta: meta(len(items))}, nil
}

func (s *server) GetTask(ctx context.Context, request generated.GetTaskRequestObject) (generated.GetTaskResponseObject, error) {
	task, err := s.loadTask(ctx, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetTask404ApplicationVndAPIPlusJSONResponse{
			ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(notFound("task", string(request.Id))),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetTask200ApplicationVndAPIPlusJSONResponse{Data: task.resource()}, nil
}

func (s *server) CreateTask(ctx context.Context, request generated.CreateTaskRequestObject) (generated.CreateTaskResponseObject, error) {
	if request.Body == nil {
		return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("data is required")), nil
	}
	body := request.Body.Data
	prospectID := body.Relationships.Prospect.Data.Id
	if body.Relationships.Prospect.Data.Type != "prospect" || prospectID == "" {
		return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("prospect relationship is required")), nil
	}
	sequenceID := ""
	if body.Relationships.Sequence != nil {
		if body.Relationships.Sequence.Data.Type != "sequence" || body.Relationships.Sequence.Data.Id == "" {
			return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("sequence relationship type must be sequence")), nil
		}
		sequenceID = body.Relationships.Sequence.Data.Id
	}
	if _, err := s.loadProspect(ctx, prospectID); errors.Is(err, sql.ErrNoRows) {
		return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("prospect not found")), nil
	} else if err != nil {
		return nil, err
	}
	if sequenceID != "" {
		if _, err := s.loadSequence(ctx, sequenceID); errors.Is(err, sql.ErrNoRows) {
			return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("sequence not found")), nil
		} else if err != nil {
			return nil, err
		}
		member, err := s.sequenceMembership(ctx, prospectID, sequenceID)
		if err != nil {
			return nil, err
		}
		if !member {
			return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("prospect is not in the sequence")), nil
		}
	}
	now := s.now()
	task := storedTask{
		Action: string(body.Attributes.Action), Note: str(body.Attributes.Note), Completed: false,
		State: string(generated.TaskStateIncomplete), TaskType: string(generated.Manual), DueAt: now,
		StateChangedAt: now, CreatedAt: now, UpdatedAt: now, ProspectID: prospectID, SequenceID: sequenceID,
	}
	if body.Attributes.TaskType != nil {
		task.TaskType = string(*body.Attributes.TaskType)
	}
	if body.Attributes.DueAt != nil {
		if strings.TrimSpace(*body.Attributes.DueAt) == "" {
			return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid("dueAt is required")), nil
		}
		task.DueAt = *body.Attributes.DueAt
	}
	if err := applyCompletion(&task, body.Attributes.Completed, body.Attributes.State, now); err != nil {
		return reject[generated.CreateTask422ApplicationVndAPIPlusJSONResponse](invalid(err.Error())), nil
	}
	id, err := s.ids.Next(ctx, "outreach.task")
	if err != nil {
		return nil, err
	}
	task.ID = id
	if err := s.insertTask(ctx, task); err != nil {
		return nil, err
	}
	return generated.CreateTask201ApplicationVndAPIPlusJSONResponse{Data: task.resource()}, nil
}

func (s *server) UpdateTask(ctx context.Context, request generated.UpdateTaskRequestObject) (generated.UpdateTaskResponseObject, error) {
	task, err := s.loadTask(ctx, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UpdateTask404ApplicationVndAPIPlusJSONResponse{
			ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(notFound("task", string(request.Id))),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.UpdateTask422ApplicationVndAPIPlusJSONResponse(invalid("data is required")), nil
	}
	body := request.Body.Data
	if body.Id != nil && *body.Id != task.ID {
		return generated.UpdateTask422ApplicationVndAPIPlusJSONResponse(invalid("id does not match the path")), nil
	}
	now := s.now()
	if body.Attributes != nil {
		attrs := body.Attributes
		if attrs.Action != nil {
			task.Action = string(*attrs.Action)
		}
		if attrs.Note != nil {
			task.Note = *attrs.Note
		}
		if attrs.TaskType != nil {
			task.TaskType = string(*attrs.TaskType)
		}
		if attrs.DueAt != nil {
			if strings.TrimSpace(*attrs.DueAt) == "" {
				return generated.UpdateTask422ApplicationVndAPIPlusJSONResponse(invalid("dueAt is required")), nil
			}
			task.DueAt = *attrs.DueAt
		}
		if err := applyCompletion(&task, attrs.Completed, attrs.State, now); err != nil {
			return generated.UpdateTask422ApplicationVndAPIPlusJSONResponse(invalid(err.Error())), nil
		}
	}
	task.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET action=?, note=?, completed=?, state=?, task_type=?, due_at=?,
		completed_at=?, state_changed_at=?, updated_at=? WHERE id=?`,
		task.Action, task.Note, boolInt(task.Completed), task.State, task.TaskType, task.DueAt,
		task.CompletedAt, task.StateChangedAt, task.UpdatedAt, task.ID); err != nil {
		return nil, err
	}
	return generated.UpdateTask200ApplicationVndAPIPlusJSONResponse{Data: task.resource()}, nil
}

type storedSequence struct {
	ID, Name, Description, SequenceType, ShareType, CreatedAt, UpdatedAt string
	Enabled                                                              bool
	Tags                                                                 []string
}

func (sequence storedSequence) resource() generated.SequenceResource {
	tags := sequence.Tags
	if tags == nil {
		tags = []string{}
	}
	return generated.SequenceResource{
		Type: generated.SequenceResourceTypeSequence,
		Id:   sequence.ID,
		Attributes: generated.SequenceAttributes{
			Name: sequence.Name, Description: sequence.Description, Enabled: sequence.Enabled,
			SequenceType: generated.SequenceType(sequence.SequenceType), ShareType: generated.ShareType(sequence.ShareType),
			Tags: tags, CreatedAt: sequence.CreatedAt, UpdatedAt: sequence.UpdatedAt,
		},
		Relationships: generated.SequenceRelationships{
			SequenceStates: relatedLink("sequenceStates", "filter[sequence][id]", sequence.ID),
			Tasks:          relatedLink("tasks", "filter[sequence][id]", sequence.ID),
		},
	}
}

type storedProspect struct {
	ID, FirstName, LastName, Name, Title, Company, CreatedAt, UpdatedAt string
	Emails, MobilePhones, Tags                                          []string
}

func (prospect storedProspect) resource() generated.ProspectResource {
	return generated.ProspectResource{
		Type: generated.ProspectResourceTypeProspect,
		Id:   prospect.ID,
		Attributes: generated.ProspectAttributes{
			FirstName: prospect.FirstName, LastName: prospect.LastName, Name: prospect.Name,
			Emails: nonNil(prospect.Emails), Title: prospect.Title, Company: prospect.Company,
			MobilePhones: nonNil(prospect.MobilePhones), Tags: nonNil(prospect.Tags),
			CreatedAt: prospect.CreatedAt, UpdatedAt: prospect.UpdatedAt,
		},
		Relationships: generated.ProspectRelationships{
			SequenceStates: relatedLink("sequenceStates", "filter[prospect][id]", prospect.ID),
			Tasks:          relatedLink("tasks", "filter[prospect][id]", prospect.ID),
		},
	}
}

type storedSequenceState struct {
	ID, State, ProspectID, SequenceID, CreatedAt, UpdatedAt, StateChangedAt string
}

func (item storedSequenceState) resource() generated.SequenceStateResource {
	return generated.SequenceStateResource{
		Type: generated.SequenceStateResourceTypeSequenceState,
		Id:   item.ID,
		Attributes: generated.SequenceStateAttributes{
			State: generated.SequenceStateName(item.State), CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt, StateChangedAt: item.StateChangedAt,
		},
		Relationships: generated.SequenceStateRelationships{
			Prospect: toOne("prospect", item.ProspectID),
			Sequence: toOne("sequence", item.SequenceID),
		},
	}
}

type storedTask struct {
	ID, Action, Note, State, TaskType, DueAt, CompletedAt, StateChangedAt, CreatedAt, UpdatedAt, ProspectID, SequenceID string
	Completed                                                                                                           bool
}

func (task storedTask) resource() generated.TaskResource {
	resource := generated.TaskResource{
		Type: generated.TaskResourceTypeTask,
		Id:   task.ID,
		Attributes: generated.TaskAttributes{
			Action: generated.TaskAction(task.Action), Note: task.Note, Completed: task.Completed,
			State: generated.TaskState(task.State), TaskType: generated.TaskType(task.TaskType),
			DueAt: task.DueAt, CompletedAt: task.CompletedAt, StateChangedAt: task.StateChangedAt,
			CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
		},
		Relationships: generated.TaskRelationships{Prospect: toOne("prospect", task.ProspectID)},
	}
	if task.SequenceID != "" {
		sequence := toOne("sequence", task.SequenceID)
		resource.Relationships.Sequence = &sequence
	}
	return resource
}

func (s *server) loadSequence(ctx context.Context, id string) (storedSequence, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, description, enabled, sequence_type, share_type, tags, created_at, updated_at
		FROM sequences WHERE id=?`, id)
	return scanSequence(row)
}

func (s *server) listProspects(ctx context.Context) ([]storedProspect, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, first_name, last_name, name, emails, title, company, mobile_phones, tags, created_at, updated_at
		FROM prospects ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prospects []storedProspect
	for rows.Next() {
		prospect, err := scanProspect(rows)
		if err != nil {
			return nil, err
		}
		prospects = append(prospects, prospect)
	}
	return prospects, rows.Err()
}

func (s *server) loadProspect(ctx context.Context, id string) (storedProspect, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, first_name, last_name, name, emails, title, company, mobile_phones, tags, created_at, updated_at
		FROM prospects WHERE id=?`, id)
	return scanProspect(row)
}

func (s *server) insertProspect(ctx context.Context, prospect storedProspect) error {
	position, err := nextPosition(ctx, s.db, "prospects")
	if err != nil {
		return err
	}
	emails, err := encodeStrings(prospect.Emails)
	if err != nil {
		return err
	}
	phones, err := encodeStrings(prospect.MobilePhones)
	if err != nil {
		return err
	}
	tags, err := encodeStrings(prospect.Tags)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO prospects
		(id, first_name, last_name, name, emails, title, company, mobile_phones, tags, created_at, updated_at, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		prospect.ID, prospect.FirstName, prospect.LastName, prospect.Name, emails, prospect.Title,
		prospect.Company, phones, tags, prospect.CreatedAt, prospect.UpdatedAt, position)
	return err
}

func (s *server) listSequenceStates(ctx context.Context, id, state, prospectID, sequenceID string) ([]storedSequenceState, error) {
	query := `SELECT id, state, prospect_id, sequence_id, created_at, updated_at, state_changed_at FROM sequence_states`
	clauses := []string{}
	args := []any{}
	if id != "" {
		clauses = append(clauses, "id=?")
		args = append(args, id)
	}
	if state != "" {
		clauses = append(clauses, "state=?")
		args = append(args, state)
	}
	if prospectID != "" {
		clauses = append(clauses, "prospect_id=?")
		args = append(args, prospectID)
	}
	if sequenceID != "" {
		clauses = append(clauses, "sequence_id=?")
		args = append(args, sequenceID)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY position, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []storedSequenceState
	for rows.Next() {
		item, err := scanSequenceState(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *server) loadSequenceState(ctx context.Context, id string) (storedSequenceState, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, state, prospect_id, sequence_id, created_at, updated_at, state_changed_at
		FROM sequence_states WHERE id=?`, id)
	return scanSequenceState(row)
}

func (s *server) listTasks(ctx context.Context, id, state, prospectID, sequenceID string) ([]storedTask, error) {
	query := `SELECT id, action, note, completed, state, task_type, due_at, completed_at, state_changed_at, created_at, updated_at, prospect_id, sequence_id
		FROM tasks`
	clauses := []string{}
	args := []any{}
	if id != "" {
		clauses = append(clauses, "id=?")
		args = append(args, id)
	}
	if state != "" {
		clauses = append(clauses, "state=?")
		args = append(args, state)
	}
	if prospectID != "" {
		clauses = append(clauses, "prospect_id=?")
		args = append(args, prospectID)
	}
	if sequenceID != "" {
		clauses = append(clauses, "sequence_id=?")
		args = append(args, sequenceID)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY position, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []storedTask
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *server) loadTask(ctx context.Context, id string) (storedTask, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, action, note, completed, state, task_type, due_at, completed_at, state_changed_at,
		created_at, updated_at, prospect_id, sequence_id FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

func (s *server) insertTask(ctx context.Context, task storedTask) error {
	position, err := nextPosition(ctx, s.db, "tasks")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tasks
		(id, action, note, completed, state, task_type, due_at, completed_at, state_changed_at, created_at, updated_at, prospect_id, sequence_id, position)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Action, task.Note, boolInt(task.Completed), task.State, task.TaskType, task.DueAt,
		task.CompletedAt, task.StateChangedAt, task.CreatedAt, task.UpdatedAt, task.ProspectID, task.SequenceID, position)
	return err
}

func (s *server) sequenceMembership(ctx context.Context, prospectID, sequenceID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sequence_states WHERE prospect_id=? AND sequence_id=?`, prospectID, sequenceID).Scan(&n)
	return n > 0, err
}

func (s *server) emailTaken(ctx context.Context, email string) (bool, error) {
	prospects, err := s.listProspects(ctx)
	if err != nil {
		return false, err
	}
	for _, prospect := range prospects {
		if contains(prospect.Emails, email) {
			return true, nil
		}
	}
	return false, nil
}

func (s *server) now() string { return s.clock.Now().UTC().Format(time.RFC3339) }

type scanner interface{ Scan(dest ...any) error }

func scanSequence(row scanner) (storedSequence, error) {
	var sequence storedSequence
	var enabled int
	var tags string
	err := row.Scan(&sequence.ID, &sequence.Name, &sequence.Description, &enabled, &sequence.SequenceType,
		&sequence.ShareType, &tags, &sequence.CreatedAt, &sequence.UpdatedAt)
	if err != nil {
		return storedSequence{}, err
	}
	sequence.Enabled = enabled != 0
	sequence.Tags, err = decodeStrings(tags)
	return sequence, err
}

func scanProspect(row scanner) (storedProspect, error) {
	var prospect storedProspect
	var emails, phones, tags string
	err := row.Scan(&prospect.ID, &prospect.FirstName, &prospect.LastName, &prospect.Name, &emails,
		&prospect.Title, &prospect.Company, &phones, &tags, &prospect.CreatedAt, &prospect.UpdatedAt)
	if err != nil {
		return storedProspect{}, err
	}
	if prospect.Emails, err = decodeStrings(emails); err != nil {
		return storedProspect{}, err
	}
	if prospect.MobilePhones, err = decodeStrings(phones); err != nil {
		return storedProspect{}, err
	}
	prospect.Tags, err = decodeStrings(tags)
	return prospect, err
}

func scanSequenceState(row scanner) (storedSequenceState, error) {
	var item storedSequenceState
	err := row.Scan(&item.ID, &item.State, &item.ProspectID, &item.SequenceID, &item.CreatedAt, &item.UpdatedAt, &item.StateChangedAt)
	return item, err
}

func scanTask(row scanner) (storedTask, error) {
	var task storedTask
	var completed int
	err := row.Scan(&task.ID, &task.Action, &task.Note, &completed, &task.State, &task.TaskType, &task.DueAt,
		&task.CompletedAt, &task.StateChangedAt, &task.CreatedAt, &task.UpdatedAt, &task.ProspectID, &task.SequenceID)
	task.Completed = completed != 0
	return task, err
}

func applyCompletion(task *storedTask, completed *bool, state *generated.TaskState, now string) error {
	if completed != nil && state != nil {
		stateComplete := *state == generated.TaskStateComplete
		if *completed != stateComplete {
			return errors.New("completed and state disagree")
		}
	}
	changed := false
	if completed != nil {
		task.Completed = *completed
		if task.Completed {
			task.State = string(generated.TaskStateComplete)
		} else if task.State == string(generated.TaskStateComplete) {
			task.State = string(generated.TaskStateIncomplete)
		}
		changed = true
	}
	if state != nil {
		task.State = string(*state)
		task.Completed = *state == generated.TaskStateComplete
		changed = true
	}
	if task.Completed {
		if task.CompletedAt == "" {
			task.CompletedAt = now
		}
	} else {
		task.CompletedAt = ""
	}
	if changed {
		task.StateChangedAt = now
	}
	return nil
}

func relationshipPair(prospect, sequence generated.ToOneRelationship, prospectType, sequenceType string) (string, string, string) {
	if prospect.Data.Type != prospectType || prospect.Data.Id == "" {
		return "", "", prospectType + " relationship is required"
	}
	if sequence.Data.Type != sequenceType || sequence.Data.Id == "" {
		return "", "", sequenceType + " relationship is required"
	}
	return prospect.Data.Id, sequence.Data.Id, ""
}

func nextPosition(ctx context.Context, db *sql.DB, table string) (int, error) {
	switch table {
	case "sequences", "prospects", "sequence_states", "tasks":
	default:
		return 0, fmt.Errorf("outreach: unknown table %s", table)
	}
	var n sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(position) FROM "+table).Scan(&n); err != nil {
		return 0, err
	}
	return int(n.Int64) + 1, nil
}

func relatedLink(collection, filter, id string) generated.RelatedLinks {
	var link generated.RelatedLinks
	link.Links.Related = fmt.Sprintf("%s/%s?%s=%s", apiBase, collection, filter, url.QueryEscape(id))
	return link
}

func toOne(resourceType, id string) generated.ToOneRelationship {
	return generated.ToOneRelationship{Data: generated.ResourceIdentifier{Type: resourceType, Id: id}}
}

func meta(count int) generated.CollectionMeta {
	return generated.CollectionMeta{Count: count, CountTruncated: false}
}

func notFound(resource, id string) generated.ErrorResponse {
	return errorResponse("resourceNotFound", "Resource Not Found", fmt.Sprintf("Could not find '%s' with ID '%s'.", resource, id))
}

func invalid(detail string) generated.ErrorResponse {
	return errorResponse("validationError", "Validation Error", detail)
}

func errorResponse(id, title, detail string) generated.ErrorResponse {
	return generated.ErrorResponse{Errors: []generated.ErrorObject{{Id: id, Title: title, Detail: detail}}}
}

func reject[T ~struct {
	generated.ErrorApplicationVndAPIPlusJSONResponse
}](body generated.ErrorResponse) T {
	return T{ErrorApplicationVndAPIPlusJSONResponse: generated.ErrorApplicationVndAPIPlusJSONResponse(body)}
}

func writeError(w http.ResponseWriter, status int, id, title, detail string) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse(id, title, detail))
}

func encodeStrings(items []string) (string, error) {
	if items == nil {
		items = []string{}
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func deref[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func str(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strSlice(value *[]string) []string {
	if value == nil || *value == nil {
		return []string{}
	}
	return *value
}
