package pipedrive

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

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/pipedrive/generated"
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

func (r jsonResponse) VisitGetDealsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitAddDealResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetDealResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitUpdateDealResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetOrganizationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitAddOrganizationResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetOrganizationResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetPersonsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitAddPersonResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetPersonResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitUpdatePersonResponse(w http.ResponseWriter) error {
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
		return nil, fmt.Errorf("pipedrive: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("pipedrive: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("pipedrive: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("pipedrive: load OpenAPI: %w", err)
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
		const prefix = "/api/v2"
		if strings.HasPrefix(cloned.URL.Path, prefix+"/") || cloned.URL.Path == prefix {
			cloned.URL.Path = strings.TrimPrefix(cloned.URL.Path, prefix)
			if cloned.URL.Path == "" {
				cloned.URL.Path = "/"
			}
		}
		validator(generatedHandler).ServeHTTP(w, cloned)
	}))
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) nextInt(ctx context.Context, table string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(CAST(id AS INTEGER)), 0) + 1 FROM "+table).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *server) GetDeals(ctx context.Context, _ generated.GetDealsRequestObject) (generated.GetDealsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM deals ORDER BY CAST(id AS INTEGER)`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": items}}, nil
}

func (s *server) AddDeal(ctx context.Context, request generated.AddDealRequestObject) (generated.AddDealResponseObject, error) {
	item := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(item, asMap(request.Body))
	}
	next, err := s.nextInt(ctx, "deals")
	if err != nil {
		return nil, err
	}
	item["id"] = next
	if _, ok := item["add_time"]; !ok {
		item["add_time"] = s.clock.Now().UTC().Format("2006-01-02T15:04:05")
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO deals(id, body) VALUES(?, ?)`, next, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) GetDeal(ctx context.Context, request generated.GetDealRequestObject) (generated.GetDealResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM deals WHERE id=?`, request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"success": false, "error": "deal not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) UpdateDeal(ctx context.Context, request generated.UpdateDealRequestObject) (generated.UpdateDealResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM deals WHERE id=?`, request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"success": false, "error": "not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	patch := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(patch, asMap(request.Body))
	}
	mergeMap(item, patch)
	item["id"] = request.Id
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE deals SET body=? WHERE id=?`, string(raw), request.Id); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) GetOrganizations(ctx context.Context, _ generated.GetOrganizationsRequestObject) (generated.GetOrganizationsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM organizations ORDER BY CAST(id AS INTEGER)`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": items}}, nil
}

func (s *server) AddOrganization(ctx context.Context, request generated.AddOrganizationRequestObject) (generated.AddOrganizationResponseObject, error) {
	item := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(item, asMap(request.Body))
	}
	next, err := s.nextInt(ctx, "organizations")
	if err != nil {
		return nil, err
	}
	item["id"] = next
	if _, ok := item["add_time"]; !ok {
		item["add_time"] = s.clock.Now().UTC().Format("2006-01-02T15:04:05")
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO organizations(id, body) VALUES(?, ?)`, next, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) GetOrganization(ctx context.Context, request generated.GetOrganizationRequestObject) (generated.GetOrganizationResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM organizations WHERE id=?`, request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"success": false, "error": "organization not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) GetPersons(ctx context.Context, _ generated.GetPersonsRequestObject) (generated.GetPersonsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM persons ORDER BY CAST(id AS INTEGER)`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": items}}, nil
}

func (s *server) AddPerson(ctx context.Context, request generated.AddPersonRequestObject) (generated.AddPersonResponseObject, error) {
	item := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(item, asMap(request.Body))
	}
	next, err := s.nextInt(ctx, "persons")
	if err != nil {
		return nil, err
	}
	item["id"] = next
	if _, ok := item["add_time"]; !ok {
		item["add_time"] = s.clock.Now().UTC().Format("2006-01-02T15:04:05")
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO persons(id, body) VALUES(?, ?)`, next, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) GetPerson(ctx context.Context, request generated.GetPersonRequestObject) (generated.GetPersonResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM persons WHERE id=?`, request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"success": false, "error": "person not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}

func (s *server) UpdatePerson(ctx context.Context, request generated.UpdatePersonRequestObject) (generated.UpdatePersonResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM persons WHERE id=?`, request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"success": false, "error": "not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	patch := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(patch, asMap(request.Body))
	}
	mergeMap(item, patch)
	item["id"] = request.Id
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE persons SET body=? WHERE id=?`, string(raw), request.Id); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"success": true, "data": item}}, nil
}
