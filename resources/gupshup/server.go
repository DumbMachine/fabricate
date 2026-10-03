package gupshup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/gupshup/generated"
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

var errWrongApp = errors.New("invalid app id")

var templatePlaceholder = regexp.MustCompile(`\{\{\d+\}\}`)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("gupshup: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("gupshup: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("gupshup: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("gupshup: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			request := input.RequestValidationInput.Request
			switch input.SecuritySchemeName {
			case "bearerAuth":
				if request.Header.Get("Authorization") != "Bearer "+token {
					return input.NewError(errors.New("Authentication Failed"))
				}
			case "apiKeyAuth":
				if request.Header.Get("apikey") != token {
					return input.NewError(errors.New("Authentication Failed"))
				}
			default:
				return input.NewError(errors.New("Authentication Failed"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			message := err.Error()
			if status == http.StatusUnauthorized {
				message = "Authentication Failed"
			}
			writeError(w, status, message)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GupshupAppBusinessGet(ctx context.Context, request generated.GupshupAppBusinessGetRequestObject) (generated.GupshupAppBusinessGetResponseObject, error) {
	app, err := s.appFor(ctx, request.AppId)
	if err != nil {
		return appError(err, func(status int, message string) generated.GupshupAppBusinessGetResponseObject {
			return generated.GupshupAppBusinessGetdefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	return generated.GupshupAppBusinessGet200JSONResponse{Status: "success", Business: businessAPI(app.Business)}, nil
}

func (s *server) GupshupAppProfileGet(ctx context.Context, request generated.GupshupAppProfileGetRequestObject) (generated.GupshupAppProfileGetResponseObject, error) {
	app, err := s.appFor(ctx, request.AppId)
	if err != nil {
		return appError(err, func(status int, message string) generated.GupshupAppProfileGetResponseObject {
			return generated.GupshupAppProfileGetdefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	return generated.GupshupAppProfileGet200JSONResponse{Status: "success", Profile: profileAPI(app.Profile)}, nil
}

func (s *server) GupshupAppProfileAboutGet(ctx context.Context, request generated.GupshupAppProfileAboutGetRequestObject) (generated.GupshupAppProfileAboutGetResponseObject, error) {
	app, err := s.appFor(ctx, request.AppId)
	if err != nil {
		return appError(err, func(status int, message string) generated.GupshupAppProfileAboutGetResponseObject {
			return generated.GupshupAppProfileAboutGetdefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	return generated.GupshupAppProfileAboutGet200JSONResponse{Status: "success", About: generated.About{Message: app.About}}, nil
}

func (s *server) GupshupTemplatesList(ctx context.Context, request generated.GupshupTemplatesListRequestObject) (generated.GupshupTemplatesListResponseObject, error) {
	if _, err := s.appFor(ctx, request.AppId); err != nil {
		return appError(err, func(status int, message string) generated.GupshupTemplatesListResponseObject {
			return generated.GupshupTemplatesListdefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	templates, err := loadTemplates(ctx, s.db)
	if err != nil {
		return nil, err
	}
	filtered := make([]generated.Template, 0, len(templates))
	for _, template := range templates {
		if request.Params.TemplateStatus != nil && *request.Params.TemplateStatus != "" && template.Status != *request.Params.TemplateStatus {
			continue
		}
		filtered = append(filtered, templateAPI(request.AppId, template))
	}
	pageNo := 0
	if request.Params.PageNo != nil {
		pageNo = *request.Params.PageNo
	}
	pageSize := 100
	if request.Params.PageSize != nil {
		pageSize = *request.Params.PageSize
	}
	start := pageNo * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[start:end]
	if page == nil {
		page = []generated.Template{}
	}
	return generated.GupshupTemplatesList200JSONResponse{Status: "success", Templates: page}, nil
}

func (s *server) GupshupTemplatesGet(ctx context.Context, request generated.GupshupTemplatesGetRequestObject) (generated.GupshupTemplatesGetResponseObject, error) {
	if _, err := s.appFor(ctx, request.AppId); err != nil {
		return appError(err, func(status int, message string) generated.GupshupTemplatesGetResponseObject {
			return generated.GupshupTemplatesGetdefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	template, err := getTemplate(ctx, s.db, request.TemplateId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GupshupTemplatesGetdefaultJSONResponse{Body: apiError("Invalid template id provided."), StatusCode: http.StatusBadRequest}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GupshupTemplatesGet200JSONResponse{Status: "success", Template: templateAPI(request.AppId, template)}, nil
}

func (s *server) GupshupMessagesList(ctx context.Context, request generated.GupshupMessagesListRequestObject) (generated.GupshupMessagesListResponseObject, error) {
	messages, err := loadMessagesByTime(ctx, s.db)
	if err != nil {
		return nil, err
	}
	filtered := []generated.Message{}
	for _, message := range messages {
		if request.Params.Destination != nil && *request.Params.Destination != "" && !phonesEqual(message.Destination, *request.Params.Destination) {
			continue
		}
		if request.Params.Status != nil && *request.Params.Status != "" && message.Status != *request.Params.Status {
			continue
		}
		if request.Params.Direction != nil && *request.Params.Direction != "" && message.Direction != string(*request.Params.Direction) {
			continue
		}
		filtered = append(filtered, messageAPI(message))
	}
	return generated.GupshupMessagesList200JSONResponse{Status: "success", Messages: filtered}, nil
}

func (s *server) GupshupMessagesGet(ctx context.Context, request generated.GupshupMessagesGetRequestObject) (generated.GupshupMessagesGetResponseObject, error) {
	message, err := getMessage(ctx, s.db, request.MessageId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GupshupMessagesGetdefaultJSONResponse{Body: apiError("Message not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GupshupMessagesGet200JSONResponse{Status: "success", Message: messageAPI(message)}, nil
}

func (s *server) GupshupMessagesSend(ctx context.Context, request generated.GupshupMessagesSendRequestObject) (generated.GupshupMessagesSendResponseObject, error) {
	if request.Body == nil {
		return sendError(http.StatusBadRequest, "Request body is required"), nil
	}
	body := request.Body
	if body.Channel != nil && *body.Channel != "" && *body.Channel != "whatsapp" {
		return sendError(http.StatusBadRequest, "Invalid channel"), nil
	}
	app, err := loadApp(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if body.SrcName != app.Name || !phonesEqual(body.Source, app.Phone) {
		return sendError(http.StatusBadRequest, "Invalid App Details"), nil
	}
	if !validPhone(body.Destination) {
		return sendError(http.StatusBadRequest, "Invalid Destination"), nil
	}
	text, contextID, err := parseSessionMessage(body.Message)
	if err != nil {
		return sendError(http.StatusBadRequest, err.Error()), nil
	}
	message, err := s.persistOutbound(ctx, strings.TrimSpace(body.Source), strings.TrimSpace(body.Destination), "text", text, contextID)
	if err != nil {
		return nil, err
	}
	return generated.GupshupMessagesSend200JSONResponse{Status: "submitted", MessageId: message.MessageID}, nil
}

func (s *server) GupshupMessagesSendTemplate(ctx context.Context, request generated.GupshupMessagesSendTemplateRequestObject) (generated.GupshupMessagesSendTemplateResponseObject, error) {
	if request.Body == nil {
		return templateSendError(http.StatusBadRequest, "Request body is required"), nil
	}
	body := request.Body
	if body.Channel != nil && *body.Channel != "" && *body.Channel != "whatsapp" {
		return templateSendError(http.StatusBadRequest, "Invalid channel"), nil
	}
	app, err := loadApp(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if body.SrcName != nil && *body.SrcName != "" && *body.SrcName != app.Name {
		return templateSendError(http.StatusBadRequest, "Invalid App Details"), nil
	}
	if !phonesEqual(body.Source, app.Phone) {
		return templateSendError(http.StatusBadRequest, "Invalid App Details"), nil
	}
	if !validPhone(body.Destination) {
		return templateSendError(http.StatusBadRequest, "Invalid Destination"), nil
	}
	templateID, params, err := parseTemplate(body.Template)
	if err != nil {
		return templateSendError(http.StatusBadRequest, err.Error()), nil
	}
	if body.Message != nil && strings.TrimSpace(*body.Message) != "" && !json.Valid([]byte(*body.Message)) {
		return templateSendError(http.StatusBadRequest, "Invalid message"), nil
	}
	template, err := getTemplate(ctx, s.db, templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return templateSendError(http.StatusBadRequest, "Invalid template id provided."), nil
	}
	if err != nil {
		return nil, err
	}
	text, err := renderTemplate(template.Data, params)
	if err != nil {
		return templateSendError(http.StatusBadRequest, err.Error()), nil
	}
	message, err := s.persistOutbound(ctx, strings.TrimSpace(body.Source), strings.TrimSpace(body.Destination), "template", text, "")
	if err != nil {
		return nil, err
	}
	return generated.GupshupMessagesSendTemplate200JSONResponse{Status: "submitted", MessageId: message.MessageID}, nil
}

func (s *server) GupshupMessagesMarkRead(ctx context.Context, request generated.GupshupMessagesMarkReadRequestObject) (generated.GupshupMessagesMarkReadResponseObject, error) {
	if _, err := s.appFor(ctx, request.AppId); err != nil {
		return appError(err, func(status int, message string) generated.GupshupMessagesMarkReadResponseObject {
			return generated.GupshupMessagesMarkReaddefaultJSONResponse{Body: apiError(message), StatusCode: status}
		})
	}
	message, err := getMessage(ctx, s.db, request.MsgId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GupshupMessagesMarkReaddefaultJSONResponse{Body: apiError("Message not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if message.Direction != "inbound" {
		return generated.GupshupMessagesMarkReaddefaultJSONResponse{Body: apiError("Only inbound messages can be marked as read"), StatusCode: http.StatusBadRequest}, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET read=1 WHERE message_id=?`, message.MessageID); err != nil {
		return nil, err
	}
	return generated.GupshupMessagesMarkRead202Response{}, nil
}

func (s *server) persistOutbound(ctx context.Context, source, destination, kind, text, contextID string) (fixtureMessage, error) {
	id, err := s.ids.Next(ctx, "gupshup.message")
	if err != nil {
		return fixtureMessage{}, fmt.Errorf("gupshup: allocate message ID: %w", err)
	}
	position, err := nextMessagePosition(ctx, s.db)
	if err != nil {
		return fixtureMessage{}, err
	}
	message := fixtureMessage{
		MessageID: id, Direction: "outbound", Source: source, Destination: destination,
		Type: kind, Text: text, Status: "submitted", SenderName: "", ContextGsID: contextID,
		Read: false, Timestamp: s.clock.Now().UnixMilli(),
	}
	if err := insertMessage(ctx, s.db, message, position); err != nil {
		return fixtureMessage{}, err
	}
	return message, nil
}

func (s *server) appFor(ctx context.Context, appID string) (fixtureApp, error) {
	app, err := loadApp(ctx, s.db)
	if err != nil {
		return fixtureApp{}, err
	}
	if app.ID != appID {
		return fixtureApp{}, errWrongApp
	}
	return app, nil
}

func appError[T any](err error, respond func(int, string) T) (T, error) {
	if errors.Is(err, errWrongApp) {
		return respond(http.StatusBadRequest, "Invalid app id provided"), nil
	}
	var zero T
	return zero, err
}

func sendError(status int, message string) generated.GupshupMessagesSenddefaultJSONResponse {
	return generated.GupshupMessagesSenddefaultJSONResponse{Body: apiError(message), StatusCode: status}
}

func templateSendError(status int, message string) generated.GupshupMessagesSendTemplatedefaultJSONResponse {
	return generated.GupshupMessagesSendTemplatedefaultJSONResponse{Body: apiError(message), StatusCode: status}
}

func apiError(message string) generated.Error {
	return generated.Error{Status: generated.ErrorStatusError, Message: message}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(message))
}

func messageAPI(message fixtureMessage) generated.Message {
	out := generated.Message{
		MessageId: message.MessageID, Direction: message.Direction, Source: message.Source,
		Destination: message.Destination, Type: message.Type, Text: message.Text,
		Status: message.Status, Read: message.Read, Timestamp: message.Timestamp,
	}
	if message.SenderName != "" {
		out.SenderName = &message.SenderName
	}
	if message.ContextGsID != "" {
		out.ContextGsId = &message.ContextGsID
	}
	return out
}

func templateAPI(appID string, template fixtureTemplate) generated.Template {
	return generated.Template{
		Id: template.ID, AppId: appID, ElementName: template.ElementName, Category: template.Category,
		Status: template.Status, LanguageCode: template.LanguageCode, TemplateType: template.TemplateType, Data: template.Data,
	}
}

func businessAPI(business fixtureBusiness) generated.Business {
	return generated.Business{
		Name: business.Name, AddressLine1: business.AddressLine1, AddressLine2: business.AddressLine2,
		City: business.City, State: business.State, Country: business.Country, PinCode: business.PinCode,
		ContactName: business.ContactName, ContactNumber: business.ContactNumber, Email: business.Email,
		Website: business.Website, Vertical: business.Vertical, EmailVerified: business.EmailVerified, TncAccepted: business.TncAccepted,
	}
}

func profileAPI(profile fixtureProfile) generated.Profile {
	return generated.Profile{
		AddressLine1: profile.AddressLine1, AddressLine2: profile.AddressLine2, City: profile.City,
		Country: profile.Country, Desc: profile.Desc, PinCode: profile.PinCode, ProfileEmail: profile.ProfileEmail,
		State: profile.State, Vertical: profile.Vertical, Website1: profile.Website1, Website2: profile.Website2,
	}
}

func getTemplate(ctx context.Context, query sqlQuery, id string) (fixtureTemplate, error) {
	var template fixtureTemplate
	err := query.QueryRowContext(ctx, `SELECT id, element_name, category, status, language_code, template_type, data FROM templates WHERE id=?`, id).Scan(
		&template.ID, &template.ElementName, &template.Category, &template.Status, &template.LanguageCode, &template.TemplateType, &template.Data)
	return template, err
}

type sessionPayload struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	PreviewURL *bool  `json:"previewUrl"`
	Context    *struct {
		MsgID string `json:"msgId"`
	} `json:"context"`
}

func parseSessionMessage(raw string) (string, string, error) {
	var payload sessionPayload
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return "", "", errors.New("Invalid message")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", "", errors.New("Invalid message")
	}
	if payload.Type != "" && payload.Type != "text" {
		return "", "", errors.New("unsupported message type")
	}
	if strings.TrimSpace(payload.Text) == "" {
		return "", "", errors.New("text is required")
	}
	contextID := ""
	if payload.Context != nil {
		contextID = payload.Context.MsgID
	}
	return payload.Text, contextID, nil
}

type templatePayload struct {
	ID     string   `json:"id"`
	Params []string `json:"params"`
}

func parseTemplate(raw string) (string, []string, error) {
	var payload templatePayload
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return "", nil, errors.New("Invalid template")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", nil, errors.New("Invalid template")
	}
	if payload.ID == "" {
		return "", nil, errors.New("Invalid template id provided.")
	}
	if payload.Params == nil {
		payload.Params = []string{}
	}
	return payload.ID, payload.Params, nil
}

func renderTemplate(data string, params []string) (string, error) {
	matches := templatePlaceholder.FindAllString(data, -1)
	if len(matches) != len(params) {
		return "", fmt.Errorf("template expects %d params", len(matches))
	}
	rendered := data
	for i, param := range params {
		rendered = strings.ReplaceAll(rendered, "{{"+strconv.Itoa(i+1)+"}}", param)
	}
	if strings.TrimSpace(rendered) == "" {
		return "", errors.New("text is required")
	}
	return rendered, nil
}

func normalizePhone(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "+")
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func phonesEqual(left, right string) bool {
	normalized := normalizePhone(left)
	return normalized != "" && normalized == normalizePhone(right)
}

func validPhone(value string) bool {
	normalized := normalizePhone(value)
	if len(normalized) < 8 || len(normalized) > 15 {
		return false
	}
	for _, r := range normalized {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
