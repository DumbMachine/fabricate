package intercom

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/intercom/generated"
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

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("intercom: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("intercom: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("intercom: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("intercom: load OpenAPI: %w", err)
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

func (s *server) IdentifyAdmin(ctx context.Context, _ generated.IdentifyAdminRequestObject) (generated.IdentifyAdminResponseObject, error) {
	var id, name, email string
	if err := s.db.QueryRowContext(ctx, `SELECT a.id, a.name, a.email FROM admins a
		JOIN metadata m ON m.value=a.id WHERE m.key='currentAdminId'`).Scan(&id, &name, &email); err != nil {
		return nil, err
	}
	typ := "admin"
	return generated.IdentifyAdmin200JSONResponse{Id: &id, Name: &name, Email: &email, Type: &typ}, nil
}

func (s *server) ListContacts(ctx context.Context, _ generated.ListContactsRequestObject) (generated.ListContactsResponseObject, error) {
	contacts, err := s.listContacts(ctx)
	if err != nil {
		return nil, err
	}
	typ := generated.ContactListTypeList
	total := len(contacts)
	return generated.ListContacts200JSONResponse{Type: &typ, Data: &contacts, TotalCount: &total}, nil
}

func (s *server) ShowContact(ctx context.Context, request generated.ShowContactRequestObject) (generated.ShowContactResponseObject, error) {
	contact, err := s.getContact(ctx, request.ContactId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ShowContact410JSONResponse{Body: apiError("not_found", "contact not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	var response generated.ShowContact200JSONResponse
	if err := remapJSON(contact, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *server) CreateContact(ctx context.Context, request generated.CreateContactRequestObject) (generated.CreateContactResponseObject, error) {
	fields := bodyFields(ctx)
	email, _ := fields["email"].(string)
	name, _ := fields["name"].(string)
	if strings.TrimSpace(email) == "" {
		return nil, fmt.Errorf("intercom: email is required")
	}
	if name == "" {
		name = email
	}
	id, err := s.ids.Next(ctx, "intercom.contact")
	if err != nil {
		return nil, err
	}
	now := int(s.clock.Now().UTC().Unix())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO contacts(id, name, email, role, created_at, updated_at) VALUES(?, ?, ?, 'user', ?, ?)`,
		id, name, email, now, now); err != nil {
		return nil, err
	}
	contact, err := s.getContact(ctx, id)
	if err != nil {
		return nil, err
	}
	var response generated.CreateContact200JSONResponse
	if err := remapJSON(contact, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *server) ListConversations(ctx context.Context, _ generated.ListConversationsRequestObject) (generated.ListConversationsResponseObject, error) {
	conversations, err := s.listConversations(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]generated.ConversationListItem, 0, len(conversations))
	for _, conversation := range conversations {
		var item generated.ConversationListItem
		if err := remapJSON(conversation, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	typ := generated.ConversationListTypeConversationList
	total := len(items)
	return generated.ListConversations200JSONResponse{Type: &typ, Conversations: &items, TotalCount: &total}, nil
}

func (s *server) RetrieveConversation(ctx context.Context, request generated.RetrieveConversationRequestObject) (generated.RetrieveConversationResponseObject, error) {
	conversation, err := s.getConversation(ctx, strconv.Itoa(request.ConversationId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveConversation404JSONResponse(apiError("not_found", "conversation not found")), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveConversation200JSONResponse(conversation), nil
}

func (s *server) UpdateConversation(ctx context.Context, request generated.UpdateConversationRequestObject) (generated.UpdateConversationResponseObject, error) {
	id := strconv.Itoa(request.ConversationId)
	conversation, err := s.getConversation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UpdateConversation404JSONResponse(apiError("not_found", "conversation not found")), nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body != nil && request.Body.Title != nil {
		now := int(s.clock.Now().UTC().Unix())
		if _, err := s.db.ExecContext(ctx, "UPDATE conversations SET title=?, updated_at=? WHERE id=?", *request.Body.Title, now, id); err != nil {
			return nil, err
		}
		conversation, err = s.getConversation(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	return generated.UpdateConversation200JSONResponse(conversation), nil
}

func (s *server) ReplyConversation(ctx context.Context, request generated.ReplyConversationRequestObject) (generated.ReplyConversationResponseObject, error) {
	if _, err := s.getConversation(ctx, request.ConversationId); errors.Is(err, sql.ErrNoRows) {
		return generated.ReplyConversation404JSONResponse(apiError("not_found", "conversation not found")), nil
	} else if err != nil {
		return nil, err
	}
	fields := bodyFields(ctx)
	body, _ := fields["body"].(string)
	if strings.TrimSpace(body) == "" {
		return generated.ReplyConversation403JSONResponse(apiError("parameter_invalid", "body is required")), nil
	}
	authorType, _ := fields["type"].(string)
	if authorType == "" {
		authorType = "admin"
	}
	authorID, _ := fields["admin_id"].(string)
	if authorID == "" {
		if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentAdminId'").Scan(&authorID); err != nil {
			return nil, err
		}
	}
	id, err := s.ids.Next(ctx, "intercom.part")
	if err != nil {
		return nil, err
	}
	now := int(s.clock.Now().UTC().Unix())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO conversation_parts(id, conversation_id, author_type, author_id, body, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`, id, request.ConversationId, authorType, authorID, body, now); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE conversations SET updated_at=? WHERE id=?", now, request.ConversationId); err != nil {
		return nil, err
	}
	conversation, err := s.getConversation(ctx, request.ConversationId)
	if err != nil {
		return nil, err
	}
	return generated.ReplyConversation200JSONResponse(conversation), nil
}

func (s *server) listContacts(ctx context.Context) ([]generated.Contact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, email, role, created_at, updated_at FROM contacts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []generated.Contact
	for rows.Next() {
		contact, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		contacts = append(contacts, contact)
	}
	if contacts == nil {
		contacts = []generated.Contact{}
	}
	return contacts, rows.Err()
}

func (s *server) getContact(ctx context.Context, id string) (generated.Contact, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, email, role, created_at, updated_at FROM contacts WHERE id=?", id)
	return scanContact(row)
}

func (s *server) listConversations(ctx context.Context) ([]generated.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, title, state, contact_id, created_at, updated_at FROM conversations ORDER BY id")
	if err != nil {
		return nil, err
	}
	var summaries []struct {
		id, title, state, contactID string
		createdAt, updatedAt        int
	}
	for rows.Next() {
		var item struct {
			id, title, state, contactID string
			createdAt, updatedAt        int
		}
		if err := rows.Scan(&item.id, &item.title, &item.state, &item.contactID, &item.createdAt, &item.updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		summaries = append(summaries, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var conversations []generated.Conversation
	for _, item := range summaries {
		conversations = append(conversations, conversationSummary(item.id, item.title, item.state, item.contactID, item.createdAt, item.updatedAt))
	}
	if conversations == nil {
		conversations = []generated.Conversation{}
	}
	return conversations, nil
}

func (s *server) getConversation(ctx context.Context, id string) (generated.Conversation, error) {
	var title, state, contactID string
	var createdAt, updatedAt int
	if err := s.db.QueryRowContext(ctx, "SELECT title, state, contact_id, created_at, updated_at FROM conversations WHERE id=?", id).
		Scan(&title, &state, &contactID, &createdAt, &updatedAt); err != nil {
		return generated.Conversation{}, err
	}
	conversation := conversationSummary(id, title, state, contactID, createdAt, updatedAt)
	partRows, err := s.db.QueryContext(ctx, `SELECT id, author_type, author_id, body, created_at
		FROM conversation_parts WHERE conversation_id=? ORDER BY created_at, id`, id)
	if err != nil {
		return generated.Conversation{}, err
	}
	defer partRows.Close()
	var parts []generated.ConversationPart
	for partRows.Next() {
		var partID, authorType, authorID, body string
		var created int
		if err := partRows.Scan(&partID, &authorType, &authorID, &body, &created); err != nil {
			return generated.Conversation{}, err
		}
		partType := "comment"
		parts = append(parts, generated.ConversationPart{
			Id: &partID, Body: &body, CreatedAt: &created, PartType: &partType,
			Author: &generated.ConversationPartAuthor{Type: &authorType, Id: &authorID},
		})
	}
	if err := partRows.Err(); err != nil {
		return generated.Conversation{}, err
	}
	listType := generated.ConversationPartList
	total := len(parts)
	conversation.ConversationParts = &generated.ConversationParts{Type: &listType, ConversationParts: &parts, TotalCount: &total}
	return conversation, nil
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

func bodyFields(ctx context.Context) map[string]any {
	fields := map[string]any{}
	raw, _ := ctx.Value(rawBodyKey{}).([]byte)
	if len(raw) == 0 {
		return fields
	}
	_ = json.Unmarshal(raw, &fields)
	return fields
}

func conversationSummary(id, title, state, contactID string, createdAt, updatedAt int) generated.Conversation {
	open := state == "open"
	convState := generated.ConversationState(state)
	contactType := generated.ConversationContactsTypeContactList
	contacts := []generated.ContactReference{{Id: &contactID}}
	return generated.Conversation{
		Id: &id, Title: &title, State: &convState, Open: &open,
		CreatedAt: &createdAt, UpdatedAt: &updatedAt,
		Contacts: &generated.ConversationContacts{Type: &contactType, Contacts: &contacts},
	}
}

func scanContact(row interface{ Scan(dest ...any) error }) (generated.Contact, error) {
	var id, name, email, role string
	var createdAt, updatedAt int
	if err := row.Scan(&id, &name, &email, &role, &createdAt, &updatedAt); err != nil {
		return generated.Contact{}, err
	}
	typ := "contact"
	return generated.Contact{Id: &id, Name: &name, Email: &email, Role: &role, Type: &typ, CreatedAt: &createdAt, UpdatedAt: &updatedAt}, nil
}

func remapJSON(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func apiError(code, message string) generated.Error {
	msg := message
	return generated.Error{
		Type: "error.list",
		Errors: []struct {
			Code    string  `json:"code"`
			Field   *string `json:"field,omitempty"`
			Message *string `json:"message,omitempty"`
		}{{Code: code, Message: &msg}},
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError("unauthorized", message))
}
