package mailchimp

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/mailchimp/generated"
	"github.com/getkin/kin-openapi/openapi3"
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

func (r jsonResponse) VisitGetCampaignsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostCampaignsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetCampaignsIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostCampaignsIdActionsSendResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetListsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostListsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetListsIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetListsIdMembersResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPostListsIdMembersResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetListsIdMembersIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGetPingResponse(w http.ResponseWriter) error {
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
		return nil, fmt.Errorf("mailchimp: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("mailchimp: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("mailchimp: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("mailchimp: load OpenAPI: %w", err)
	}
	if spec.Components == nil {
		spec.Components = &openapi3.Components{}
	}
	if spec.Components.SecuritySchemes == nil {
		spec.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	spec.Components.SecuritySchemes["basicAuth"] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{Type: "http", Scheme: "bearer"},
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
		const prefix = "/3.0"
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

func subscriberHash(email string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func (s *server) GetPing(_ context.Context, _ generated.GetPingRequestObject) (generated.GetPingResponseObject, error) {
	return jsonResponse{body: map[string]any{"health_status": "Everything's Chimpy!"}}, nil
}

func (s *server) GetLists(ctx context.Context, _ generated.GetListsRequestObject) (generated.GetListsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM lists ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"lists": items, "total_items": len(items)}}, nil
}

func (s *server) PostLists(ctx context.Context, _ generated.PostListsRequestObject) (generated.PostListsResponseObject, error) {
	item := bodyMap(ctx)
	id, err := s.ids.Next(ctx, "mailchimp.list")
	if err != nil {
		return nil, err
	}
	item["id"] = id
	if _, ok := item["date_created"]; !ok {
		item["date_created"] = s.clock.Now().UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO lists(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) GetListsId(ctx context.Context, request generated.GetListsIdRequestObject) (generated.GetListsIdResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM lists WHERE id=?`, request.ListId))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"title": "Resource Not Found", "status": 404, "detail": "list not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) GetListsIdMembers(ctx context.Context, request generated.GetListsIdMembersRequestObject) (generated.GetListsIdMembersResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM members ORDER BY id`)
	if err != nil {
		return nil, err
	}
	filtered := []map[string]any{}
	for _, item := range items {
		if item["list_id"] == request.ListId {
			filtered = append(filtered, item)
		}
	}
	return jsonResponse{body: map[string]any{"members": filtered, "total_items": len(filtered)}}, nil
}

func (s *server) PostListsIdMembers(ctx context.Context, request generated.PostListsIdMembersRequestObject) (generated.PostListsIdMembersResponseObject, error) {
	item := bodyMap(ctx)
	email, _ := item["email_address"].(string)
	id := subscriberHash(email)
	item["id"] = id
	item["list_id"] = request.ListId
	if _, ok := item["status"]; !ok {
		item["status"] = "subscribed"
	}
	item["unique_email_id"] = id[:10]
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO members(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) GetListsIdMembersId(ctx context.Context, request generated.GetListsIdMembersIdRequestObject) (generated.GetListsIdMembersIdResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM members WHERE id=?`, request.SubscriberHash))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"title": "Resource Not Found", "status": 404, "detail": "member not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if item["list_id"] != request.ListId {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"title": "Resource Not Found", "status": 404, "detail": "member not found"}}, nil
	}
	return jsonResponse{body: item}, nil
}

func (s *server) GetCampaigns(ctx context.Context, _ generated.GetCampaignsRequestObject) (generated.GetCampaignsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM campaigns ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"campaigns": items, "total_items": len(items)}}, nil
}

func (s *server) PostCampaigns(ctx context.Context, _ generated.PostCampaignsRequestObject) (generated.PostCampaignsResponseObject, error) {
	item := bodyMap(ctx)
	id, err := s.ids.Next(ctx, "mailchimp.campaign")
	if err != nil {
		return nil, err
	}
	item["id"] = id
	if _, ok := item["status"]; !ok {
		item["status"] = "save"
	}
	item["create_time"] = s.clock.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO campaigns(id, body) VALUES(?, ?)`, id, string(raw)); err != nil {
		return nil, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) GetCampaignsId(ctx context.Context, request generated.GetCampaignsIdRequestObject) (generated.GetCampaignsIdResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM campaigns WHERE id=?`, request.CampaignId))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"title": "Resource Not Found", "status": 404, "detail": "campaign not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: item}, nil
}

func (s *server) PostCampaignsIdActionsSend(ctx context.Context, request generated.PostCampaignsIdActionsSendRequestObject) (generated.PostCampaignsIdActionsSendResponseObject, error) {
	item, err := scanJSON(s.db.QueryRowContext(ctx, `SELECT body FROM campaigns WHERE id=?`, request.CampaignId))
	if errors.Is(err, sql.ErrNoRows) {
		return jsonResponse{status: http.StatusNotFound, body: map[string]any{"title": "Resource Not Found", "status": 404, "detail": "campaign not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	item["status"] = "sent"
	item["send_time"] = s.clock.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE campaigns SET body=? WHERE id=?`, string(raw), request.CampaignId); err != nil {
		return nil, err
	}
	return jsonResponse{status: http.StatusNoContent}, nil
}
