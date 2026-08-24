package gmail

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/gmail/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
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
		return nil, fmt.Errorf("gmail: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("gmail: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("gmail: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("gmail: load OpenAPI: %w", err)
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
			code := "INVALID_ARGUMENT"
			if status == http.StatusUnauthorized {
				code = "UNAUTHENTICATED"
			}
			writeError(w, status, code, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GmailUsersGetProfile(ctx context.Context, _ generated.GmailUsersGetProfileRequestObject) (generated.GmailUsersGetProfileResponseObject, error) {
	var email string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='emailAddress'").Scan(&email); err != nil {
		return nil, err
	}
	var messages, threads int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*), COUNT(DISTINCT thread_id) FROM messages").Scan(&messages, &threads); err != nil {
		return nil, err
	}
	return generated.GmailUsersGetProfile200JSONResponse{
		EmailAddress: openapi_types.Email(email), MessagesTotal: messages, ThreadsTotal: threads, HistoryId: "1",
	}, nil
}

func (s *server) GmailUsersLabelsList(ctx context.Context, _ generated.GmailUsersLabelsListRequestObject) (generated.GmailUsersLabelsListResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, type FROM labels ORDER BY type DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := []generated.Label{}
	for rows.Next() {
		var label generated.Label
		if err := rows.Scan(&label.Id, &label.Name, &label.Type); err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return generated.GmailUsersLabelsList200JSONResponse{Labels: labels}, rows.Err()
}

func (s *server) GmailUsersLabelsGet(ctx context.Context, request generated.GmailUsersLabelsGetRequestObject) (generated.GmailUsersLabelsGetResponseObject, error) {
	var label generated.Label
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type FROM labels WHERE id=?", request.Id).Scan(&label.Id, &label.Name, &label.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GmailUsersLabelsGetdefaultJSONResponse{Body: errorEnvelope(404, "NOT_FOUND", "Label not found"), StatusCode: 404}, nil
	}
	if err != nil {
		return nil, err
	}
	messages, err := loadMessages(ctx, s.db)
	if err != nil {
		return nil, err
	}
	threadSet, unreadThreadSet := map[string]struct{}{}, map[string]struct{}{}
	total, unread := 0, 0
	for _, message := range messages {
		if !message.hasLabel(label.Id) {
			continue
		}
		total++
		threadSet[message.ThreadID] = struct{}{}
		if message.hasLabel("UNREAD") {
			unread++
			unreadThreadSet[message.ThreadID] = struct{}{}
		}
	}
	label.MessagesTotal, label.MessagesUnread = ptr(total), ptr(unread)
	label.ThreadsTotal, label.ThreadsUnread = ptr(len(threadSet)), ptr(len(unreadThreadSet))
	return generated.GmailUsersLabelsGet200JSONResponse(label), nil
}

func (s *server) GmailUsersMessagesList(ctx context.Context, request generated.GmailUsersMessagesListRequestObject) (generated.GmailUsersMessagesListResponseObject, error) {
	messages, err := loadMessages(ctx, s.db)
	if err != nil {
		return nil, err
	}
	query := parseQuery(value(request.Params.Q))
	labels := sliceValue(request.Params.LabelIds)
	includeTrash := boolValue(request.Params.IncludeSpamTrash) || contains(labels, "TRASH") || query.wantsTrash()
	filtered := make([]mailMessage, 0, len(messages))
	for _, message := range messages {
		if (!includeTrash && (message.hasLabel("TRASH") || message.hasLabel("SPAM"))) || !matchesLabels(message, labels) || !query.matches(message) {
			continue
		}
		filtered = append(filtered, message)
	}
	offset, err := pageOffset(request.Params.PageToken, len(filtered))
	if err != nil {
		return generated.GmailUsersMessagesListdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", err.Error()), StatusCode: 400}, nil
	}
	limit := 100
	if request.Params.MaxResults != nil {
		limit = *request.Params.MaxResults
	}
	end := min(offset+limit, len(filtered))
	refs := make([]generated.MessageRef, 0, end-offset)
	for _, message := range filtered[offset:end] {
		refs = append(refs, generated.MessageRef{Id: message.ID, ThreadId: message.ThreadID})
	}
	response := generated.ListMessagesResponse{Messages: refs, ResultSizeEstimate: len(filtered)}
	if end < len(filtered) {
		next := strconv.Itoa(end)
		response.NextPageToken = &next
	}
	return generated.GmailUsersMessagesList200JSONResponse(response), nil
}

func (s *server) GmailUsersMessagesGet(ctx context.Context, request generated.GmailUsersMessagesGetRequestObject) (generated.GmailUsersMessagesGetResponseObject, error) {
	message, err := getMessage(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GmailUsersMessagesGetdefaultJSONResponse{Body: errorEnvelope(404, "NOT_FOUND", "Message not found"), StatusCode: 404}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GmailUsersMessagesGet200JSONResponse(message.full()), nil
}

func (s *server) GmailUsersMessagesModify(ctx context.Context, request generated.GmailUsersMessagesModifyRequestObject) (generated.GmailUsersMessagesModifyResponseObject, error) {
	if request.Body == nil {
		return generated.GmailUsersMessagesModifydefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	message, err := s.applyLabels(ctx, request.Id, sliceValue(request.Body.AddLabelIds), sliceValue(request.Body.RemoveLabelIds))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GmailUsersMessagesModifydefaultJSONResponse{Body: errorEnvelope(404, "NOT_FOUND", "Message not found"), StatusCode: 404}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GmailUsersMessagesModify200JSONResponse(message.full()), nil
}

func (s *server) GmailUsersMessagesTrash(ctx context.Context, request generated.GmailUsersMessagesTrashRequestObject) (generated.GmailUsersMessagesTrashResponseObject, error) {
	message, err := s.applyLabels(ctx, request.Id, []string{"TRASH"}, []string{"INBOX", "UNREAD"})
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GmailUsersMessagesTrashdefaultJSONResponse{Body: errorEnvelope(404, "NOT_FOUND", "Message not found"), StatusCode: 404}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GmailUsersMessagesTrash200JSONResponse(message.full()), nil
}

func (s *server) GmailUsersMessagesSend(ctx context.Context, request generated.GmailUsersMessagesSendRequestObject) (generated.GmailUsersMessagesSendResponseObject, error) {
	if request.Body == nil || request.Body.Raw == "" {
		return generated.GmailUsersMessagesSenddefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "raw is required"), StatusCode: 400}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(request.Body.Raw, "="))
	if err != nil {
		return generated.GmailUsersMessagesSenddefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "raw is not valid base64url"), StatusCode: 400}, nil
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return generated.GmailUsersMessagesSenddefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "raw is not a valid RFC 2822 message"), StatusCode: 400}, nil
	}
	body, err := io.ReadAll(io.LimitReader(parsed.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	var from string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='emailAddress'").Scan(&from); err != nil {
		return nil, err
	}
	id, err := s.ids.Next(ctx, "gmail.message")
	if err != nil {
		return nil, fmt.Errorf("gmail: allocate message ID: %w", err)
	}
	threadID := id
	if request.Body.ThreadId != nil && *request.Body.ThreadId != "" {
		threadID = *request.Body.ThreadId
	}
	labels := []string{"SENT"}
	encodedLabels, _ := json.Marshal(labels)
	message := mailMessage{ID: id, ThreadID: threadID, From: from, To: parsed.Header.Get("To"), Subject: parsed.Header.Get("Subject"), Body: string(body), InternalDate: s.clock.Now().UnixMilli(), LabelIDs: labels}
	_, err = s.db.ExecContext(ctx, `INSERT INTO messages
		(id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, message.ID, message.ThreadID, message.From, message.To,
		message.Subject, message.Body, message.InternalDate, string(encodedLabels))
	if err != nil {
		return nil, err
	}
	return generated.GmailUsersMessagesSend200JSONResponse(message.full()), nil
}

func (s *server) GmailUsersThreadsList(ctx context.Context, request generated.GmailUsersThreadsListRequestObject) (generated.GmailUsersThreadsListResponseObject, error) {
	messages, err := loadMessages(ctx, s.db)
	if err != nil {
		return nil, err
	}
	query := parseQuery(value(request.Params.Q))
	labels := sliceValue(request.Params.LabelIds)
	includeTrash := boolValue(request.Params.IncludeSpamTrash) || contains(labels, "TRASH") || query.wantsTrash()
	refs := []generated.ThreadRef{}
	seen := map[string]struct{}{}
	for _, message := range messages {
		if _, exists := seen[message.ThreadID]; exists {
			continue
		}
		if (!includeTrash && (message.hasLabel("TRASH") || message.hasLabel("SPAM"))) || !matchesLabels(message, labels) || !query.matches(message) {
			continue
		}
		seen[message.ThreadID] = struct{}{}
		refs = append(refs, generated.ThreadRef{Id: message.ThreadID, Snippet: message.snippet(), HistoryId: "1"})
	}
	offset, err := pageOffset(request.Params.PageToken, len(refs))
	if err != nil {
		return generated.GmailUsersThreadsListdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", err.Error()), StatusCode: 400}, nil
	}
	limit := 100
	if request.Params.MaxResults != nil {
		limit = *request.Params.MaxResults
	}
	end := min(offset+limit, len(refs))
	response := generated.ListThreadsResponse{Threads: refs[offset:end], ResultSizeEstimate: len(refs)}
	if end < len(refs) {
		next := strconv.Itoa(end)
		response.NextPageToken = &next
	}
	return generated.GmailUsersThreadsList200JSONResponse(response), nil
}

func (s *server) GmailUsersThreadsGet(ctx context.Context, request generated.GmailUsersThreadsGetRequestObject) (generated.GmailUsersThreadsGetResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE thread_id=? ORDER BY internal_date, id", request.Id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []generated.Message{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message.full())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return generated.GmailUsersThreadsGetdefaultJSONResponse{Body: errorEnvelope(404, "NOT_FOUND", "Thread not found"), StatusCode: 404}, nil
	}
	return generated.GmailUsersThreadsGet200JSONResponse{Id: request.Id, HistoryId: "1", Snippet: messages[len(messages)-1].Snippet, Messages: messages}, nil
}

func (s *server) applyLabels(ctx context.Context, id string, add, remove []string) (mailMessage, error) {
	message, err := getMessage(ctx, s.db, id)
	if err != nil {
		return mailMessage{}, err
	}
	set := map[string]struct{}{}
	for _, label := range message.LabelIDs {
		set[label] = struct{}{}
	}
	for _, label := range add {
		set[label] = struct{}{}
	}
	for _, label := range remove {
		delete(set, label)
	}
	message.LabelIDs = message.LabelIDs[:0]
	for label := range set {
		message.LabelIDs = append(message.LabelIDs, label)
	}
	sort.Strings(message.LabelIDs)
	labels, _ := json.Marshal(message.LabelIDs)
	_, err = s.db.ExecContext(ctx, "UPDATE messages SET label_ids=? WHERE id=?", string(labels), id)
	return message, err
}

type mailMessage struct {
	ID, ThreadID, From, To, Subject, Body string
	InternalDate                          int64
	LabelIDs                              []string
}

const messageColumns = "id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids"

func loadMessages(ctx context.Context, db *sql.DB) ([]mailMessage, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+messageColumns+" FROM messages ORDER BY internal_date DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mailMessage{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func getMessage(ctx context.Context, db *sql.DB, id string) (mailMessage, error) {
	return scanMessage(db.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id=?", id))
}

func scanMessage(row interface{ Scan(...any) error }) (mailMessage, error) {
	var message mailMessage
	var labels string
	err := row.Scan(&message.ID, &message.ThreadID, &message.From, &message.To, &message.Subject, &message.Body, &message.InternalDate, &labels)
	if err != nil {
		return mailMessage{}, err
	}
	if err := json.Unmarshal([]byte(labels), &message.LabelIDs); err != nil {
		return mailMessage{}, err
	}
	return message, nil
}

func (m mailMessage) full() generated.Message {
	data := base64.RawURLEncoding.EncodeToString([]byte(m.Body))
	payload := generated.MessagePart{
		MimeType: "text/plain", Headers: []generated.MessagePartHeader{
			{Name: "From", Value: m.From}, {Name: "To", Value: m.To}, {Name: "Subject", Value: m.Subject},
			{Name: "Date", Value: time.UnixMilli(m.InternalDate).UTC().Format(time.RFC1123Z)},
		}, Body: generated.MessagePartBody{Size: len(m.Body), Data: &data},
	}
	return generated.Message{Id: m.ID, ThreadId: m.ThreadID, LabelIds: m.LabelIDs, Snippet: m.snippet(), HistoryId: "1", InternalDate: strconv.FormatInt(m.InternalDate, 10), SizeEstimate: len(m.Body), Payload: &payload}
}

func (m mailMessage) snippet() string {
	snippet := strings.Join(strings.Fields(m.Body), " ")
	if len(snippet) > 120 {
		snippet = snippet[:120] + "…"
	}
	return snippet
}

func (m mailMessage) hasLabel(id string) bool { return contains(m.LabelIDs, id) }

type searchQuery struct {
	unread, starred                  bool
	labels, from, to, subject, words []string
}

func parseQuery(raw string) searchQuery {
	var query searchQuery
	for _, token := range strings.Fields(raw) {
		lower := strings.ToLower(token)
		switch {
		case lower == "is:unread":
			query.unread = true
		case lower == "is:starred":
			query.starred = true
		case strings.HasPrefix(lower, "in:"), strings.HasPrefix(lower, "label:"):
			query.labels = append(query.labels, strings.SplitN(token, ":", 2)[1])
		case strings.HasPrefix(lower, "from:"):
			query.from = append(query.from, strings.TrimPrefix(lower, "from:"))
		case strings.HasPrefix(lower, "to:"):
			query.to = append(query.to, strings.TrimPrefix(lower, "to:"))
		case strings.HasPrefix(lower, "subject:"):
			query.subject = append(query.subject, strings.TrimPrefix(lower, "subject:"))
		case strings.Contains(lower, ":"):
		default:
			query.words = append(query.words, lower)
		}
	}
	return query
}

func (q searchQuery) wantsTrash() bool {
	for _, label := range q.labels {
		if strings.EqualFold(label, "trash") || strings.EqualFold(label, "spam") {
			return true
		}
	}
	return false
}

func (q searchQuery) matches(message mailMessage) bool {
	if q.unread && !message.hasLabel("UNREAD") || q.starred && !message.hasLabel("STARRED") {
		return false
	}
	for _, label := range q.labels {
		if !hasLabelFold(message, label) {
			return false
		}
	}
	for _, from := range q.from {
		if !strings.Contains(strings.ToLower(message.From), from) {
			return false
		}
	}
	for _, to := range q.to {
		if !strings.Contains(strings.ToLower(message.To), to) {
			return false
		}
	}
	for _, subject := range q.subject {
		if !strings.Contains(strings.ToLower(message.Subject), subject) {
			return false
		}
	}
	haystack := strings.ToLower(message.Subject + " " + message.Body)
	for _, word := range q.words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

func hasLabelFold(message mailMessage, wanted string) bool {
	for _, label := range message.LabelIDs {
		if strings.EqualFold(label, wanted) || strings.EqualFold(strings.TrimPrefix(label, "Label_"), wanted) {
			return true
		}
	}
	return false
}

func matchesLabels(message mailMessage, labels []string) bool {
	for _, label := range labels {
		if !message.hasLabel(label) {
			return false
		}
	}
	return true
}

func pageOffset(token *string, size int) (int, error) {
	if token == nil || *token == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(*token)
	if err != nil || offset < 0 || offset > size {
		return 0, fmt.Errorf("invalid pageToken")
	}
	return offset, nil
}

func errorEnvelope(status int, code, message string) generated.ErrorEnvelope {
	return generated.ErrorEnvelope{Error: generated.ErrorBody{Code: status, Message: message, Status: code, Errors: []generated.ErrorDetail{{Domain: "global", Reason: strings.ToLower(code), Message: message}}}}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope(status, code, message))
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func ptr[T any](value T) *T { return &value }
func value[T ~string](pointer *T) string {
	if pointer == nil {
		return ""
	}
	return string(*pointer)
}
func sliceValue[T any](pointer *[]T) []T {
	if pointer == nil {
		return nil
	}
	return *pointer
}
func boolValue(pointer *bool) bool { return pointer != nil && *pointer }
