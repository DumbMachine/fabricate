package digitalocean

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
	"github.com/dumbmachine/fabricate/resources/digitalocean/generated"
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

func (r jsonResponse) VisitListDropletsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetDropletResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitCreateDropletResponse(w http.ResponseWriter) error {
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
		return nil, fmt.Errorf("digitalocean: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("digitalocean: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("digitalocean: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("digitalocean: load OpenAPI: %w", err)
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
	impl.handler = withRawBody(validator(generatedHandler))
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListDroplets(ctx context.Context, _ generated.ListDropletsRequestObject) (generated.ListDropletsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM droplets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"droplets": items}}, nil
}

func (s *server) GetDroplet(ctx context.Context, request generated.GetDropletRequestObject) (generated.GetDropletResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM droplets WHERE id=?`, request.DropletId))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"message": "not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"droplet": item}}, nil
}

func (s *server) CreateDroplet(ctx context.Context, request generated.CreateDropletRequestObject) (generated.CreateDropletResponseObject, error) {
	item := bodyMap(ctx)
	if request.Body != nil {
		mergeMap(item, asMap(request.Body))
	}
	required, _ := item["name"].(string)
	if strings.TrimSpace(required) == "" {
		return jsonResponse{status: http.StatusBadRequest, body: map[string]any{"message": "name is required"}}, nil
	}
	id, err := s.ids.Next(ctx, "digitalocean.droplet")
	if err != nil {
		return nil, err
	}
	item["id"] = id
	if _, ok := item["region"]; !ok {
		item["region"] = "nyc1"
	}
	if _, ok := item["status"]; !ok {
		item["status"] = "active"
	}
	if _, ok := item["created_at"]; !ok {
		if _, ok := item["createdAt"]; !ok {
			item["created_at"] = s.clock.Now().UTC().Format(time.RFC3339)
		}
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO droplets(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"droplet": item}}, nil
}
