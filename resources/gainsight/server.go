package gainsight

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/gainsight/generated"
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
		return nil, fmt.Errorf("gainsight: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("gainsight: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("gainsight: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("gainsight: load OpenAPI: %w", err)
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
			code := "GSOBJ_1001"
			if status == http.StatusUnauthorized {
				code = "UNAUTHORIZED"
			}
			writeError(w, status, code, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GainsightCompanyQuery(ctx context.Context, request generated.GainsightCompanyQueryRequestObject) (generated.GainsightCompanyQueryResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightCompanyQuery200JSONResponse(listEnvelope(requestID, false, "GSOBJ_1001", "Request body is required", nil)), nil
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	records := make([]generated.Record, 0, len(companies))
	for _, company := range companies {
		records = append(records, companyWire(company))
	}
	limit, offset := 5000, 0
	if request.Body.Limit != nil {
		limit = *request.Body.Limit
	}
	if request.Body.Offset != nil {
		offset = *request.Body.Offset
	}
	projected, code, desc := filterProject(records, request.Body.Where, request.Body.OrderBy, offset, limit, request.Body.Select)
	if code != "" {
		return generated.GainsightCompanyQuery200JSONResponse(listEnvelope(requestID, false, code, desc, nil)), nil
	}
	return generated.GainsightCompanyQuery200JSONResponse(listEnvelope(requestID, true, "", "", projected)), nil
}

func (s *server) GainsightCompanyInsert(ctx context.Context, request generated.GainsightCompanyInsertRequestObject) (generated.GainsightCompanyInsertResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightCompanyInsert200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", "Request body is required", nil)), nil
	}
	existing, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	names := map[string]struct{}{}
	for _, company := range existing {
		names[company.Name] = struct{}{}
	}
	now := s.clock.Now().UTC().UnixMilli()
	pending := make([]fixtureCompany, 0, len(request.Body.Records))
	for _, record := range request.Body.Records {
		company, err := companyFromWrite(record, now)
		if err != nil {
			return generated.GainsightCompanyInsert200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", err.Error(), nil)), nil
		}
		if _, exists := names[company.Name]; exists {
			return generated.GainsightCompanyInsert200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", "duplicate company name "+company.Name, nil)), nil
		}
		id, err := s.ids.Next(ctx, "gainsight.company")
		if err != nil {
			return nil, err
		}
		company.Gsid = id
		names[company.Name] = struct{}{}
		pending = append(pending, company)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stored := make([]generated.Record, 0, len(pending))
	for _, company := range pending {
		if err := insertCompany(ctx, tx, company); err != nil {
			return nil, err
		}
		stored = append(stored, companyWire(company))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.GainsightCompanyInsert200JSONResponse(batchEnvelope(requestID, true, "", "", stored)), nil
}

func (s *server) GainsightCompanyUpdate(ctx context.Context, request generated.GainsightCompanyUpdateRequestObject) (generated.GainsightCompanyUpdateResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", "Request body is required", nil)), nil
	}
	keys := splitKeys(request.Params.Keys)
	if len(keys) == 0 || len(keys) > 3 {
		return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", "keys must list one to three Company fields", nil)), nil
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().UnixMilli()
	updated := []fixtureCompany{}
	for _, record := range request.Body.Records {
		matches := []int{}
		for i, company := range companies {
			ok, err := companyMatchesKeys(company, record, keys)
			if err != nil {
				return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", err.Error(), nil)), nil
			}
			if ok {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1002", "No data found for given criteria", nil)), nil
		}
		for _, index := range matches {
			if err := applyCompanyPatch(&companies[index], record, keys, now); err != nil {
				return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", err.Error(), nil)), nil
			}
			updated = append(updated, companies[index])
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, company := range updated {
		if _, err := tx.ExecContext(ctx, `UPDATE companies SET name=?, industry=?, arr=?, employees=?, lifecycle_in_weeks=?, original_contract_date=?, renewal_date=?,
			stage=?, status=?, health=?, csm_first_name=?, csm_last_name=?, csm_email=?, domain=?, modified_date=? WHERE gsid=?`,
			company.Name, company.Industry, nullFloat(company.ARR), nullInt(company.Employees), nullInt(company.LifecycleInWeeks),
			company.OriginalContractDate, company.RenewalDate, company.Stage, company.Status, company.Health,
			company.CSMFirstName, company.CSMLastName, company.CSMEmail, company.Domain, company.ModifiedDate, company.Gsid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	records := make([]generated.Record, 0, len(updated))
	for _, company := range updated {
		records = append(records, companyWire(company))
	}
	return generated.GainsightCompanyUpdate200JSONResponse(batchEnvelope(requestID, true, "", "", records)), nil
}

func (s *server) GainsightCompanyDelete(ctx context.Context, request generated.GainsightCompanyDeleteRequestObject) (generated.GainsightCompanyDeleteResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	var ctas, plans int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ctas WHERE company_id=?", request.Gsid).Scan(&ctas); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM success_plans WHERE company_id=?", request.Gsid).Scan(&plans); err != nil {
		return nil, err
	}
	if ctas > 0 || plans > 0 {
		desc := "Company is referenced by a CTA or success plan"
		return generated.GainsightCompanyDelete200JSONResponse(deleteEnvelope(requestID, false, "GSOBJ_1001", desc, nil)), nil
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM companies WHERE gsid=?", request.Gsid)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return generated.GainsightCompanyDelete200JSONResponse(deleteEnvelope(requestID, false, "GSOBJ_1002", "No data found for given criteria", nil)), nil
	}
	message := "Record with GSID: " + request.Gsid + " successfully deleted."
	return generated.GainsightCompanyDelete200JSONResponse(deleteEnvelope(requestID, true, "", "", &message)), nil
}

func (s *server) GainsightCtaList(ctx context.Context, request generated.GainsightCtaListRequestObject) (generated.GainsightCtaListResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightCtaList200JSONResponse(listEnvelope(requestID, false, "COCKPIT_5101", "Request body is required", nil)), nil
	}
	ctas, err := loadCTAs(ctx, s.db)
	if err != nil {
		return nil, err
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	byID := map[string]fixtureCompany{}
	for _, company := range companies {
		byID[company.Gsid] = company
	}
	records := make([]generated.Record, 0, len(ctas))
	for _, cta := range ctas {
		records = append(records, ctaWire(cta, byID[cta.CompanyID]))
	}
	pageSize, pageNumber := 100, 1
	if request.Body.PageSize != nil {
		pageSize = *request.Body.PageSize
	}
	if request.Body.PageNumber != nil {
		pageNumber = *request.Body.PageNumber
	}
	projected, code, desc := filterProject(records, request.Body.Where, nil, (pageNumber-1)*pageSize, pageSize, request.Body.Select)
	if code != "" {
		return generated.GainsightCtaList200JSONResponse(listEnvelope(requestID, false, "COCKPIT_5101", desc, nil)), nil
	}
	return generated.GainsightCtaList200JSONResponse(listEnvelope(requestID, true, "", "", projected)), nil
}

func (s *server) GainsightCtaCreate(ctx context.Context, request generated.GainsightCtaCreateRequestObject) (generated.GainsightCtaCreateResponseObject, error) {
	envelope, err := s.writeCTAs(ctx, request.Body, false)
	if err != nil {
		return nil, err
	}
	return generated.GainsightCtaCreate200JSONResponse(envelope), nil
}

func (s *server) GainsightCtaUpdate(ctx context.Context, request generated.GainsightCtaUpdateRequestObject) (generated.GainsightCtaUpdateResponseObject, error) {
	envelope, err := s.writeCTAs(ctx, request.Body, true)
	if err != nil {
		return nil, err
	}
	return generated.GainsightCtaUpdate200JSONResponse(envelope), nil
}

func (s *server) writeCTAs(ctx context.Context, body *generated.CtaWriteRequest, update bool) (generated.BulkResultEnvelope, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return generated.BulkResultEnvelope{}, err
	}
	if body == nil {
		return bulkEnvelope(requestID, false, "COCKPIT_5101", "Request body is required", nil, nil), nil
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return generated.BulkResultEnvelope{}, err
	}
	byID := map[string]fixtureCompany{}
	byName := map[string]fixtureCompany{}
	for _, company := range companies {
		byID[company.Gsid] = company
		byName[company.Name] = company
	}
	success := []generated.StringMap{}
	failure := []generated.StringMap{}
	for i, item := range body.Requests {
		ref := sval(item.Record.ReferenceId)
		if ref == "" {
			ref = fmt.Sprintf("%d", i+1)
		}
		if update {
			if err := s.updateCTA(ctx, item.Record, byID, byName); err != nil {
				failure = append(failure, generated.StringMap{ref: err.Error()})
				continue
			}
			success = append(success, generated.StringMap{ref: sval(item.Record.Gsid)})
			continue
		}
		gsid, err := s.createCTA(ctx, item.Record, byID, byName)
		if err != nil {
			failure = append(failure, generated.StringMap{ref: err.Error()})
			continue
		}
		success = append(success, generated.StringMap{ref: gsid})
	}
	return bulkEnvelope(requestID, true, "", "", success, failure), nil
}

func (s *server) createCTA(ctx context.Context, record generated.CtaRecord, byID, byName map[string]fixtureCompany) (string, error) {
	name := sval(record.Name)
	if name == "" {
		return "", errors.New("Name is required")
	}
	company, err := resolveCompany(sval(record.CompanyId), byID, byName)
	if err != nil {
		return "", err
	}
	email := sval(record.OwnerEmail)
	if email == "" && strings.Contains(sval(record.OwnerId), "@") {
		email = sval(record.OwnerId)
	}
	if email == "" {
		return "", errors.New("OwnerEmail or OwnerId is required")
	}
	id, err := s.ids.Next(ctx, "gainsight.cta")
	if err != nil {
		return "", err
	}
	status := sval(record.Status)
	if status == "" {
		status = "New"
	}
	cta := fixtureCTA{
		Gsid: id, Name: name, CompanyID: company.Gsid, OwnerEmail: email, OwnerName: ownerName(email, company),
		DueDate: sval(record.DueDate), Type: sval(record.Type), Reason: sval(record.Reason), Status: status,
		Priority: sval(record.Priority), Comments: sval(record.Comments), EntityType: "COMPANY",
	}
	if record.IsClosed != nil {
		cta.IsClosed = *record.IsClosed
	}
	if record.IsImportant != nil {
		cta.IsImportant = *record.IsImportant
	}
	if err := insertCTA(ctx, s.db, cta); err != nil {
		return "", err
	}
	return id, nil
}

func (s *server) updateCTA(ctx context.Context, record generated.CtaRecord, byID, byName map[string]fixtureCompany) error {
	gsid := sval(record.Gsid)
	if gsid == "" {
		return errors.New("Gsid is required")
	}
	ctas, err := loadCTAs(ctx, s.db)
	if err != nil {
		return err
	}
	var current *fixtureCTA
	for i := range ctas {
		if ctas[i].Gsid == gsid {
			current = &ctas[i]
			break
		}
	}
	if current == nil {
		return errors.New("No data found for given criteria")
	}
	if record.Name != nil {
		current.Name = *record.Name
	}
	if record.CompanyId != nil {
		company, err := resolveCompany(*record.CompanyId, byID, byName)
		if err != nil {
			return err
		}
		current.CompanyID = company.Gsid
	}
	if record.OwnerEmail != nil {
		current.OwnerEmail = *record.OwnerEmail
		current.OwnerName = ownerName(current.OwnerEmail, byID[current.CompanyID])
	}
	if record.DueDate != nil {
		current.DueDate = *record.DueDate
	}
	if record.Type != nil {
		current.Type = *record.Type
	}
	if record.Reason != nil {
		current.Reason = *record.Reason
	}
	if record.Status != nil {
		current.Status = *record.Status
	}
	if record.Priority != nil {
		current.Priority = *record.Priority
	}
	if record.Comments != nil {
		current.Comments = *record.Comments
	}
	if record.IsClosed != nil {
		current.IsClosed = *record.IsClosed
	}
	if record.IsImportant != nil {
		current.IsImportant = *record.IsImportant
	}
	closed, important := 0, 0
	if current.IsClosed {
		closed = 1
	}
	if current.IsImportant {
		important = 1
	}
	_, err = s.db.ExecContext(ctx, `UPDATE ctas SET name=?, company_id=?, owner_email=?, owner_name=?, due_date=?, type=?, reason=?, status=?, priority=?, comments=?, is_closed=?, is_important=? WHERE gsid=?`,
		current.Name, current.CompanyID, current.OwnerEmail, current.OwnerName, current.DueDate, current.Type, current.Reason, current.Status, current.Priority, current.Comments, closed, important, current.Gsid)
	return err
}

func (s *server) GainsightSuccessPlanList(ctx context.Context, request generated.GainsightSuccessPlanListRequestObject) (generated.GainsightSuccessPlanListResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightSuccessPlanList200JSONResponse(listEnvelope(requestID, false, "", "Request body is required", nil)), nil
	}
	plans, err := loadPlans(ctx, s.db)
	if err != nil {
		return nil, err
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	byID := map[string]fixtureCompany{}
	for _, company := range companies {
		byID[company.Gsid] = company
	}
	records := make([]generated.Record, 0, len(plans))
	for _, plan := range plans {
		records = append(records, planWire(plan, byID[plan.CompanyID]))
	}
	pageSize, pageNumber := 100, 1
	if request.Body.PageSize != nil {
		pageSize = *request.Body.PageSize
	}
	if request.Body.PageNumber != nil {
		pageNumber = *request.Body.PageNumber
	}
	projected, code, desc := filterProject(records, request.Body.Where, nil, (pageNumber-1)*pageSize, pageSize, request.Body.Select)
	if code != "" {
		return generated.GainsightSuccessPlanList200JSONResponse(listEnvelope(requestID, false, "", desc, nil)), nil
	}
	return generated.GainsightSuccessPlanList200JSONResponse(listEnvelope(requestID, true, "", "", projected)), nil
}

func (s *server) GainsightSuccessPlanCreate(ctx context.Context, request generated.GainsightSuccessPlanCreateRequestObject) (generated.GainsightSuccessPlanCreateResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightSuccessPlanCreate200JSONResponse(bulkEnvelope(requestID, false, "", "Request body is required", nil, nil)), nil
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	byID := map[string]fixtureCompany{}
	byName := map[string]fixtureCompany{}
	for _, company := range companies {
		byID[company.Gsid] = company
		byName[company.Name] = company
	}
	success := []generated.StringMap{}
	failure := []generated.StringMap{}
	for i, item := range request.Body.Requests {
		ref := sval(item.Record.ReferenceId)
		if ref == "" {
			ref = fmt.Sprintf("%d", i+1)
		}
		name := sval(item.Record.Name)
		if name == "" {
			failure = append(failure, generated.StringMap{ref: "Name is required"})
			continue
		}
		company, err := resolveCompany(sval(item.Record.CompanyId), byID, byName)
		if err != nil {
			failure = append(failure, generated.StringMap{ref: err.Error()})
			continue
		}
		email := sval(item.Record.OwnerEmail)
		if email == "" && strings.Contains(sval(item.Record.OwnerId), "@") {
			email = sval(item.Record.OwnerId)
		}
		if email == "" {
			failure = append(failure, generated.StringMap{ref: "OwnerEmail or OwnerId is required"})
			continue
		}
		planType := sval(item.Record.SuccessPlanType)
		if planType == "" {
			failure = append(failure, generated.StringMap{ref: "SuccessPlanType is required"})
			continue
		}
		id, err := s.ids.Next(ctx, "gainsight.successPlan")
		if err != nil {
			return nil, err
		}
		status := sval(item.Record.Status)
		if status == "" {
			status = "Draft"
		}
		plan := fixturePlan{
			Gsid: id, Name: name, CompanyID: company.Gsid, OwnerEmail: email, OwnerName: ownerName(email, company),
			DueDate: sval(item.Record.DueDate), Type: planType, Status: status, ActionPlan: sval(item.Record.ActionPlan), EntityType: "COMPANY",
		}
		if err := insertPlan(ctx, s.db, plan); err != nil {
			return nil, err
		}
		success = append(success, generated.StringMap{ref: id})
	}
	return generated.GainsightSuccessPlanCreate200JSONResponse(bulkEnvelope(requestID, true, "", "", success, failure)), nil
}

func (s *server) GainsightActivityCreate(ctx context.Context, request generated.GainsightActivityCreateRequestObject) (generated.GainsightActivityCreateResponseObject, error) {
	if request.Body == nil {
		return generated.GainsightActivityCreate200JSONResponse(activityEnvelope(false, map[string][]generated.ActivityFailure{}, map[string][]generated.ActivitySuccess{})), nil
	}
	companies, err := loadCompanies(ctx, s.db)
	if err != nil {
		return nil, err
	}
	ctas, err := loadCTAs(ctx, s.db)
	if err != nil {
		return nil, err
	}
	plans, err := loadPlans(ctx, s.db)
	if err != nil {
		return nil, err
	}
	byID := map[string]fixtureCompany{}
	byName := map[string]fixtureCompany{}
	for _, company := range companies {
		byID[company.Gsid] = company
		byName[company.Name] = company
	}
	ctaByID := map[string]fixtureCTA{}
	for _, cta := range ctas {
		ctaByID[cta.Gsid] = cta
	}
	planByID := map[string]fixturePlan{}
	for _, plan := range plans {
		planByID[plan.Gsid] = plan
	}
	existing, err := loadActivities(ctx, s.db)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, activity := range existing {
		seen[activity.ExternalID] = struct{}{}
	}
	failures := map[string][]generated.ActivityFailure{}
	success := map[string][]generated.ActivitySuccess{}
	for _, record := range request.Body.Records {
		externalID := record.ExternalId
		if _, exists := seen[externalID]; exists {
			failures[externalID] = []generated.ActivityFailure{{ErrorCode: "GS_TL_10_0207", Message: "duplicate ExternalId"}}
			continue
		}
		activity, fail := s.activityFromWrite(ctx, record, byID, byName, ctaByID, planByID)
		if fail != nil {
			failures[externalID] = []generated.ActivityFailure{*fail}
			continue
		}
		if err := insertActivity(ctx, s.db, activity); err != nil {
			return nil, err
		}
		seen[externalID] = struct{}{}
		success[externalID] = []generated.ActivitySuccess{{ActivityId: activity.Gsid}}
	}
	return generated.GainsightActivityCreate200JSONResponse(activityEnvelope(true, failures, success)), nil
}

func (s *server) activityFromWrite(ctx context.Context, record generated.ActivityRecord, byID, byName map[string]fixtureCompany, ctas map[string]fixtureCTA, plans map[string]fixturePlan) (fixtureActivity, *generated.ActivityFailure) {
	author := sval(record.Author)
	if author == "" {
		author = sval(record.AuthorId)
	}
	if author == "" {
		return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0206", Message: "Author is required"}
	}
	authorID := sval(record.AuthorId)
	if authorID == "" {
		authorID = userID(author)
	}
	companyID := sval(record.GsCompanyId)
	contextID := sval(record.ContextId)
	switch string(record.ContextName) {
	case "Company":
		if companyID == "" {
			company, ok := byName[sval(record.CompanyName)]
			if !ok {
				return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "company was not found"}
			}
			companyID = company.Gsid
		} else if _, ok := byID[companyID]; !ok {
			return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "company was not found"}
		}
	case "CTA":
		cta, ok := ctas[contextID]
		if !ok {
			return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "CTA was not found"}
		}
		if companyID == "" {
			companyID = cta.CompanyID
		} else if companyID != cta.CompanyID {
			return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "GsCompanyId does not match the CTA"}
		}
	case "SuccessPlan":
		plan, ok := plans[contextID]
		if !ok {
			return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "Success Plan was not found"}
		}
		if companyID == "" {
			companyID = plan.CompanyID
		} else if companyID != plan.CompanyID {
			return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0213", Message: "GsCompanyId does not match the Success Plan"}
		}
	default:
		return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0206", Message: "Activity context name for key 'ContextName' missing"}
	}
	id, err := s.ids.Next(ctx, "gainsight.activity")
	if err != nil {
		return fixtureActivity{}, &generated.ActivityFailure{ErrorCode: "GS_TL_10_0500", Message: err.Error()}
	}
	activityDate := sval(record.ActivityDate)
	if activityDate == "" {
		activityDate = s.clock.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return fixtureActivity{
		Gsid: id, ExternalID: record.ExternalId, ContextName: string(record.ContextName), ContextID: contextID,
		CompanyID: companyID, Author: author, AuthorID: authorID, TypeName: record.TypeName, Subject: record.Subject,
		Notes: record.Notes, ActivityDate: activityDate,
	}, nil
}

func (s *server) GainsightActivityQuery(ctx context.Context, request generated.GainsightActivityQueryRequestObject) (generated.GainsightActivityQueryResponseObject, error) {
	requestID, err := s.requestID(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.GainsightActivityQuery200JSONResponse(batchEnvelope(requestID, false, "GSOBJ_1001", "Request body is required", nil)), nil
	}
	activities, err := loadActivities(ctx, s.db)
	if err != nil {
		return nil, err
	}
	records := make([]generated.Record, 0, len(activities))
	for _, activity := range activities {
		records = append(records, activityWire(activity))
	}
	limit, offset := 5000, 0
	if request.Body.Limit != nil {
		limit = *request.Body.Limit
	}
	if request.Body.Offset != nil {
		offset = *request.Body.Offset
	}
	projected, code, desc := filterProject(records, request.Body.Where, request.Body.OrderBy, offset, limit, request.Body.Select)
	if code != "" {
		return generated.GainsightActivityQuery200JSONResponse(batchEnvelope(requestID, false, code, desc, nil)), nil
	}
	return generated.GainsightActivityQuery200JSONResponse(batchEnvelope(requestID, true, "", "", projected)), nil
}

func (s *server) requestID(ctx context.Context) (string, error) {
	return s.ids.Next(ctx, "gainsight.request")
}
