package close

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/close/generated"
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

type jsonResponse struct {
	status int
	body   any
}

func (r jsonResponse) write(w http.ResponseWriter) error {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	if r.body == nil {
		w.WriteHeader(status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(r.body)
}

func (r jsonResponse) VisitContactsListResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitContactsCreateResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitContactsGetResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitLeadsListResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitLeadsCreateResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitLeadsGetResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitLeadsUpdateResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitUsersGetMeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitOpportunitiesListResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitOpportunitiesCreateResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitOpportunitiesGetResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitOpportunitiesUpdateResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"message":%q}`, message)))
}

func scanJSON(row *sql.Row) (map[string]any, error) {
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	return decodeMap(raw)
}

func listJSON(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
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

func asMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	item := map[string]any{}
	_ = json.Unmarshal(raw, &item)
	return item
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		if value != nil {
			dst[key] = value
		}
	}
}

type rawBodyKey struct{}

func withRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Body == nil || (request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodPatch) {
			next.ServeHTTP(w, request)
			return
		}
		raw, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cloned := request.Clone(context.WithValue(request.Context(), rawBodyKey{}, raw))
		cloned.Body = io.NopCloser(bytes.NewReader(raw))
		cloned.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(raw)), nil
		}
		cloned.ContentLength = int64(len(raw))
		next.ServeHTTP(w, cloned)
	})
}

func rawBody(ctx context.Context) []byte {
	raw, _ := ctx.Value(rawBodyKey{}).([]byte)
	return raw
}

func bodyMap(ctx context.Context) map[string]any {
	fields := map[string]any{}
	_ = json.Unmarshal(rawBody(ctx), &fields)
	return fields
}

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("close: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("close: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("close: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("close: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{
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
	impl.handler = withRawBody(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		cloned := request.Clone(request.Context())
		path := cloned.URL.Path
		if strings.HasPrefix(path, "/api/v1/") {
			path = strings.TrimPrefix(path, "/api/v1")
		} else if path == "/api/v1" {
			path = "/"
		}
		if path != "/" && !strings.HasSuffix(path, "/") {
			path += "/"
		}
		cloned.URL.Path = path
		validator(generatedHandler).ServeHTTP(w, cloned)
	}))
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) listCollection(ctx context.Context, table string) (jsonResponse, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM `+table+` ORDER BY id`)
	if err != nil {
		return jsonResponse{}, err
	}
	return jsonResponse{body: map[string]any{"data": items, "has_more": false}}, nil
}

func (s *server) getItem(ctx context.Context, table, id string) (jsonResponse, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM `+table+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"error": table + " not found"}}, nil
	}
	if err != nil {
		return jsonResponse{}, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) createItem(ctx context.Context, table, prefix string, extra map[string]any) (jsonResponse, error) {
	item := bodyMap(ctx)
	mergeMap(item, extra)
	id, err := s.ids.Next(ctx, "close."+table)
	if err != nil {
		return jsonResponse{}, err
	}
	item["id"] = prefix + id
	if _, ok := item["date_created"]; !ok {
		item["date_created"] = s.clock.Now().UTC().Format(time.RFC3339)
	}
	item["date_updated"] = item["date_created"]
	raw, err := json.Marshal(item)
	if err != nil {
		return jsonResponse{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO `+table+`(id, body) VALUES(?, ?)`, item["id"], string(raw)); err != nil {
		return jsonResponse{}, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) updateItem(ctx context.Context, table, id string) (jsonResponse, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM `+table+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"error": "not found"}}, nil
	}
	if err != nil {
		return jsonResponse{}, err
	}
	mergeMap(item, bodyMap(ctx))
	item["id"] = id
	item["date_updated"] = s.clock.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(item)
	if err != nil {
		return jsonResponse{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET body=? WHERE id=?`, string(raw), id); err != nil {
		return jsonResponse{}, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) ContactsList(ctx context.Context, _ generated.ContactsListRequestObject) (generated.ContactsListResponseObject, error) {
	return s.listCollection(ctx, "contacts")
}
func (s *server) ContactsCreate(ctx context.Context, _ generated.ContactsCreateRequestObject) (generated.ContactsCreateResponseObject, error) {
	return s.createItem(ctx, "contacts", "cont_", nil)
}
func (s *server) ContactsGet(ctx context.Context, request generated.ContactsGetRequestObject) (generated.ContactsGetResponseObject, error) {
	return s.getItem(ctx, "contacts", request.Id)
}
func (s *server) LeadsList(ctx context.Context, _ generated.LeadsListRequestObject) (generated.LeadsListResponseObject, error) {
	return s.listCollection(ctx, "leads")
}
func (s *server) LeadsCreate(ctx context.Context, _ generated.LeadsCreateRequestObject) (generated.LeadsCreateResponseObject, error) {
	return s.createItem(ctx, "leads", "lead_", nil)
}
func (s *server) LeadsGet(ctx context.Context, request generated.LeadsGetRequestObject) (generated.LeadsGetResponseObject, error) {
	return s.getItem(ctx, "leads", request.Id)
}
func (s *server) LeadsUpdate(ctx context.Context, request generated.LeadsUpdateRequestObject) (generated.LeadsUpdateResponseObject, error) {
	return s.updateItem(ctx, "leads", request.Id)
}
func (s *server) UsersGetMe(ctx context.Context, _ generated.UsersGetMeRequestObject) (generated.UsersGetMeResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"error": "user not found"}}, nil
	}
	return jsonResponse{body: items[0]}, nil
}
func (s *server) OpportunitiesList(ctx context.Context, _ generated.OpportunitiesListRequestObject) (generated.OpportunitiesListResponseObject, error) {
	return s.listCollection(ctx, "opportunities")
}
func (s *server) OpportunitiesCreate(ctx context.Context, _ generated.OpportunitiesCreateRequestObject) (generated.OpportunitiesCreateResponseObject, error) {
	return s.createItem(ctx, "opportunities", "oppo_", nil)
}
func (s *server) OpportunitiesGet(ctx context.Context, request generated.OpportunitiesGetRequestObject) (generated.OpportunitiesGetResponseObject, error) {
	return s.getItem(ctx, "opportunities", request.Id)
}
func (s *server) OpportunitiesUpdate(ctx context.Context, request generated.OpportunitiesUpdateRequestObject) (generated.OpportunitiesUpdateResponseObject, error) {
	return s.updateItem(ctx, "opportunities", request.Id)
}
