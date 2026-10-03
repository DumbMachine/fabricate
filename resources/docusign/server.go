package docusign

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/docusign/generated"
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
		return nil, fmt.Errorf("docusign: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("docusign: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("docusign: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("docusign: load OpenAPI: %w", err)
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
			code := "INVALID_REQUEST_PARAMETER"
			message := err.Error()
			if status == http.StatusUnauthorized {
				code = "USER_AUTHENTICATION_FAILED"
				message = "The Authorization header is missing or the bearer token is invalid."
			}
			writeError(w, status, code, message)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) AccountsGetAccount(ctx context.Context, request generated.AccountsGetAccountRequestObject) (generated.AccountsGetAccountResponseObject, error) {
	account, ok, err := s.account(ctx, request.AccountId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.AccountsGetAccountdefaultJSONResponse{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	return generated.AccountsGetAccount200JSONResponse{
		AccountIdGuid: account.AccountId, AccountName: account.AccountName, ExternalAccountId: account.ExternalAccountId,
		CurrencyCode: account.CurrencyCode, PlanName: account.PlanName, CreatedDate: account.CreatedDate,
	}, nil
}

func (s *server) UsersGetUsers(ctx context.Context, request generated.UsersGetUsersRequestObject) (generated.UsersGetUsersResponseObject, error) {
	if _, ok, err := s.account(ctx, request.AccountId); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return generated.UsersGetUsersdefaultJSONResponse{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	users, err := loadUsers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if request.Params.Email != nil && *request.Params.Email != "" {
		filtered := []fixtureUser{}
		for _, user := range users {
			if strings.EqualFold(user.Email, *request.Params.Email) {
				filtered = append(filtered, user)
			}
		}
		users = filtered
	}
	start, count := pageArgs(request.Params.StartPosition, request.Params.Count)
	from, to, info := paginate(userListURI(request.AccountId), start, count, len(users))
	page := make([]generated.UserInformation, 0, to-from)
	for _, user := range users[from:to] {
		page = append(page, userInformation(request.AccountId, user))
	}
	return generated.UsersGetUsers200JSONResponse{
		ResultSetSize: info.result, StartPosition: info.start, EndPosition: info.end, TotalSetSize: info.total,
		NextUri: info.next, PreviousUri: info.prev, Users: page,
	}, nil
}

func (s *server) UserGetUser(ctx context.Context, request generated.UserGetUserRequestObject) (generated.UserGetUserResponseObject, error) {
	if _, ok, err := s.account(ctx, request.AccountId); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return generated.UserGetUserdefaultJSONResponse{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	user, err := loadUser(ctx, s.db, request.UserId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UserGetUserdefaultJSONResponse{Body: errorDetails("USER_DOES_NOT_EXIST", "The user does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.UserGetUser200JSONResponse(userInformation(request.AccountId, user)), nil
}

func (s *server) EnvelopesGetEnvelopes(ctx context.Context, request generated.EnvelopesGetEnvelopesRequestObject) (generated.EnvelopesGetEnvelopesResponseObject, error) {
	if _, ok, err := s.account(ctx, request.AccountId); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return generated.EnvelopesGetEnvelopesdefaultJSONResponse{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	envelopes, err := loadEnvelopes(ctx, s.db)
	if err != nil {
		return nil, err
	}
	filtered, err := filterEnvelopes(envelopes, request.Params)
	if err != nil {
		return generated.EnvelopesGetEnvelopesdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_PARAMETER", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	start, count := pageArgs(request.Params.StartPosition, request.Params.Count)
	from, to, info := paginate(envelopeListURI(request.AccountId), start, count, len(filtered))
	page := make([]generated.Envelope, 0, to-from)
	for _, envelope := range filtered[from:to] {
		api, err := s.toAPIEnvelope(ctx, request.AccountId, envelope)
		if err != nil {
			return nil, err
		}
		page = append(page, api)
	}
	return generated.EnvelopesGetEnvelopes200JSONResponse{
		ResultSetSize: info.result, StartPosition: info.start, EndPosition: info.end, TotalSetSize: info.total,
		NextUri: info.next, PreviousUri: info.prev, Envelopes: page,
	}, nil
}

func (s *server) EnvelopesPostEnvelopes(ctx context.Context, request generated.EnvelopesPostEnvelopesRequestObject) (generated.EnvelopesPostEnvelopesResponseObject, error) {
	if _, ok, err := s.account(ctx, request.AccountId); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return generated.EnvelopesPostEnvelopesdefaultJSONResponse{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	if request.Body == nil {
		return generated.EnvelopesPostEnvelopesdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "Request body is required."), StatusCode: http.StatusBadRequest}, nil
	}
	envelope, err := s.envelopeFromDefinition(ctx, *request.Body)
	if err != nil {
		return generated.EnvelopesPostEnvelopesdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	if err := insertEnvelope(ctx, s.db, envelope); err != nil {
		return nil, err
	}
	return generated.EnvelopesPostEnvelopes201JSONResponse{
		EnvelopeId: envelope.EnvelopeId, Status: envelope.Status, StatusDateTime: envelope.StatusChangedDateTime,
		Uri: envelopeURI(request.AccountId, envelope.EnvelopeId),
	}, nil
}

func (s *server) EnvelopesGetEnvelope(ctx context.Context, request generated.EnvelopesGetEnvelopeRequestObject) (generated.EnvelopesGetEnvelopeResponseObject, error) {
	envelope, response, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if response != nil || err != nil {
		if response != nil {
			return generated.EnvelopesGetEnvelopedefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, err
		}
		return nil, err
	}
	api, err := s.toAPIEnvelope(ctx, request.AccountId, envelope)
	if err != nil {
		return nil, err
	}
	return generated.EnvelopesGetEnvelope200JSONResponse(api), nil
}

func (s *server) EnvelopesPutEnvelope(ctx context.Context, request generated.EnvelopesPutEnvelopeRequestObject) (generated.EnvelopesPutEnvelopeResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "Request body is required."), StatusCode: http.StatusBadRequest}, nil
	}
	now := formatAPITime(s.clock.Now())
	body := request.Body
	if body.EmailSubject != nil || body.EmailBlurb != nil {
		if envelope.Status == "completed" || envelope.Status == "voided" {
			return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("ENVELOPE_CANNOT_CORRECT_INVALID_STATE", "The envelope cannot be modified in its current state."), StatusCode: http.StatusBadRequest}, nil
		}
		if body.EmailSubject != nil {
			envelope.EmailSubject = strings.TrimSpace(*body.EmailSubject)
		}
		if body.EmailBlurb != nil {
			envelope.EmailBlurb = *body.EmailBlurb
		}
	}
	if body.Status != nil {
		switch *body.Status {
		case generated.EnvelopeUpdateStatusSent:
			if envelope.Status != "created" && envelope.Status != "sent" && envelope.Status != "delivered" {
				return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("ENVELOPE_CANNOT_CORRECT_INVALID_STATE", "The envelope cannot be sent in its current state."), StatusCode: http.StatusBadRequest}, nil
			}
			if strings.TrimSpace(envelope.EmailSubject) == "" {
				return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "emailSubject is required to send an envelope."), StatusCode: http.StatusBadRequest}, nil
			}
			if len(envelope.Documents) == 0 || len(envelope.Signers) == 0 {
				return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "A sent envelope requires at least one document and one signer."), StatusCode: http.StatusBadRequest}, nil
			}
			if envelope.Status == "created" {
				envelope.Status = "sent"
				if envelope.SentDateTime == "" {
					envelope.SentDateTime = now
				}
				envelope.StatusChangedDateTime = now
				markCurrentSignersSent(envelope.Signers, now)
			}
		case generated.EnvelopeUpdateStatusVoided:
			if envelope.Status != "sent" && envelope.Status != "delivered" && envelope.Status != "signed" {
				return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("ENVELOPE_CANNOT_VOID_INVALID_STATE", "The envelope cannot be voided in its current state."), StatusCode: http.StatusBadRequest}, nil
			}
			reason := strings.TrimSpace(value(body.VoidedReason))
			if reason == "" {
				return generated.EnvelopesPutEnvelopedefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "voidedReason is required."), StatusCode: http.StatusBadRequest}, nil
			}
			if len(reason) > 200 {
				reason = reason[:200]
			}
			envelope.Status = "voided"
			envelope.VoidedReason = reason
			envelope.VoidedDateTime = now
			envelope.StatusChangedDateTime = now
		}
	}
	if err := updateEnvelope(ctx, s.db, envelope); err != nil {
		return nil, err
	}
	if err := updateSigners(ctx, s.db, envelope.EnvelopeId, envelope.Signers); err != nil {
		return nil, err
	}
	purge := "unpurged"
	return generated.EnvelopesPutEnvelope200JSONResponse{EnvelopeId: envelope.EnvelopeId, PurgeState: &purge}, nil
}

func (s *server) RecipientsGetRecipients(ctx context.Context, request generated.RecipientsGetRecipientsRequestObject) (generated.RecipientsGetRecipientsResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.RecipientsGetRecipientsdefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	api, err := s.toAPIEnvelope(ctx, request.AccountId, envelope)
	if err != nil {
		return nil, err
	}
	return generated.RecipientsGetRecipients200JSONResponse(api.Recipients), nil
}

func (s *server) RecipientsPutRecipients(ctx context.Context, request generated.RecipientsPutRecipientsRequestObject) (generated.RecipientsPutRecipientsResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	if request.Body == nil || request.Body.Signers == nil || len(*request.Body.Signers) == 0 {
		return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "signers is required."), StatusCode: http.StatusBadRequest}, nil
	}
	if envelope.Status == "completed" || envelope.Status == "voided" {
		return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("ENVELOPE_CANNOT_CORRECT_INVALID_STATE", "Recipients cannot be modified in the envelope's current state."), StatusCode: http.StatusBadRequest}, nil
	}
	byID := map[string]int{}
	for i, signer := range envelope.Signers {
		byID[signer.RecipientId] = i
	}
	results := make([]generated.RecipientUpdateResult, 0, len(*request.Body.Signers))
	for _, update := range *request.Body.Signers {
		recipientID := strings.TrimSpace(value(update.RecipientId))
		index, ok := byID[recipientID]
		if recipientID == "" || !ok {
			return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("UNKNOWN_ENVELOPE_RECIPIENT", "The recipient does not exist on this envelope."), StatusCode: http.StatusBadRequest}, nil
		}
		name := strings.TrimSpace(update.Name)
		email := strings.TrimSpace(update.Email)
		if name == "" || email == "" {
			return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "Signer name and email are required."), StatusCode: http.StatusBadRequest}, nil
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "Signer email is invalid."), StatusCode: http.StatusBadRequest}, nil
		}
		envelope.Signers[index].Name = name
		envelope.Signers[index].Email = email
		if update.RoutingOrder != nil && strings.TrimSpace(*update.RoutingOrder) != "" {
			if _, err := strconv.Atoi(strings.TrimSpace(*update.RoutingOrder)); err != nil {
				return generated.RecipientsPutRecipientsdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "routingOrder must be an integer."), StatusCode: http.StatusBadRequest}, nil
			}
			envelope.Signers[index].RoutingOrder = strings.TrimSpace(*update.RoutingOrder)
		}
		results = append(results, generated.RecipientUpdateResult{RecipientId: recipientID})
	}
	if err := updateSigners(ctx, s.db, envelope.EnvelopeId, envelope.Signers); err != nil {
		return nil, err
	}
	return generated.RecipientsPutRecipients200JSONResponse{RecipientUpdateResults: results}, nil
}

func (s *server) DocumentsGetDocuments(ctx context.Context, request generated.DocumentsGetDocumentsRequestObject) (generated.DocumentsGetDocumentsResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.DocumentsGetDocumentsdefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	api, err := s.toAPIEnvelope(ctx, request.AccountId, envelope)
	if err != nil {
		return nil, err
	}
	return generated.DocumentsGetDocuments200JSONResponse{EnvelopeId: envelope.EnvelopeId, EnvelopeDocuments: api.EnvelopeDocuments}, nil
}

func (s *server) DocumentsGetDocument(ctx context.Context, request generated.DocumentsGetDocumentRequestObject) (generated.DocumentsGetDocumentResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.DocumentsGetDocumentdefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	var payload []byte
	if request.DocumentId == "combined" {
		var buf bytes.Buffer
		for i, document := range envelope.Documents {
			if i > 0 {
				buf.WriteByte('\n')
			}
			raw, err := documentBytes(document)
			if err != nil {
				return nil, err
			}
			buf.Write(raw)
		}
		payload = buf.Bytes()
	} else {
		found := false
		for _, document := range envelope.Documents {
			if document.DocumentId != request.DocumentId {
				continue
			}
			payload, err = documentBytes(document)
			if err != nil {
				return nil, err
			}
			found = true
			break
		}
		if !found {
			return generated.DocumentsGetDocumentdefaultJSONResponse{Body: errorDetails("DOCUMENT_DOES_NOT_EXIST", "The document specified either does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
		}
	}
	return generated.DocumentsGetDocument200ApplicationpdfResponse{Body: bytes.NewReader(payload), ContentLength: int64(len(payload))}, nil
}

func (s *server) ViewsPostEnvelopeRecipientView(ctx context.Context, request generated.ViewsPostEnvelopeRecipientViewRequestObject) (generated.ViewsPostEnvelopeRecipientViewResponseObject, error) {
	envelope, missing, err := s.envelopeFor(ctx, request.AccountId, request.EnvelopeId)
	if missing != nil || err != nil {
		if missing != nil {
			return generated.ViewsPostEnvelopeRecipientViewdefaultJSONResponse{Body: missing.Body, StatusCode: missing.StatusCode}, err
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.ViewsPostEnvelopeRecipientViewdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "Request body is required."), StatusCode: http.StatusBadRequest}, nil
	}
	if strings.TrimSpace(request.Body.ReturnUrl) == "" || strings.TrimSpace(request.Body.AuthenticationMethod) == "" {
		return generated.ViewsPostEnvelopeRecipientViewdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "returnUrl and authenticationMethod are required."), StatusCode: http.StatusBadRequest}, nil
	}
	email := strings.TrimSpace(value(request.Body.Email))
	recipientID := strings.TrimSpace(value(request.Body.RecipientId))
	if email == "" && recipientID == "" {
		return generated.ViewsPostEnvelopeRecipientViewdefaultJSONResponse{Body: errorDetails("INVALID_REQUEST_BODY", "email or recipientId is required."), StatusCode: http.StatusBadRequest}, nil
	}
	var match *fixtureSigner
	for i := range envelope.Signers {
		signer := &envelope.Signers[i]
		if (recipientID == "" || signer.RecipientId == recipientID) && (email == "" || strings.EqualFold(signer.Email, email)) {
			match = signer
			break
		}
	}
	if match == nil {
		return generated.ViewsPostEnvelopeRecipientViewdefaultJSONResponse{Body: errorDetails("UNKNOWN_ENVELOPE_RECIPIENT", "The recipient does not exist on this envelope."), StatusCode: http.StatusBadRequest}, nil
	}
	url := fmt.Sprintf("https://demo.docusign.net/Signing/startinsession.aspx?envelopeId=%s&recipientId=%s", envelope.EnvelopeId, match.RecipientId)
	return generated.ViewsPostEnvelopeRecipientView201JSONResponse{Url: url}, nil
}

type apiFailure struct {
	Body       generated.ErrorDetails
	StatusCode int
}

func (s *server) account(ctx context.Context, accountID string) (fixtureAccount, bool, error) {
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return fixtureAccount{}, false, err
	}
	if account.AccountId != accountID {
		return fixtureAccount{}, false, nil
	}
	return account, true, nil
}

func (s *server) envelopeFor(ctx context.Context, accountID, envelopeID string) (fixtureEnvelope, *apiFailure, error) {
	if _, ok, err := s.account(ctx, accountID); err != nil || !ok {
		if err != nil {
			return fixtureEnvelope{}, nil, err
		}
		return fixtureEnvelope{}, &apiFailure{Body: errorDetails("ACCOUNT_DOES_NOT_EXIST", "The account does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	envelope, err := loadEnvelope(ctx, s.db, envelopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return fixtureEnvelope{}, &apiFailure{Body: errorDetails("ENVELOPE_DOES_NOT_EXIST", "The envelope specified either does not exist or you have no rights to it."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return fixtureEnvelope{}, nil, err
	}
	return envelope, nil, nil
}

func (s *server) envelopeFromDefinition(ctx context.Context, body generated.EnvelopeDefinition) (fixtureEnvelope, error) {
	status := "created"
	if body.Status != nil {
		status = string(*body.Status)
	}
	subject := strings.TrimSpace(value(body.EmailSubject))
	blurb := value(body.EmailBlurb)
	documents, err := documentsFromRequest(body.Documents)
	if err != nil {
		return fixtureEnvelope{}, err
	}
	signers, err := signersFromRequest(body.Recipients)
	if err != nil {
		return fixtureEnvelope{}, err
	}
	if status == "sent" {
		if subject == "" {
			return fixtureEnvelope{}, fmt.Errorf("emailSubject is required to send an envelope")
		}
		if len(documents) == 0 || len(signers) == 0 {
			return fixtureEnvelope{}, fmt.Errorf("a sent envelope requires at least one document and one signer")
		}
	}
	now := formatAPITime(s.clock.Now())
	id, err := s.ids.Next(ctx, "docusign.envelope")
	if err != nil {
		return fixtureEnvelope{}, fmt.Errorf("docusign: allocate envelope ID: %w", err)
	}
	sender, err := defaultSenderID(ctx, s.db)
	if err != nil {
		return fixtureEnvelope{}, err
	}
	for i := range signers {
		if signers[i].Status == "" {
			signers[i].Status = "created"
		}
	}
	if status == "sent" {
		markCurrentSignersSent(signers, now)
	}
	sent := ""
	if status == "sent" {
		sent = now
	}
	return fixtureEnvelope{
		EnvelopeId: id, Status: status, EmailSubject: subject, EmailBlurb: blurb,
		CreatedDateTime: now, SentDateTime: sent, StatusChangedDateTime: now, SenderUserId: sender,
		Documents: documents, Signers: signers,
	}, nil
}

func documentsFromRequest(values *[]generated.Document) ([]fixtureDocument, error) {
	if values == nil {
		return []fixtureDocument{}, nil
	}
	documents := make([]fixtureDocument, 0, len(*values))
	seen := map[string]struct{}{}
	for i, document := range *values {
		name := strings.TrimSpace(document.Name)
		if name == "" {
			return nil, fmt.Errorf("documents[%d].name is required", i)
		}
		id := strings.TrimSpace(value(document.DocumentId))
		if id == "" {
			id = strconv.Itoa(i + 1)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate documentId %s", id)
		}
		seen[id] = struct{}{}
		order := strings.TrimSpace(value(document.Order))
		if order == "" {
			order = strconv.Itoa(i + 1)
		}
		extension := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value(document.FileExtension))), ".")
		if extension == "" {
			extension = fileExtension(name)
		}
		encoded := value(document.DocumentBase64)
		if encoded != "" {
			if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
				return nil, fmt.Errorf("documents[%d].documentBase64 is not valid base64", i)
			}
		}
		documents = append(documents, fixtureDocument{
			DocumentId: id, Name: name, FileExtension: extension, Order: order, DocumentBase64: encoded,
		})
	}
	return documents, nil
}

func signersFromRequest(recipients *generated.Recipients) ([]fixtureSigner, error) {
	if recipients == nil || recipients.Signers == nil {
		return []fixtureSigner{}, nil
	}
	signers := make([]fixtureSigner, 0, len(*recipients.Signers))
	seen := map[string]struct{}{}
	for i, signer := range *recipients.Signers {
		name := strings.TrimSpace(signer.Name)
		email := strings.TrimSpace(signer.Email)
		if name == "" || email == "" {
			return nil, fmt.Errorf("signers[%d] requires name and email", i)
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return nil, fmt.Errorf("signers[%d].email: %w", i, err)
		}
		id := strings.TrimSpace(value(signer.RecipientId))
		if id == "" {
			id = strconv.Itoa(i + 1)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate recipientId %s", id)
		}
		seen[id] = struct{}{}
		order := strings.TrimSpace(value(signer.RoutingOrder))
		if order == "" {
			order = "1"
		}
		if _, err := strconv.Atoi(order); err != nil {
			return nil, fmt.Errorf("signers[%d].routingOrder must be an integer", i)
		}
		signers = append(signers, fixtureSigner{
			RecipientId: id, Name: name, Email: email, RoutingOrder: order, Status: "created",
		})
	}
	return signers, nil
}

func markCurrentSignersSent(signers []fixtureSigner, now string) {
	if len(signers) == 0 {
		return
	}
	minimum := routingValue(signers[0].RoutingOrder)
	for _, signer := range signers[1:] {
		if order := routingValue(signer.RoutingOrder); order < minimum {
			minimum = order
		}
	}
	for i := range signers {
		if signers[i].Status == "completed" || signers[i].Status == "declined" || signers[i].Status == "signed" || signers[i].Status == "delivered" {
			continue
		}
		if routingValue(signers[i].RoutingOrder) == minimum {
			signers[i].Status = "sent"
			if signers[i].SentDateTime == "" {
				signers[i].SentDateTime = now
			}
			continue
		}
		if signers[i].Status == "" {
			signers[i].Status = "created"
		}
	}
}

func (s *server) toAPIEnvelope(ctx context.Context, accountID string, envelope fixtureEnvelope) (generated.Envelope, error) {
	signers := make([]generated.Signer, 0, len(envelope.Signers))
	for _, signer := range envelope.Signers {
		signers = append(signers, generated.Signer{
			RecipientId: strPtr(signer.RecipientId), Name: signer.Name, Email: signer.Email,
			RoutingOrder: strPtr(signer.RoutingOrder), Status: strPtr(signer.Status),
			SentDateTime: strPtr(signer.SentDateTime), DeliveredDateTime: strPtr(signer.DeliveredDateTime),
			SignedDateTime: strPtr(signer.SignedDateTime),
		})
	}
	count := strconv.Itoa(len(signers))
	order := currentRoutingOrder(envelope)
	documents := make([]generated.EnvelopeDocument, 0, len(envelope.Documents))
	for _, document := range envelope.Documents {
		documents = append(documents, generated.EnvelopeDocument{
			DocumentId: document.DocumentId, Name: document.Name, FileExtension: document.FileExtension,
			Order: document.Order, Type: "content", Uri: documentURI(accountID, envelope.EnvelopeId, document.DocumentId),
		})
	}
	api := generated.Envelope{
		EnvelopeId: envelope.EnvelopeId, Status: envelope.Status, EmailSubject: envelope.EmailSubject, EmailBlurb: envelope.EmailBlurb,
		CreatedDateTime: envelope.CreatedDateTime, StatusChangedDateTime: envelope.StatusChangedDateTime,
		SentDateTime: strPtr(envelope.SentDateTime), DeliveredDateTime: strPtr(envelope.DeliveredDateTime),
		CompletedDateTime: strPtr(envelope.CompletedDateTime), VoidedDateTime: strPtr(envelope.VoidedDateTime),
		VoidedReason:  strPtr(envelope.VoidedReason),
		RecipientsUri: recipientsURI(accountID, envelope.EnvelopeId), DocumentsUri: documentsURI(accountID, envelope.EnvelopeId),
		EnvelopeUri:       envelopeURI(accountID, envelope.EnvelopeId),
		Recipients:        generated.Recipients{Signers: &signers, RecipientCount: &count, CurrentRoutingOrder: &order},
		EnvelopeDocuments: documents,
	}
	if envelope.SenderUserId != "" {
		user, err := loadUser(ctx, s.db, envelope.SenderUserId)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return generated.Envelope{}, err
		}
		if err == nil {
			api.Sender = &generated.UserInfo{UserId: user.UserId, UserName: user.UserName, Email: user.Email, AccountId: &accountID}
		}
	}
	return api, nil
}

func filterEnvelopes(envelopes []fixtureEnvelope, params generated.EnvelopesGetEnvelopesParams) ([]fixtureEnvelope, error) {
	statusSet := map[string]struct{}{}
	for _, status := range splitCSV(value(params.Status)) {
		switch status {
		case "created", "sent", "delivered", "signed", "completed", "declined", "voided":
			statusSet[status] = struct{}{}
		default:
			return nil, fmt.Errorf("invalid status %q", status)
		}
	}
	idSet := map[string]struct{}{}
	for _, id := range splitCSV(value(params.EnvelopeIds)) {
		idSet[id] = struct{}{}
	}
	var from, to time.Time
	var hasFrom, hasTo bool
	if params.FromDate != nil && strings.TrimSpace(*params.FromDate) != "" {
		parsed, err := parseAPITime(strings.TrimSpace(*params.FromDate))
		if err != nil {
			return nil, fmt.Errorf("from_date is invalid")
		}
		from, hasFrom = parsed, true
	}
	if params.ToDate != nil && strings.TrimSpace(*params.ToDate) != "" {
		parsed, err := parseAPITime(strings.TrimSpace(*params.ToDate))
		if err != nil {
			return nil, fmt.Errorf("to_date is invalid")
		}
		to, hasTo = parsed, true
	}
	search := strings.ToLower(strings.TrimSpace(value(params.SearchText)))
	filtered := []fixtureEnvelope{}
	for _, envelope := range envelopes {
		if len(statusSet) > 0 {
			if _, ok := statusSet[envelope.Status]; !ok {
				continue
			}
		}
		if len(idSet) > 0 {
			if _, ok := idSet[envelope.EnvelopeId]; !ok {
				continue
			}
		}
		if hasFrom || hasTo {
			changed, err := parseAPITime(envelope.StatusChangedDateTime)
			if err != nil {
				continue
			}
			if hasFrom && changed.Before(from) {
				continue
			}
			if hasTo && changed.After(to) {
				continue
			}
		}
		if search != "" && !envelopeMatches(envelope, search) {
			continue
		}
		filtered = append(filtered, envelope)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].StatusChangedDateTime != filtered[j].StatusChangedDateTime {
			return filtered[i].StatusChangedDateTime > filtered[j].StatusChangedDateTime
		}
		return filtered[i].EnvelopeId < filtered[j].EnvelopeId
	})
	return filtered, nil
}

func envelopeMatches(envelope fixtureEnvelope, search string) bool {
	parts := []string{envelope.EnvelopeId, envelope.EmailSubject, envelope.EmailBlurb, envelope.Status}
	for _, signer := range envelope.Signers {
		parts = append(parts, signer.Name, signer.Email)
	}
	for _, document := range envelope.Documents {
		parts = append(parts, document.Name)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), search)
}

func currentRoutingOrder(envelope fixtureEnvelope) string {
	if len(envelope.Signers) == 0 {
		return "1"
	}
	active := ""
	activeOrder := 0
	last := envelope.Signers[0].RoutingOrder
	lastOrder := routingValue(last)
	for _, signer := range envelope.Signers {
		order := routingValue(signer.RoutingOrder)
		if order >= lastOrder {
			last = signer.RoutingOrder
			lastOrder = order
		}
		if signer.Status == "sent" || signer.Status == "delivered" {
			if active == "" || order < activeOrder {
				active = signer.RoutingOrder
				activeOrder = order
			}
		}
	}
	if active == "" {
		return last
	}
	return active
}

func routingValue(value string) int {
	order, err := strconv.Atoi(value)
	if err != nil {
		return 1
	}
	return order
}

func documentBytes(document fixtureDocument) ([]byte, error) {
	if document.DocumentBase64 == "" {
		return []byte(document.Name + "\n"), nil
	}
	raw, err := base64.StdEncoding.DecodeString(document.DocumentBase64)
	if err != nil {
		return nil, fmt.Errorf("docusign: document %s: %w", document.DocumentId, err)
	}
	return raw, nil
}

func userInformation(accountID string, user fixtureUser) generated.UserInformation {
	return generated.UserInformation{
		UserId: user.UserId, UserName: user.UserName, Email: user.Email, UserStatus: user.UserStatus,
		IsAdmin: user.IsAdmin, UserType: "CompanyUser", Uri: userURI(accountID, user.UserId),
	}
}

func fileExtension(name string) string {
	if dot := strings.LastIndex(name, "."); dot >= 0 && dot < len(name)-1 {
		return strings.ToLower(name[dot+1:])
	}
	return "pdf"
}

func pageArgs(start *int, count *int) (int, int) {
	from := 0
	if start != nil {
		from = *start
	}
	limit := 100
	if count != nil {
		limit = *count
	}
	return from, limit
}

type pageWindow struct {
	result, start, end, total string
	next, prev                *string
}

func paginate(path string, start, count, total int) (int, int, pageWindow) {
	if start > total {
		start = total
	}
	if count < 1 {
		count = 1
	}
	end := start + count
	if end > total {
		end = total
	}
	endPosition := start
	if end > start {
		endPosition = end - 1
	}
	window := pageWindow{
		result: strconv.Itoa(end - start), start: strconv.Itoa(start), end: strconv.Itoa(endPosition), total: strconv.Itoa(total),
	}
	if end < total {
		next := fmt.Sprintf("%s?start_position=%d&count=%d", path, end, count)
		window.next = &next
	}
	if start > 0 {
		prevStart := start - count
		if prevStart < 0 {
			prevStart = 0
		}
		prev := fmt.Sprintf("%s?start_position=%d&count=%d", path, prevStart, count)
		window.prev = &prev
	}
	return start, end, window
}

func accountBase(accountID string) string {
	return "/restapi/v2.1/accounts/" + accountID
}

func envelopeListURI(accountID string) string { return accountBase(accountID) + "/envelopes" }
func envelopeURI(accountID, envelopeID string) string {
	return envelopeListURI(accountID) + "/" + envelopeID
}
func recipientsURI(accountID, envelopeID string) string {
	return envelopeURI(accountID, envelopeID) + "/recipients"
}
func documentsURI(accountID, envelopeID string) string {
	return envelopeURI(accountID, envelopeID) + "/documents"
}
func documentURI(accountID, envelopeID, documentID string) string {
	return documentsURI(accountID, envelopeID) + "/" + documentID
}
func userListURI(accountID string) string { return accountBase(accountID) + "/users" }
func userURI(accountID, userID string) string {
	return userListURI(accountID) + "/" + userID
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func errorDetails(code, message string) generated.ErrorDetails {
	return generated.ErrorDetails{ErrorCode: code, Message: message}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorDetails(code, message))
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}
