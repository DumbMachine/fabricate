package impact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/impact/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const (
	accountSID      = "acct-acme"
	defaultPageSize = 100
	noteCreator     = "Val Ortega"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

var errUnknownAccount = errors.New("account not found")

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("impact: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("impact: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("impact: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("impact: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			// impact.com uses HTTP Basic (Account SID, Auth Token). The proxy
			// injects a bearer token, so this stand-in is the accepted credential.
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

func (s *server) ListPrograms(ctx context.Context, request generated.ListProgramsRequestObject) (generated.ListProgramsResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return listProgramsError(err)
	}
	programs, err := s.programs(ctx)
	if err != nil {
		return nil, err
	}
	filters := url.Values{}
	filtered := make([]generated.Program, 0, len(programs))
	for _, program := range programs {
		if request.Params.Name != nil && !strings.EqualFold(program.Name, *request.Params.Name) {
			continue
		}
		if request.Params.State != nil && program.State != string(*request.Params.State) {
			continue
		}
		filtered = append(filtered, s.programModel(request.AccountSID, program))
	}
	if request.Params.Name != nil && *request.Params.Name != "" {
		filters.Set("Name", *request.Params.Name)
	}
	if request.Params.State != nil && *request.Params.State != "" {
		filters.Set("State", string(*request.Params.State))
	}
	page, pageSize := pageRequest(request.Params.Page, request.Params.PageSize)
	window, meta := paginate(filtered, page, pageSize, "/Advertisers/"+request.AccountSID+"/Campaigns", filters)
	return generated.ListPrograms200JSONResponse{
		Campaigns: window, Page: meta.page, Numpages: meta.numPages, Pagesize: meta.pageSize,
		Total: meta.total, Start: meta.start, End: meta.end, Uri: meta.uri,
		Firstpageuri: meta.first, Previouspageuri: meta.prev, Nextpageuri: meta.next, Lastpageuri: meta.last,
	}, nil
}

func (s *server) GetProgramById(ctx context.Context, request generated.GetProgramByIdRequestObject) (generated.GetProgramByIdResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return getProgramError(err)
	}
	program, err := s.program(ctx, request.CampaignId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetProgramByIddefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetProgramById200JSONResponse(s.programModel(request.AccountSID, program)), nil
}

func (s *server) ListPartners(ctx context.Context, request generated.ListPartnersRequestObject) (generated.ListPartnersResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return listPartnersError(err)
	}
	if request.Params.CampaignId != nil {
		if _, err := s.program(ctx, *request.Params.CampaignId); errors.Is(err, sql.ErrNoRows) {
			return generated.ListPartnersdefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
		} else if err != nil {
			return nil, err
		}
	}
	partners, err := s.partners(ctx)
	if err != nil {
		return nil, err
	}
	filters := url.Values{}
	filtered := make([]generated.Partner, 0, len(partners))
	for _, partner := range partners {
		if !containsID(request.Params.Id, partner.ID) {
			continue
		}
		if request.Params.CampaignId != nil && partner.CampaignID != *request.Params.CampaignId {
			continue
		}
		if request.Params.State != nil && partner.State != string(*request.Params.State) {
			continue
		}
		filtered = append(filtered, s.partnerModel(request.AccountSID, partner))
	}
	if request.Params.Id != nil && *request.Params.Id != "" {
		filters.Set("Id", *request.Params.Id)
	}
	if request.Params.CampaignId != nil {
		filters.Set("CampaignId", strconv.Itoa(*request.Params.CampaignId))
	}
	if request.Params.State != nil && *request.Params.State != "" {
		filters.Set("State", string(*request.Params.State))
	}
	page, pageSize := pageRequest(request.Params.Page, request.Params.PageSize)
	window, meta := paginate(filtered, page, pageSize, "/Advertisers/"+request.AccountSID+"/MediaPartners", filters)
	return generated.ListPartners200JSONResponse{
		Partners: window, Page: meta.page, Numpages: meta.numPages, Pagesize: meta.pageSize,
		Total: meta.total, Start: meta.start, End: meta.end, Uri: meta.uri,
		Firstpageuri: meta.first, Previouspageuri: meta.prev, Nextpageuri: meta.next, Lastpageuri: meta.last,
	}, nil
}

func (s *server) GetPartnerById(ctx context.Context, request generated.GetPartnerByIdRequestObject) (generated.GetPartnerByIdResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return getPartnerError(err)
	}
	partner, err := s.partner(ctx, request.PartnerId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetPartnerByIddefaultJSONResponse{Body: apiError("partner not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetPartnerById200JSONResponse(s.partnerModel(request.AccountSID, partner)), nil
}

func (s *server) ListActions(ctx context.Context, request generated.ListActionsRequestObject) (generated.ListActionsResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return listActionsError(err)
	}
	if _, err := s.program(ctx, request.Params.CampaignId); errors.Is(err, sql.ErrNoRows) {
		return generated.ListActionsdefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
	} else if err != nil {
		return nil, err
	}
	actions, err := s.actions(ctx, request.Params.CampaignId, value(request.Params.State), value(request.Params.Oid))
	if err != nil {
		return nil, err
	}
	models := make([]generated.Action, 0, len(actions))
	for _, action := range actions {
		models = append(models, s.actionModel(request.AccountSID, action))
	}
	filters := url.Values{}
	filters.Set("CampaignId", strconv.Itoa(request.Params.CampaignId))
	if request.Params.State != nil && *request.Params.State != "" {
		filters.Set("State", string(*request.Params.State))
	}
	if request.Params.Oid != nil && *request.Params.Oid != "" {
		filters.Set("Oid", *request.Params.Oid)
	}
	page, pageSize := pageRequest(request.Params.Page, request.Params.PageSize)
	window, meta := paginate(models, page, pageSize, "/Advertisers/"+request.AccountSID+"/Actions", filters)
	return generated.ListActions200JSONResponse{
		Actions: window, Page: meta.page, Numpages: meta.numPages, Pagesize: meta.pageSize,
		Total: meta.total, Start: meta.start, End: meta.end, Uri: meta.uri,
		Firstpageuri: meta.first, Previouspageuri: meta.prev, Nextpageuri: meta.next, Lastpageuri: meta.last,
	}, nil
}

func (s *server) GetActionById(ctx context.Context, request generated.GetActionByIdRequestObject) (generated.GetActionByIdResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return getActionError(err)
	}
	action, err := s.resolveAction(ctx, request.ActionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetActionByIddefaultJSONResponse{Body: apiError("action not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetActionById200JSONResponse(s.actionModel(request.AccountSID, action)), nil
}

func (s *server) ListActionItems(ctx context.Context, request generated.ListActionItemsRequestObject) (generated.ListActionItemsResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return listItemsError(err)
	}
	action, err := s.resolveAction(ctx, request.ActionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ListActionItemsdefaultJSONResponse{Body: apiError("action not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := s.items(ctx, action.ID)
	if err != nil {
		return nil, err
	}
	models := make([]generated.ActionItem, 0, len(items))
	for _, item := range items {
		models = append(models, generated.ActionItem{
			Sku: item.SKU, ItemName: item.Name, Quantity: strconv.Itoa(item.Quantity),
			SaleAmount: formatMajor(item.SaleAmountPaise), SaleAmountCurrency: action.Currency,
			Uri: itemURI(request.AccountSID, action.ID, item.SKU),
		})
	}
	page, pageSize := pageRequest(request.Params.Page, request.Params.PageSize)
	path := "/Advertisers/" + request.AccountSID + "/Actions/" + url.PathEscape(request.ActionId) + "/Items"
	window, meta := paginate(models, page, pageSize, path, url.Values{})
	return generated.ListActionItems200JSONResponse{
		ActionItems: window, Page: meta.page, Numpages: meta.numPages, Pagesize: meta.pageSize,
		Total: meta.total, Start: meta.start, End: meta.end, Uri: meta.uri,
		Firstpageuri: meta.first, Previouspageuri: meta.prev, Nextpageuri: meta.next, Lastpageuri: meta.last,
	}, nil
}

func (s *server) SubmitConversion(ctx context.Context, request generated.SubmitConversionRequestObject) (generated.SubmitConversionResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return submitConversionError(err)
	}
	if request.Body == nil {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body
	if body.CurrencyCode != "INR" {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("CurrencyCode must be INR"), StatusCode: http.StatusBadRequest}, nil
	}
	program, err := s.program(ctx, body.CampaignId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	partner, err := s.partner(ctx, body.MediaPartnerId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("partner not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if partner.CampaignID != program.ID {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("partner is not on this program"), StatusCode: http.StatusBadRequest}, nil
	}
	amount, err := minorUnits(body.Amount)
	if err != nil {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError(err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	var payout int64
	if body.Payout != nil {
		payout, err = minorUnits(*body.Payout)
		if err != nil {
			return generated.SubmitConversiondefaultJSONResponse{Body: apiError(err.Error()), StatusCode: http.StatusBadRequest}, nil
		}
	}
	eventDate := s.now()
	if body.EventDate != nil && *body.EventDate != "" {
		parsed, err := time.Parse(time.RFC3339, *body.EventDate)
		if err != nil {
			return generated.SubmitConversiondefaultJSONResponse{Body: apiError("EventDate must be RFC3339"), StatusCode: http.StatusBadRequest}, nil
		}
		eventDate = parsed.UTC().Format(time.RFC3339)
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM actions WHERE oid=?`, body.OrderId).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("order id already exists"), StatusCode: http.StatusBadRequest}, nil
	}
	id, err := s.ids.Next(ctx, "impact.action")
	if err != nil {
		return nil, fmt.Errorf("impact: allocate action id: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO actions(
		id, campaign_id, partner_id, oid, state, payout_paise, amount_paise, currency,
		event_date, creation_date, customer_city, customer_region, customer_country, customer_post_code, note)
		VALUES(?, ?, ?, ?, 'PENDING', ?, ?, 'INR', ?, ?, '', '', '', '', '')`,
		id, program.ID, partner.ID, body.OrderId, payout, amount, eventDate, s.now())
	if err != nil {
		return nil, err
	}
	return generated.SubmitConversion200JSONResponse{Status: "QUEUED", QueuedUri: actionURI(request.AccountSID, id)}, nil
}

func (s *server) ListNotes(ctx context.Context, request generated.ListNotesRequestObject) (generated.ListNotesResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return listNotesError(err)
	}
	if _, err := s.program(ctx, request.CampaignId); errors.Is(err, sql.ErrNoRows) {
		return generated.ListNotesdefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
	} else if err != nil {
		return nil, err
	}
	notes, err := s.notes(ctx, request.CampaignId, value(request.Params.MediaId))
	if err != nil {
		return nil, err
	}
	models := make([]generated.Note, 0, len(notes))
	for _, note := range notes {
		models = append(models, s.noteModel(request.AccountSID, note))
	}
	filters := url.Values{}
	if request.Params.MediaId != nil && *request.Params.MediaId != "" {
		filters.Set("MediaId", *request.Params.MediaId)
	}
	page, pageSize := pageRequest(request.Params.Page, request.Params.PageSize)
	path := fmt.Sprintf("/Advertisers/%s/Campaigns/%d/Notes", request.AccountSID, request.CampaignId)
	window, meta := paginate(models, page, pageSize, path, filters)
	return generated.ListNotes200JSONResponse{
		Notes: window, Page: meta.page, Numpages: meta.numPages, Pagesize: meta.pageSize,
		Total: meta.total, Start: meta.start, End: meta.end, Uri: meta.uri,
		Firstpageuri: meta.first, Previouspageuri: meta.prev, Nextpageuri: meta.next, Lastpageuri: meta.last,
	}, nil
}

func (s *server) CreateNote(ctx context.Context, request generated.CreateNoteRequestObject) (generated.CreateNoteResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return createNoteError(err)
	}
	if request.Body == nil || request.Body.MediaId == "" || request.Body.Content == "" {
		return generated.CreateNotedefaultJSONResponse{Body: apiError("MediaId and Content are required"), StatusCode: http.StatusBadRequest}, nil
	}
	program, err := s.program(ctx, request.CampaignId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateNotedefaultJSONResponse{Body: apiError("program not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	partner, err := s.partner(ctx, request.Body.MediaId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateNotedefaultJSONResponse{Body: apiError("partner not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if partner.CampaignID != program.ID {
		return generated.CreateNotedefaultJSONResponse{Body: apiError("partner is not on this program"), StatusCode: http.StatusBadRequest}, nil
	}
	noteType := "NONE"
	if request.Body.Type != nil && *request.Body.Type != "" {
		noteType = string(*request.Body.Type)
	}
	id, err := s.ids.Next(ctx, "impact.note")
	if err != nil {
		return nil, fmt.Errorf("impact: allocate note id: %w", err)
	}
	now := s.now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO notes(
		id, campaign_id, partner_id, creator, content, type, creation_date, modification_date)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, id, program.ID, partner.ID, noteCreator, request.Body.Content, noteType, now, now)
	if err != nil {
		return nil, err
	}
	return generated.CreateNote200JSONResponse{Status: "OK", Uri: noteURI(request.AccountSID, program.ID, id)}, nil
}

func (s *server) GetNoteById(ctx context.Context, request generated.GetNoteByIdRequestObject) (generated.GetNoteByIdResponseObject, error) {
	if err := s.requireAccount(ctx, request.AccountSID); err != nil {
		return getNoteError(err)
	}
	note, err := s.note(ctx, request.CampaignId, request.NoteId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetNoteByIddefaultJSONResponse{Body: apiError("note not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetNoteById200JSONResponse(s.noteModel(request.AccountSID, note)), nil
}

type programRow struct {
	ID                                  int
	Name, State, Type, ShortDescription string
}

type partnerRow struct {
	ID                                                       string
	CampaignID                                               int
	CampaignName                                             string
	Name, State, Currency, Website, Description, DateCreated string
}

type actionRow struct {
	ID                                                                    string
	CampaignID                                                            int
	CampaignName                                                          string
	PartnerID, PartnerName                                                string
	Oid, State, Currency, EventDate, CreationDate                         string
	PayoutPaise, AmountPaise                                              int64
	CustomerCity, CustomerRegion, CustomerCountry, CustomerPostCode, Note string
}

type itemRow struct {
	SKU, Name       string
	Quantity        int
	SaleAmountPaise int64
}

type noteRow struct {
	ID, PartnerID, PartnerName, Creator, Content, Type, CreationDate, ModificationDate string
	CampaignID                                                                         int
}

func (s *server) requireAccount(ctx context.Context, sid string) error {
	var got string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='accountSid'`).Scan(&got); err != nil {
		return fmt.Errorf("impact: read account: %w", err)
	}
	if sid != got {
		return errUnknownAccount
	}
	return nil
}

func (s *server) programs(ctx context.Context) ([]programRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, state, type, short_description FROM programs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []programRow{}
	for rows.Next() {
		var program programRow
		if err := rows.Scan(&program.ID, &program.Name, &program.State, &program.Type, &program.ShortDescription); err != nil {
			return nil, err
		}
		out = append(out, program)
	}
	return out, rows.Err()
}

func (s *server) program(ctx context.Context, id int) (programRow, error) {
	var program programRow
	err := s.db.QueryRowContext(ctx, `SELECT id, name, state, type, short_description FROM programs WHERE id=?`, id).
		Scan(&program.ID, &program.Name, &program.State, &program.Type, &program.ShortDescription)
	return program, err
}

func (s *server) partners(ctx context.Context) ([]partnerRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mp.id, mp.campaign_id, p.name, mp.name, mp.state, mp.currency, mp.website, mp.description, mp.date_created
		FROM partners mp JOIN programs p ON p.id = mp.campaign_id
		ORDER BY mp.date_created, mp.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []partnerRow{}
	for rows.Next() {
		var partner partnerRow
		if err := rows.Scan(&partner.ID, &partner.CampaignID, &partner.CampaignName, &partner.Name, &partner.State,
			&partner.Currency, &partner.Website, &partner.Description, &partner.DateCreated); err != nil {
			return nil, err
		}
		out = append(out, partner)
	}
	return out, rows.Err()
}

func (s *server) partner(ctx context.Context, id string) (partnerRow, error) {
	var partner partnerRow
	err := s.db.QueryRowContext(ctx, `SELECT mp.id, mp.campaign_id, p.name, mp.name, mp.state, mp.currency, mp.website, mp.description, mp.date_created
		FROM partners mp JOIN programs p ON p.id = mp.campaign_id WHERE mp.id=?`, id).
		Scan(&partner.ID, &partner.CampaignID, &partner.CampaignName, &partner.Name, &partner.State,
			&partner.Currency, &partner.Website, &partner.Description, &partner.DateCreated)
	return partner, err
}

func (s *server) actions(ctx context.Context, campaignID int, state, oid string) ([]actionRow, error) {
	query := actionSelect + ` WHERE a.campaign_id=?`
	args := []any{campaignID}
	if state != "" {
		query += ` AND a.state=?`
		args = append(args, state)
	}
	if oid != "" {
		query += ` AND a.oid=?`
		args = append(args, oid)
	}
	query += ` ORDER BY a.creation_date DESC, a.id DESC`
	return s.queryActions(ctx, query, args...)
}

func (s *server) resolveAction(ctx context.Context, id string) (actionRow, error) {
	row, err := s.actionWhere(ctx, `a.id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return s.actionWhere(ctx, `a.oid=?`, id)
	}
	return row, err
}

func (s *server) actionWhere(ctx context.Context, clause string, arg any) (actionRow, error) {
	rows, err := s.queryActions(ctx, actionSelect+` WHERE `+clause, arg)
	if err != nil {
		return actionRow{}, err
	}
	if len(rows) == 0 {
		return actionRow{}, sql.ErrNoRows
	}
	return rows[0], nil
}

const actionSelect = `SELECT a.id, a.campaign_id, p.name, a.partner_id, mp.name, a.oid, a.state, a.payout_paise, a.amount_paise,
	a.currency, a.event_date, a.creation_date, a.customer_city, a.customer_region, a.customer_country, a.customer_post_code, a.note
	FROM actions a
	JOIN programs p ON p.id = a.campaign_id
	JOIN partners mp ON mp.id = a.partner_id`

func (s *server) queryActions(ctx context.Context, query string, args ...any) ([]actionRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []actionRow{}
	for rows.Next() {
		var action actionRow
		if err := rows.Scan(&action.ID, &action.CampaignID, &action.CampaignName, &action.PartnerID, &action.PartnerName,
			&action.Oid, &action.State, &action.PayoutPaise, &action.AmountPaise, &action.Currency, &action.EventDate,
			&action.CreationDate, &action.CustomerCity, &action.CustomerRegion, &action.CustomerCountry,
			&action.CustomerPostCode, &action.Note); err != nil {
			return nil, err
		}
		out = append(out, action)
	}
	return out, rows.Err()
}

func (s *server) items(ctx context.Context, actionID string) ([]itemRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sku, name, quantity, sale_amount_paise FROM action_items
		WHERE action_id=? ORDER BY position, sku`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []itemRow{}
	for rows.Next() {
		var item itemRow
		if err := rows.Scan(&item.SKU, &item.Name, &item.Quantity, &item.SaleAmountPaise); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *server) notes(ctx context.Context, campaignID int, mediaID string) ([]noteRow, error) {
	query := noteSelect + ` WHERE n.campaign_id=?`
	args := []any{campaignID}
	if mediaID != "" {
		query += ` AND n.partner_id=?`
		args = append(args, mediaID)
	}
	query += ` ORDER BY n.creation_date, n.id`
	return s.queryNotes(ctx, query, args...)
}

func (s *server) note(ctx context.Context, campaignID int, id string) (noteRow, error) {
	rows, err := s.queryNotes(ctx, noteSelect+` WHERE n.campaign_id=? AND n.id=?`, campaignID, id)
	if err != nil {
		return noteRow{}, err
	}
	if len(rows) == 0 {
		return noteRow{}, sql.ErrNoRows
	}
	return rows[0], nil
}

const noteSelect = `SELECT n.id, n.campaign_id, n.partner_id, mp.name, n.creator, n.content, n.type, n.creation_date, n.modification_date
	FROM notes n JOIN partners mp ON mp.id = n.partner_id`

func (s *server) queryNotes(ctx context.Context, query string, args ...any) ([]noteRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []noteRow{}
	for rows.Next() {
		var note noteRow
		if err := rows.Scan(&note.ID, &note.CampaignID, &note.PartnerID, &note.PartnerName, &note.Creator, &note.Content,
			&note.Type, &note.CreationDate, &note.ModificationDate); err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, rows.Err()
}

func (s *server) programModel(account string, program programRow) generated.Program {
	return generated.Program{
		Id: program.ID, Name: program.Name, State: generated.ProgramState(program.State),
		Type: generated.ProgramType(program.Type), ShortDescription: program.ShortDescription,
		Uri: programURI(account, program.ID),
	}
}

func (s *server) partnerModel(account string, partner partnerRow) generated.Partner {
	return generated.Partner{
		Id: partner.ID, Name: partner.Name, Description: partner.Description, Website: partner.Website,
		State: generated.PartnerState(partner.State), Currency: partner.Currency, DateCreated: partner.DateCreated,
		Programs: []generated.PartnerProgram{{Id: strconv.Itoa(partner.CampaignID), Name: partner.CampaignName}},
		Uri:      partnerURI(account, partner.ID),
	}
}

func (s *server) actionModel(account string, action actionRow) generated.Action {
	return generated.Action{
		Id: action.ID, CampaignId: action.CampaignID, CampaignName: action.CampaignName,
		MediaPartnerId: action.PartnerID, MediaPartnerName: action.PartnerName,
		State: generated.ActionState(action.State), Payout: majorUnits(action.PayoutPaise),
		Amount: majorUnits(action.AmountPaise), Currency: action.Currency,
		EventDate: action.EventDate, CreationDate: action.CreationDate, Oid: action.Oid,
		CustomerCity: strPtr(action.CustomerCity), CustomerRegion: strPtr(action.CustomerRegion),
		CustomerCountry: strPtr(action.CustomerCountry), CustomerPostCode: strPtr(action.CustomerPostCode),
		Note: strPtr(action.Note), Uri: actionURI(account, action.ID),
	}
}

func (s *server) noteModel(account string, note noteRow) generated.Note {
	return generated.Note{
		Id: note.ID, MediaId: note.PartnerID, MediaName: note.PartnerName, Creator: note.Creator,
		CreationDate: note.CreationDate, ModificationDate: note.ModificationDate, Content: note.Content,
		Type: generated.NoteType(note.Type), Uri: noteURI(account, note.CampaignID, note.ID),
	}
}

func (s *server) now() string { return s.clock.Now().UTC().Format(time.RFC3339) }

func listProgramsError(err error) (generated.ListProgramsResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.ListProgramsdefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func getProgramError(err error) (generated.GetProgramByIdResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.GetProgramByIddefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func listPartnersError(err error) (generated.ListPartnersResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.ListPartnersdefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func getPartnerError(err error) (generated.GetPartnerByIdResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.GetPartnerByIddefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func listActionsError(err error) (generated.ListActionsResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.ListActionsdefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func getActionError(err error) (generated.GetActionByIdResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.GetActionByIddefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func listItemsError(err error) (generated.ListActionItemsResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.ListActionItemsdefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func submitConversionError(err error) (generated.SubmitConversionResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.SubmitConversiondefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func listNotesError(err error) (generated.ListNotesResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.ListNotesdefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func createNoteError(err error) (generated.CreateNoteResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.CreateNotedefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

func getNoteError(err error) (generated.GetNoteByIdResponseObject, error) {
	if errors.Is(err, errUnknownAccount) {
		return generated.GetNoteByIddefaultJSONResponse{Body: apiError("account not found"), StatusCode: http.StatusNotFound}, nil
	}
	return nil, err
}

type pageMeta struct {
	page, numPages, pageSize, total, start, end, uri, first, prev, next, last string
}

func paginate[T any](items []T, page, pageSize int, path string, filters url.Values) ([]T, pageMeta) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	window := items[start:end]
	if window == nil {
		window = []T{}
	}
	numPages := 0
	if total > 0 {
		numPages = (total + pageSize - 1) / pageSize
	}
	startAttr, endAttr := 0, 0
	if end > start {
		startAttr = start
		endAttr = end - 1
	}
	uriFor := func(p int) string {
		query := url.Values{}
		for key, values := range filters {
			query[key] = append([]string(nil), values...)
		}
		query.Set("Page", strconv.Itoa(p))
		query.Set("PageSize", strconv.Itoa(pageSize))
		return path + "?" + query.Encode()
	}
	lastPage := numPages
	if lastPage < 1 {
		lastPage = 1
	}
	prev, next := "", ""
	if numPages > 0 && page > 1 {
		prev = uriFor(page - 1)
	}
	if numPages > 0 && page < numPages {
		next = uriFor(page + 1)
	}
	return window, pageMeta{
		page: strconv.Itoa(page), numPages: strconv.Itoa(numPages), pageSize: strconv.Itoa(pageSize),
		total: strconv.Itoa(total), start: strconv.Itoa(startAttr), end: strconv.Itoa(endAttr),
		uri: uriFor(page), first: uriFor(1), prev: prev, next: next, last: uriFor(lastPage),
	}
}

func pageRequest(page, pageSize *int) (int, int) {
	p, s := 1, defaultPageSize
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		s = *pageSize
	}
	return p, s
}

func containsID(filter *string, id string) bool {
	if filter == nil || strings.TrimSpace(*filter) == "" {
		return true
	}
	for _, part := range strings.Split(*filter, ",") {
		if strings.TrimSpace(part) == id {
			return true
		}
	}
	return false
}

func minorUnits(major float32) (int64, error) {
	if major < 0 || math.IsNaN(float64(major)) || math.IsInf(float64(major), 0) {
		return 0, fmt.Errorf("amount must be a non-negative two-decimal value")
	}
	scaled := float64(major) * 100
	nearest := math.Round(scaled)
	if math.Abs(scaled-nearest) > 1e-4 {
		return 0, fmt.Errorf("amount must be a non-negative two-decimal value")
	}
	return int64(nearest), nil
}

func majorUnits(paise int64) float32 { return float32(paise) / 100 }

func formatMajor(paise int64) string {
	return strconv.FormatFloat(float64(paise)/100, 'f', 2, 64)
}

func programURI(account string, id int) string {
	return fmt.Sprintf("/Advertisers/%s/Campaigns/%d", account, id)
}

func partnerURI(account, id string) string {
	return fmt.Sprintf("/Advertisers/%s/MediaPartners/%s", account, url.PathEscape(id))
}

func actionURI(account, id string) string {
	return fmt.Sprintf("/Advertisers/%s/Actions/%s", account, url.PathEscape(id))
}

func itemURI(account, actionID, sku string) string {
	return fmt.Sprintf("/Advertisers/%s/Actions/%s/Items/%s", account, url.PathEscape(actionID), url.PathEscape(sku))
}

func noteURI(account string, campaignID int, id string) string {
	return fmt.Sprintf("/Advertisers/%s/Campaigns/%d/Notes/%s", account, campaignID, url.PathEscape(id))
}

func apiError(message string) generated.APIError {
	return generated.APIError{Status: "ERROR", Message: message}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(message))
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func value[T ~string](pointer *T) string {
	if pointer == nil {
		return ""
	}
	return string(*pointer)
}
