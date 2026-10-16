package airbyte

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/airbyte/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("airbyte: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("airbyte: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("airbyte: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("airbyte: load OpenAPI: %w", err)
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
			writeError(w, status, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListSources(ctx context.Context, request generated.ListSourcesRequestObject) (generated.ListSourcesResponseObject, error) {
	items, err := loadAll[fixtureSource](ctx, s.db, "sources")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureSource, 0, len(items))
	for _, item := range items {
		if workspaceAllowed(request.Params.WorkspaceIds, item.WorkspaceID) {
			filtered = append(filtered, item)
		}
	}
	page, previous, next := paginate(filtered, intOr(request.Params.Limit, 20), intOr(request.Params.Offset, 0), "/v1/sources")
	data := make([]generated.SourceResponse, 0, len(page))
	for _, item := range page {
		body, err := convert[generated.SourceResponse](item)
		if err != nil {
			return nil, err
		}
		data = append(data, body)
	}
	return generated.ListSources200JSONResponse{Data: data, Previous: previous, Next: next}, nil
}

func (s *server) GetSource(ctx context.Context, request generated.GetSourceRequestObject) (generated.GetSourceResponseObject, error) {
	item, err := loadOne[fixtureSource](ctx, s.db, "sources", request.SourceId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetSourcedefaultJSONResponse{Body: generated.ErrorModel{Message: "source not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := convert[generated.SourceResponse](item)
	if err != nil {
		return nil, err
	}
	return generated.GetSource200JSONResponse(body), nil
}

func (s *server) ListDestinations(ctx context.Context, request generated.ListDestinationsRequestObject) (generated.ListDestinationsResponseObject, error) {
	items, err := loadAll[fixtureDestination](ctx, s.db, "destinations")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureDestination, 0, len(items))
	for _, item := range items {
		if workspaceAllowed(request.Params.WorkspaceIds, item.WorkspaceID) {
			filtered = append(filtered, item)
		}
	}
	page, previous, next := paginate(filtered, intOr(request.Params.Limit, 20), intOr(request.Params.Offset, 0), "/v1/destinations")
	data := make([]generated.DestinationResponse, 0, len(page))
	for _, item := range page {
		body, err := convert[generated.DestinationResponse](item)
		if err != nil {
			return nil, err
		}
		data = append(data, body)
	}
	return generated.ListDestinations200JSONResponse{Data: data, Previous: previous, Next: next}, nil
}

func (s *server) GetDestination(ctx context.Context, request generated.GetDestinationRequestObject) (generated.GetDestinationResponseObject, error) {
	item, err := loadOne[fixtureDestination](ctx, s.db, "destinations", request.DestinationId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetDestinationdefaultJSONResponse{Body: generated.ErrorModel{Message: "destination not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := convert[generated.DestinationResponse](item)
	if err != nil {
		return nil, err
	}
	return generated.GetDestination200JSONResponse(body), nil
}

func (s *server) ListConnections(ctx context.Context, request generated.ListConnectionsRequestObject) (generated.ListConnectionsResponseObject, error) {
	items, err := loadAll[fixtureConnection](ctx, s.db, "connections")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureConnection, 0, len(items))
	for _, item := range items {
		if workspaceAllowed(request.Params.WorkspaceIds, item.WorkspaceID) {
			filtered = append(filtered, item)
		}
	}
	page, previous, next := paginate(filtered, intOr(request.Params.Limit, 20), intOr(request.Params.Offset, 0), "/v1/connections")
	data := make([]generated.ConnectionResponse, 0, len(page))
	for _, item := range page {
		body, err := convert[generated.ConnectionResponse](item)
		if err != nil {
			return nil, err
		}
		data = append(data, body)
	}
	return generated.ListConnections200JSONResponse{Data: data, Previous: previous, Next: next}, nil
}

func (s *server) GetConnection(ctx context.Context, request generated.GetConnectionRequestObject) (generated.GetConnectionResponseObject, error) {
	item, err := loadOne[fixtureConnection](ctx, s.db, "connections", request.ConnectionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetConnectiondefaultJSONResponse{Body: generated.ErrorModel{Message: "connection not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := convert[generated.ConnectionResponse](item)
	if err != nil {
		return nil, err
	}
	return generated.GetConnection200JSONResponse(body), nil
}

func (s *server) ListJobs(ctx context.Context, request generated.ListJobsRequestObject) (generated.ListJobsResponseObject, error) {
	jobs, err := s.filterJobs(ctx, request.Params)
	if err != nil {
		var bad *badRequest
		if errors.As(err, &bad) {
			return generated.ListJobsdefaultJSONResponse{Body: generated.ErrorModel{Message: bad.message}, StatusCode: http.StatusBadRequest}, nil
		}
		return nil, err
	}
	page, previous, next := paginate(jobs, intOr(request.Params.Limit, 20), intOr(request.Params.Offset, 0), "/v1/jobs")
	data := make([]generated.JobResponse, 0, len(page))
	for _, item := range page {
		body, err := convert[generated.JobResponse](item)
		if err != nil {
			return nil, err
		}
		data = append(data, body)
	}
	return generated.ListJobs200JSONResponse{Data: data, Previous: previous, Next: next}, nil
}

func (s *server) GetJob(ctx context.Context, request generated.GetJobRequestObject) (generated.GetJobResponseObject, error) {
	item, err := loadOne[fixtureJob](ctx, s.db, "jobs", request.JobId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetJobdefaultJSONResponse{Body: generated.ErrorModel{Message: "job not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := convert[generated.JobResponse](item)
	if err != nil {
		return nil, err
	}
	return generated.GetJob200JSONResponse(body), nil
}

func (s *server) CreateJob(ctx context.Context, request generated.CreateJobRequestObject) (generated.CreateJobResponseObject, error) {
	if request.Body == nil || request.Body.ConnectionId == "" || request.Body.JobType == "" {
		return generated.CreateJobdefaultJSONResponse{Body: generated.ErrorModel{Message: "connectionId and jobType are required"}, StatusCode: http.StatusBadRequest}, nil
	}
	if _, err := loadOne[fixtureConnection](ctx, s.db, "connections", request.Body.ConnectionId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.CreateJobdefaultJSONResponse{Body: generated.ErrorModel{Message: "connection not found"}, StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	id, err := s.ids.Next(ctx, "airbyte.job")
	if err != nil {
		return nil, fmt.Errorf("airbyte: allocate job ID: %w", err)
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	job := fixtureJob{
		JobID: id, Status: "running", JobType: string(request.Body.JobType),
		StartTime: now, ConnectionID: request.Body.ConnectionId, LastUpdatedAt: now,
	}
	if err := s.insertJob(ctx, job); err != nil {
		return nil, err
	}
	body, err := convert[generated.JobResponse](job)
	if err != nil {
		return nil, err
	}
	return generated.CreateJob200JSONResponse(body), nil
}

func (s *server) filterJobs(ctx context.Context, params generated.ListJobsParams) ([]fixtureJob, error) {
	jobs, err := loadAll[fixtureJob](ctx, s.db, "jobs")
	if err != nil {
		return nil, err
	}
	sources, err := loadAll[fixtureSource](ctx, s.db, "sources")
	if err != nil {
		return nil, err
	}
	connections, err := loadAll[fixtureConnection](ctx, s.db, "connections")
	if err != nil {
		return nil, err
	}
	sourceByID := map[string]fixtureSource{}
	for _, source := range sources {
		sourceByID[source.SourceID] = source
	}
	connectionByID := map[string]fixtureConnection{}
	for _, connection := range connections {
		connectionByID[connection.ConnectionID] = connection
	}
	filtered := make([]fixtureJob, 0, len(jobs))
	for _, job := range jobs {
		if params.ConnectionId != nil && *params.ConnectionId != "" && job.ConnectionID != *params.ConnectionId {
			continue
		}
		if params.JobType != nil && job.JobType != string(*params.JobType) {
			continue
		}
		if params.Status != nil && job.Status != string(*params.Status) {
			continue
		}
		workspace, err := jobWorkspace(job, sourceByID, connectionByID)
		if err != nil {
			return nil, err
		}
		if !workspaceAllowed(params.WorkspaceIds, workspace) {
			continue
		}
		createdOK, err := within(job.StartTime, deref(params.CreatedAtStart), deref(params.CreatedAtEnd), "createdAtStart", "createdAtEnd")
		if err != nil {
			return nil, err
		}
		updated := job.LastUpdatedAt
		if updated == "" {
			updated = job.StartTime
		}
		updatedOK, err := within(updated, deref(params.UpdatedAtStart), deref(params.UpdatedAtEnd), "updatedAtStart", "updatedAtEnd")
		if err != nil {
			return nil, err
		}
		if !createdOK || !updatedOK {
			continue
		}
		filtered = append(filtered, job)
	}
	if params.OrderBy != nil && *params.OrderBy != "" {
		sortJobs(filtered, *params.OrderBy)
	}
	return filtered, nil
}

func (s *server) insertJob(ctx context.Context, job fixtureJob) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seq int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) + 1 FROM jobs").Scan(&seq); err != nil {
		return err
	}
	if err := insertBody(ctx, tx, "jobs", seq, job.JobID, job); err != nil {
		return err
	}
	return tx.Commit()
}

func jobWorkspace(job fixtureJob, sources map[string]fixtureSource, connections map[string]fixtureConnection) (string, error) {
	if job.ConnectionID != "" {
		connection, ok := connections[job.ConnectionID]
		if !ok {
			return "", fmt.Errorf("airbyte: job %s references unknown connection %s", job.JobID, job.ConnectionID)
		}
		return connection.WorkspaceID, nil
	}
	source, ok := sources[job.SourceID]
	if !ok {
		return "", fmt.Errorf("airbyte: job %s references unknown source %s", job.JobID, job.SourceID)
	}
	return source.WorkspaceID, nil
}

func sortJobs(jobs []fixtureJob, orderBy string) {
	field, dir, ok := strings.Cut(orderBy, "|")
	if !ok {
		return
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		left, right := jobSortKey(jobs[i], field), jobSortKey(jobs[j], field)
		if dir == "ASC" {
			return left < right
		}
		return left > right
	})
}

func jobSortKey(job fixtureJob, field string) string {
	switch field {
	case "updatedAt":
		if job.LastUpdatedAt != "" {
			return job.LastUpdatedAt
		}
		return job.StartTime
	default:
		return job.StartTime
	}
}

func within(value, start, end, startName, endName string) (bool, error) {
	if start == "" && end == "" {
		return true, nil
	}
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return false, err
	}
	if start != "" {
		bound, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return false, &badRequest{message: "invalid " + startName}
		}
		if instant.Before(bound) {
			return false, nil
		}
	}
	if end != "" {
		bound, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return false, &badRequest{message: "invalid " + endName}
		}
		if instant.After(bound) {
			return false, nil
		}
	}
	return true, nil
}

func paginate[T any](items []T, limit, offset int, path string) ([]T, *string, *string) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	if page == nil {
		page = []T{}
	}
	var previous, next *string
	if offset > 0 {
		prev := offset - limit
		if prev < 0 {
			prev = 0
		}
		previous = stringPtr(fmt.Sprintf("https://api.airbyte.com%s?limit=%d&offset=%d", path, limit, prev))
	}
	if end < len(items) {
		next = stringPtr(fmt.Sprintf("https://api.airbyte.com%s?limit=%d&offset=%d", path, limit, end))
	}
	return page, previous, next
}

func workspaceAllowed(filter *[]string, workspace string) bool {
	ids := workspaceIDs(filter)
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == workspace {
			return true
		}
	}
	return false
}

func workspaceIDs(filter *[]string) []string {
	if filter == nil {
		return nil
	}
	out := []string{}
	for _, item := range *filter {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func convert[T any](value any) (T, error) {
	var out T
	raw, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.ErrorModel{Message: message})
}

func intOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string { return &value }
