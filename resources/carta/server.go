package carta

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/carta/generated"
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
		return nil, fmt.Errorf("carta: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("carta: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("carta: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("carta: load OpenAPI: %w", err)
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
			writeError(w, status, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) V1alpha1IssuersGetIssuer(ctx context.Context, request generated.V1alpha1IssuersGetIssuerRequestObject) (generated.V1alpha1IssuersGetIssuerResponseObject, error) {
	issuer, err := s.loadIssuer(ctx)
	if err != nil {
		return nil, err
	}
	if issuer.Id != request.Id {
		return generated.V1alpha1IssuersGetIssuerdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
	}
	return generated.V1alpha1IssuersGetIssuer200JSONResponse{Issuer: issuer}, nil
}

func (s *server) V1alpha1IssuersListStakeholders(ctx context.Context, request generated.V1alpha1IssuersListStakeholdersRequestObject) (generated.V1alpha1IssuersListStakeholdersResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		return issuerMissingListStakeholders(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, issuer_id, full_name, email, relationship, entity_type, country FROM stakeholders WHERE issuer_id=? ORDER BY position, id`, request.IssuerId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.Stakeholder{}
	for rows.Next() {
		item, err := scanStakeholder(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start, end, next, err := pageWindow(request.Params.PageSize, request.Params.PageToken, len(items), 100)
	if err != nil {
		return generated.V1alpha1IssuersListStakeholdersdefaultJSONResponse{Body: rpcStatus(http.StatusBadRequest, err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	return generated.V1alpha1IssuersListStakeholders200JSONResponse{Stakeholders: items[start:end], NextPageToken: next}, nil
}

func (s *server) V1alpha1IssuersGetStakeholder(ctx context.Context, request generated.V1alpha1IssuersGetStakeholderRequestObject) (generated.V1alpha1IssuersGetStakeholderResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersGetStakeholderdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, issuer_id, full_name, email, relationship, entity_type, country FROM stakeholders WHERE issuer_id=? AND id=?`, request.IssuerId, request.Id)
	item, err := scanStakeholder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.V1alpha1IssuersGetStakeholderdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "stakeholder not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.V1alpha1IssuersGetStakeholder200JSONResponse{Stakeholder: item}, nil
}

func (s *server) V1alpha1IssuersListCertificates(ctx context.Context, request generated.V1alpha1IssuersListCertificatesRequestObject) (generated.V1alpha1IssuersListCertificatesResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersListCertificatesdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, issuer_id, stakeholder_id, share_class_name, security_label, issue_date, quantity, currency, price FROM certificates WHERE issuer_id=? ORDER BY position, id`, request.IssuerId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.Certificate{}
	for rows.Next() {
		item, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start, end, next, err := pageWindow(request.Params.PageSize, request.Params.PageToken, len(items), 100)
	if err != nil {
		return generated.V1alpha1IssuersListCertificatesdefaultJSONResponse{Body: rpcStatus(http.StatusBadRequest, err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	return generated.V1alpha1IssuersListCertificates200JSONResponse{Certificates: items[start:end], NextPageToken: next}, nil
}

func (s *server) V1alpha1IssuersGetCertificate(ctx context.Context, request generated.V1alpha1IssuersGetCertificateRequestObject) (generated.V1alpha1IssuersGetCertificateResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersGetCertificatedefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, issuer_id, stakeholder_id, share_class_name, security_label, issue_date, quantity, currency, price FROM certificates WHERE issuer_id=? AND id=?`, request.IssuerId, request.Id)
	item, err := scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.V1alpha1IssuersGetCertificatedefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "certificate not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.V1alpha1IssuersGetCertificate200JSONResponse{Certificate: item}, nil
}

func (s *server) V1alpha1IssuersListOptionGrants(ctx context.Context, request generated.V1alpha1IssuersListOptionGrantsRequestObject) (generated.V1alpha1IssuersListOptionGrantsResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersListOptionGrantsdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, issuer_id, stakeholder_id, plan_name, security_label, stock_option_type, issue_date, quantity, outstanding_quantity, currency, exercise_price FROM option_grants WHERE issuer_id=? ORDER BY position, id`, request.IssuerId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.OptionGrant{}
	for rows.Next() {
		item, err := scanOptionGrant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start, end, next, err := pageWindow(request.Params.PageSize, request.Params.PageToken, len(items), 50)
	if err != nil {
		return generated.V1alpha1IssuersListOptionGrantsdefaultJSONResponse{Body: rpcStatus(http.StatusBadRequest, err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	return generated.V1alpha1IssuersListOptionGrants200JSONResponse{OptionGrants: items[start:end], NextPageToken: next}, nil
}

func (s *server) V1alpha1IssuersGetOptionGrant(ctx context.Context, request generated.V1alpha1IssuersGetOptionGrantRequestObject) (generated.V1alpha1IssuersGetOptionGrantResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersGetOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, issuer_id, stakeholder_id, plan_name, security_label, stock_option_type, issue_date, quantity, outstanding_quantity, currency, exercise_price FROM option_grants WHERE issuer_id=? AND id=?`, request.IssuerId, request.Id)
	item, err := scanOptionGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.V1alpha1IssuersGetOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "option grant not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.V1alpha1IssuersGetOptionGrant200JSONResponse{OptionGrant: item}, nil
}

func (s *server) V1alpha1IssuersListFairMarketValues(ctx context.Context, request generated.V1alpha1IssuersListFairMarketValuesRequestObject) (generated.V1alpha1IssuersListFairMarketValuesResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersListFairMarketValuesdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, effective_date, expiration_date, valuations FROM fair_market_values ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.FairMarketValue{}
	for rows.Next() {
		var id, effective, expiration, raw string
		if err := rows.Scan(&id, &effective, &expiration, &raw); err != nil {
			return nil, err
		}
		var valuations []fixtureShareClass
		if err := json.Unmarshal([]byte(raw), &valuations); err != nil {
			return nil, err
		}
		if valuations == nil {
			valuations = []fixtureShareClass{}
		}
		item := generated.FairMarketValue{
			Id: id, EffectiveDate: generated.CalendarDate{Value: effective}, ExpirationDate: generated.CalendarDate{Value: expiration},
			ShareClassValuations: make([]generated.ShareClassValuation, 0, len(valuations)),
		}
		for _, valuation := range valuations {
			item.ShareClassValuations = append(item.ShareClassValuations, generated.ShareClassValuation{
				ShareClassId: valuation.ShareClassID, ShareClassName: valuation.ShareClassName, Common: valuation.Common,
				Price: moneyAPI(valuation.Price),
			})
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start, end, next, err := pageWindow(request.Params.PageSize, request.Params.PageToken, len(items), 50)
	if err != nil {
		return generated.V1alpha1IssuersListFairMarketValuesdefaultJSONResponse{Body: rpcStatus(http.StatusBadRequest, err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	return generated.V1alpha1IssuersListFairMarketValues200JSONResponse{FairMarketValues: items[start:end], NextPageToken: next}, nil
}

func (s *server) V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrant(ctx context.Context, request generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrantRequestObject) (generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrantResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusBadRequest, "request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	setID, err := s.resolveSet(ctx, request.IssuerId, request.DraftOptionGrantSetId)
	if errors.Is(err, errSetNotFound) {
		return generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "draft option grant set not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	id, err := s.ids.Next(ctx, "carta.draftOptionGrant")
	if err != nil {
		return nil, fmt.Errorf("carta: allocate draft option grant ID: %w", err)
	}
	now := s.clock.Now().UTC().Format("2006-01-02T15:04:05Z")
	input := request.Body.DraftOptionGrant
	draft := fixtureDraft{
		ID: id, SetID: setID, IssuerID: request.IssuerId, State: "DRAFT_SECURITY_STATE_DRAFTING",
		StockOptionType: string(input.StockOptionType), Quantity: stringValue{Value: input.Quantity.Value},
		Stakeholder: fixtureDraftStakeholder{Name: input.Stakeholder.Name, Email: input.Stakeholder.Email},
		CreateTime:  stringValue{Value: now}, UpdateTime: stringValue{Value: now},
		EarlyExercise: input.EarlyExercise,
	}
	if input.GrantReason != nil {
		draft.GrantReason = string(*input.GrantReason)
	}
	if input.Notes != nil {
		draft.Notes = *input.Notes
	}
	if input.ExercisePrice != nil {
		price := moneyFixture(*input.ExercisePrice)
		draft.ExercisePrice = &price
	}
	if input.Stakeholder.EmployeeId != nil {
		draft.Stakeholder.EmployeeID = *input.Stakeholder.EmployeeId
	}
	if input.Stakeholder.Type != nil {
		draft.Stakeholder.Type = string(*input.Stakeholder.Type)
	}
	if input.Stakeholder.Relationship != nil {
		draft.Stakeholder.Relationship = string(*input.Stakeholder.Relationship)
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	var position int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM draft_option_grants`).Scan(&position); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO draft_option_grants(position, id, issuer_id, set_id, body) VALUES(?, ?, ?, ?, ?)`,
		position, draft.ID, draft.IssuerID, draft.SetID, string(raw)); err != nil {
		return nil, err
	}
	return generated.V1alpha1IssuersDraftsecuritiesCreateDraftOptionGrant200JSONResponse{DraftOptionGrant: draftAPI(draft)}, nil
}

func (s *server) V1alpha1IssuersDraftsecuritiesGetDraftOptionGrant(ctx context.Context, request generated.V1alpha1IssuersDraftsecuritiesGetDraftOptionGrantRequestObject) (generated.V1alpha1IssuersDraftsecuritiesGetDraftOptionGrantResponseObject, error) {
	if err := s.requireIssuer(ctx, request.IssuerId); err != nil {
		if errors.Is(err, errIssuerNotFound) {
			return generated.V1alpha1IssuersDraftsecuritiesGetDraftOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT body FROM draft_option_grants WHERE issuer_id=? AND set_id=? AND id=?`, request.IssuerId, request.DraftOptionGrantSetId, request.DraftOptionGrantId).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.V1alpha1IssuersDraftsecuritiesGetDraftOptionGrantdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "draft option grant not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	var draft fixtureDraft
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&draft); err != nil {
		return nil, err
	}
	return generated.V1alpha1IssuersDraftsecuritiesGetDraftOptionGrant200JSONResponse{DraftOptionGrant: draftAPI(draft)}, nil
}

func (s *server) resolveSet(ctx context.Context, issuerID, setID string) (string, error) {
	if setID == "LATEST" {
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM draft_sets WHERE issuer_id=? ORDER BY created_seq DESC LIMIT 1`, issuerID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return s.insertSet(ctx, issuerID, "")
		}
		return id, err
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM draft_sets WHERE id=? AND issuer_id=?`, setID, issuerID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errSetNotFound
	}
	return id, err
}

func (s *server) insertSet(ctx context.Context, issuerID, id string) (string, error) {
	if id == "" {
		allocated, err := s.ids.Next(ctx, "carta.draftOptionGrantSet")
		if err != nil {
			return "", fmt.Errorf("carta: allocate draft option grant set ID: %w", err)
		}
		id = allocated
	}
	var seq int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(created_seq), 0) FROM draft_sets WHERE issuer_id=?`, issuerID).Scan(&seq); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO draft_sets(id, issuer_id, created_seq) VALUES(?, ?, ?)`, id, issuerID, seq+1); err != nil {
		return "", err
	}
	return id, nil
}

func (s *server) loadIssuer(ctx context.Context) (generated.Issuer, error) {
	var issuer generated.Issuer
	err := s.db.QueryRowContext(ctx, `SELECT id, legal_name, doing_business_as_name, website FROM issuer`).Scan(&issuer.Id, &issuer.LegalName, &issuer.DoingBusinessAsName, &issuer.Website)
	return issuer, err
}

func (s *server) requireIssuer(ctx context.Context, id string) error {
	issuer, err := s.loadIssuer(ctx)
	if err != nil {
		return err
	}
	if issuer.Id != id {
		return errIssuerNotFound
	}
	return nil
}

var (
	errIssuerNotFound = errors.New("issuer not found")
	errSetNotFound    = errors.New("draft option grant set not found")
)

func issuerMissingListStakeholders(err error) (generated.V1alpha1IssuersListStakeholdersResponseObject, error) {
	if errors.Is(err, errIssuerNotFound) {
		return generated.V1alpha1IssuersListStakeholdersdefaultJSONResponse{Body: rpcStatus(http.StatusNotFound, "issuer not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

type scanner interface{ Scan(...any) error }

func scanStakeholder(row scanner) (generated.Stakeholder, error) {
	var id, issuerID, fullName, email, relationship, entityType, country string
	if err := row.Scan(&id, &issuerID, &fullName, &email, &relationship, &entityType, &country); err != nil {
		return generated.Stakeholder{}, err
	}
	item := generated.Stakeholder{
		Id: id, IssuerId: issuerID, FullName: fullName,
		Relationship: generated.StakeholderRelationship(relationship),
		EntityType:   generated.StakeholderEntityType(entityType),
	}
	if email != "" {
		value := email
		item.Email = &value
	}
	if country != "" {
		value := country
		item.Address = &generated.StakeholderAddress{Country: &value}
	}
	return item, nil
}

func scanCertificate(row scanner) (generated.Certificate, error) {
	var item generated.Certificate
	var issueDate, quantity, currency, price string
	if err := row.Scan(&item.Id, &item.IssuerId, &item.StakeholderId, &item.ShareClassName, &item.SecurityLabel, &issueDate, &quantity, &currency, &price); err != nil {
		return generated.Certificate{}, err
	}
	item.IssueDate = generated.CalendarDate{Value: issueDate}
	item.Quantity = generated.StringValue{Value: quantity}
	item.PricePerShare = generated.Money{CurrencyCode: generated.CurrencyCode{Value: currency}, Amount: generated.StringValue{Value: price}}
	return item, nil
}

func scanOptionGrant(row scanner) (generated.OptionGrant, error) {
	var item generated.OptionGrant
	var optionType, issueDate, quantity, outstanding, currency, price string
	if err := row.Scan(&item.Id, &item.IssuerId, &item.StakeholderId, &item.EquityIncentivePlanName, &item.SecurityLabel, &optionType, &issueDate, &quantity, &outstanding, &currency, &price); err != nil {
		return generated.OptionGrant{}, err
	}
	item.StockOptionType = generated.StockOptionType(optionType)
	item.IssueDate = generated.CalendarDate{Value: issueDate}
	item.Quantity = generated.StringValue{Value: quantity}
	item.OutstandingQuantity = generated.StringValue{Value: outstanding}
	item.ExercisePrice = generated.Money{CurrencyCode: generated.CurrencyCode{Value: currency}, Amount: generated.StringValue{Value: price}}
	return item, nil
}

func draftAPI(draft fixtureDraft) generated.DraftOptionGrant {
	out := generated.DraftOptionGrant{
		Id: draft.ID, DraftOptionGrantSetId: draft.SetID, IssuerId: draft.IssuerID,
		State: generated.DraftSecurityState(draft.State), StockOptionType: generated.DraftStockOptionType(draft.StockOptionType),
		Quantity: generated.StringValue{Value: draft.Quantity.Value}, EarlyExercise: draft.EarlyExercise,
		Stakeholder: generated.DraftStakeholder{Name: draft.Stakeholder.Name, Email: draft.Stakeholder.Email},
		CreateTime:  generated.DateTime{Value: draft.CreateTime.Value}, UpdateTime: generated.DateTime{Value: draft.UpdateTime.Value},
	}
	if draft.GrantReason != "" {
		reason := generated.GrantReason(draft.GrantReason)
		out.GrantReason = &reason
	}
	if draft.Notes != "" {
		notes := draft.Notes
		out.Notes = &notes
	}
	if draft.ExercisePrice != nil {
		price := moneyAPI(*draft.ExercisePrice)
		out.ExercisePrice = &price
	}
	if draft.Stakeholder.EmployeeID != "" {
		value := draft.Stakeholder.EmployeeID
		out.Stakeholder.EmployeeId = &value
	}
	if draft.Stakeholder.Type != "" {
		value := generated.DraftStakeholderType(draft.Stakeholder.Type)
		out.Stakeholder.Type = &value
	}
	if draft.Stakeholder.Relationship != "" {
		value := generated.DraftStakeholderRelationship(draft.Stakeholder.Relationship)
		out.Stakeholder.Relationship = &value
	}
	return out
}

func moneyAPI(money fixtureMoney) generated.Money {
	return generated.Money{CurrencyCode: generated.CurrencyCode{Value: money.CurrencyCode.Value}, Amount: generated.StringValue{Value: money.Amount.Value}}
}

func moneyFixture(money generated.Money) fixtureMoney {
	return fixtureMoney{CurrencyCode: stringValue{Value: money.CurrencyCode.Value}, Amount: stringValue{Value: money.Amount.Value}}
}

func pageWindow(size *int32, token *string, n, maxSize int) (int, int, *string, error) {
	limit := 25
	if size != nil {
		limit = int(*size)
		if limit > maxSize {
			limit = maxSize
		}
	}
	offset := 0
	if token != nil && *token != "" {
		parsed, err := strconv.Atoi(*token)
		if err != nil || parsed < 0 || parsed > n {
			return 0, 0, nil, fmt.Errorf("invalid pageToken")
		}
		offset = parsed
	}
	end := offset + limit
	if end > n {
		end = n
	}
	var next *string
	if end < n {
		value := strconv.Itoa(end)
		next = &value
	}
	return offset, end, next, nil
}

func rpcStatus(code int, message string) generated.RpcStatus {
	return generated.RpcStatus{Code: int32(code), Message: message}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcStatus(status, message))
}
