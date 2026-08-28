package attio

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/attio/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const workspaceID = "0a0a0a0a-0a0a-4a0a-8a0a-0a0a0a0a0a0a"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("attio: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("attio: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("attio: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("attio: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{
			// Attio record values are attribute-typed unions; the live API
			// accepts compact write shapes the generated oneOfs reject.
			ExcludeRequestBody: true,
			AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
				header := input.RequestValidationInput.Request.Header.Get("Authorization")
				if header != "Bearer "+token {
					return input.NewError(errors.New("invalid synthetic bearer token"))
				}
				return nil
			},
		},
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

func (s *server) GetV2Objects(ctx context.Context, _ generated.GetV2ObjectsRequestObject) (generated.GetV2ObjectsResponseObject, error) {
	items, err := s.listJSON(ctx, `SELECT body FROM objects ORDER BY api_slug`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": items}}, nil
}

func (s *server) PostV2ObjectsObjectRecordsQuery(ctx context.Context, request generated.PostV2ObjectsObjectRecordsQueryRequestObject) (generated.PostV2ObjectsObjectRecordsQueryResponseObject, error) {
	if _, err := s.objectBody(ctx, request.Object); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return jsonResponse{status: http.StatusNotFound, body: errorPayload("object not found")}, nil
		}
		return nil, err
	}
	items, err := s.listJSON(ctx, `SELECT body FROM records WHERE object_slug=? ORDER BY record_id`, request.Object)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": items}}, nil
}

func (s *server) PostV2ObjectsObjectRecords(ctx context.Context, request generated.PostV2ObjectsObjectRecordsRequestObject) (generated.PostV2ObjectsObjectRecordsResponseObject, error) {
	object, err := s.objectBody(ctx, request.Object)
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: errorPayload("object not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	recordID, err := s.ids.Next(ctx, "attio.record")
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if request.Body != nil && request.Body.Data.Values != nil {
		for key, value := range request.Body.Data.Values {
			values[key] = value
		}
	}
	record := map[string]any{
		"id": map[string]any{
			"workspace_id": workspaceIDFrom(object),
			"object_id":    objectIDFrom(object),
			"record_id":    recordID,
		},
		"created_at": s.clock.Now().UTC().Format(time.RFC3339Nano),
		"values":     values,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO records(object_slug, record_id, body) VALUES(?, ?, ?)`, request.Object, recordID, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": record}}, nil
}

func (s *server) GetV2ObjectsObjectRecordsRecordId(ctx context.Context, request generated.GetV2ObjectsObjectRecordsRecordIdRequestObject) (generated.GetV2ObjectsObjectRecordsRecordIdResponseObject, error) {
	record, err := s.recordBody(ctx, request.Object, request.RecordId.String())
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: errorPayload("record not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": record}}, nil
}

func (s *server) PatchV2ObjectsObjectRecordsRecordId(ctx context.Context, request generated.PatchV2ObjectsObjectRecordsRecordIdRequestObject) (generated.PatchV2ObjectsObjectRecordsRecordIdResponseObject, error) {
	record, err := s.recordBody(ctx, request.Object, request.RecordId)
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: errorPayload("record not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	values, _ := record["values"].(map[string]any)
	if values == nil {
		values = map[string]any{}
	}
	if request.Body != nil {
		raw, err := json.Marshal(request.Body)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Data struct {
				Values map[string]any `json:"values"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		for key, value := range payload.Data.Values {
			values[key] = value
		}
	}
	record["values"] = values
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE records SET body=? WHERE object_slug=? AND record_id=?`, string(raw), request.Object, request.RecordId); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": record}}, nil
}

func (s *server) GetV2WorkspaceMembers(ctx context.Context, _ generated.GetV2WorkspaceMembersRequestObject) (generated.GetV2WorkspaceMembersResponseObject, error) {
	items, err := s.listJSON(ctx, `SELECT body FROM workspace_members ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"data": items}}, nil
}

func (s *server) objectBody(ctx context.Context, slug string) (map[string]any, error) {
	return scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM objects WHERE api_slug=?`, slug))
}

func (s *server) recordBody(ctx context.Context, objectSlug, recordID string) (map[string]any, error) {
	return scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM records WHERE object_slug=? AND record_id=?`, objectSlug, recordID))
}

func (s *server) listJSON(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item, err := decodeMap(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanJSON(row *sql.Row) (map[string]any, error) {
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	return decodeMap(raw)
}

func objectIDFrom(object map[string]any) string {
	if id := nestedString(object, "id", "object_id"); id != "" {
		return id
	}
	return ""
}

func workspaceIDFrom(object map[string]any) string {
	if id := nestedString(object, "id", "workspace_id"); id != "" {
		return id
	}
	return workspaceID
}

type jsonResponse struct {
	status int
	body   any
}

func (r jsonResponse) write(w http.ResponseWriter) error {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.body == nil {
		return nil
	}
	return json.NewEncoder(w).Encode(r.body)
}

func (r jsonResponse) VisitGetV2ObjectsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostV2ObjectsObjectRecordsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostV2ObjectsObjectRecordsQueryResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetV2ObjectsObjectRecordsRecordIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPatchV2ObjectsObjectRecordsRecordIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetV2WorkspaceMembersResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func errorPayload(message string) map[string]any {
	return map[string]any{"status_code": http.StatusNotFound, "type": "invalid_request_error", "code": "not_found", "message": message}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.Copy(w, bytes.NewReader([]byte(fmt.Sprintf(`{"status_code":%d,"type":"invalid_request_error","code":"validation_error","message":%q}`, status, message))))
}
