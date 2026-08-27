package mailgun

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/mailgun/generated"
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

func (r jsonResponse) VisitGetV3DomainNameEventsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitPOSTV3DomainNameMessagesResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r jsonResponse) VisitGETV4DomainsResponse(w http.ResponseWriter) error {
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
		return nil, fmt.Errorf("mailgun: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("mailgun: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("mailgun: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("mailgun: load OpenAPI: %w", err)
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
	impl.handler = withRawBody(asMultipart(validator(generatedHandler)))
	return impl, nil
}

func asMultipart(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/") {
			next.ServeHTTP(w, request)
			return
		}
		raw := rawBody(request.Context())
		values, err := url.ParseQuery(string(raw))
		if err != nil {
			next.ServeHTTP(w, request)
			return
		}
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for key, items := range values {
			for _, value := range items {
				_ = writer.WriteField(key, value)
			}
		}
		if err := writer.Close(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cloned := request.Clone(request.Context())
		cloned.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
		cloned.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
		}
		cloned.ContentLength = int64(buf.Len())
		cloned.Header.Set("Content-Type", writer.FormDataContentType())
		next.ServeHTTP(w, cloned)
	})
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func parseForm(ctx context.Context) map[string]string {
	raw := rawBody(ctx)
	values := map[string]string{}
	parsed, err := url.ParseQuery(string(raw))
	if err == nil {
		for key, items := range parsed {
			if len(items) > 0 {
				values[key] = items[0]
			}
		}
	}
	return values
}

func (s *server) GETV4Domains(ctx context.Context, _ generated.GETV4DomainsRequestObject) (generated.GETV4DomainsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM domains ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"items": items, "total_count": len(items)}}, nil
}

func (s *server) GetV3DomainNameEvents(ctx context.Context, request generated.GetV3DomainNameEventsRequestObject) (generated.GetV3DomainNameEventsResponseObject, error) {
	items, err := listJSON(ctx, s.db, `SELECT body FROM events ORDER BY id`)
	if err != nil {
		return nil, err
	}
	filtered := []map[string]any{}
	for _, item := range items {
		domain, _ := item["domain"].(string)
		if domain == "" || domain == request.DomainName {
			filtered = append(filtered, item)
		}
	}
	return jsonResponse{body: map[string]any{"items": filtered, "paging": map[string]any{"next": "", "previous": ""}}}, nil
}

func (s *server) POSTV3DomainNameMessages(ctx context.Context, request generated.POSTV3DomainNameMessagesRequestObject) (generated.POSTV3DomainNameMessagesResponseObject, error) {
	fields := parseForm(ctx)
	id, err := s.ids.Next(ctx, "mailgun.message")
	if err != nil {
		return nil, err
	}
	messageID := "<" + id + "@" + request.DomainName + ">"
	now := s.clock.Now().UTC().Format(time.RFC3339)
	message := map[string]any{
		"id": messageID, "domain": request.DomainName,
		"from": fields["from"], "to": fields["to"], "subject": fields["subject"],
		"text": fields["text"], "html": fields["html"], "created_at": now,
	}
	event := map[string]any{
		"id": id, "event": "accepted", "recipient": fields["to"],
		"domain": request.DomainName, "message": map[string]any{"headers": map[string]any{"from": fields["from"], "to": fields["to"], "subject": fields["subject"], "message-id": messageID}},
		"timestamp": float64(s.clock.Now().UTC().Unix()),
	}
	rawMessage, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO messages(id, body) VALUES(?, ?)`, messageID, string(rawMessage)); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO events(id, body) VALUES(?, ?)`, id, string(rawEvent)); err != nil {
		return nil, err
	}
	return jsonResponse{body: map[string]any{"id": messageID, "message": "Queued. Thank you."}}, nil
}
