package hubspot

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
	"github.com/dumbmachine/fabricate/resources/hubspot/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const dealsAlias = "/crm/v3/objects/deals"
const dealsCanonical = "/crm/v3/objects/0-3"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("hubspot: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("hubspot: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("hubspot: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("hubspot: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{
			// HubSpot's published CRM specs mark search/create fields required
			// even when the live API accepts partial bodies.
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
	inner := validator(generatedHandler)
	impl.handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, dealsAlias) {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = dealsCanonical + strings.TrimPrefix(request.URL.Path, dealsAlias)
			inner.ServeHTTP(w, cloned)
			return
		}
		inner.ServeHTTP(w, request)
	})
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) Getcrmv3objectscontactsGetPage(ctx context.Context, request generated.Getcrmv3objectscontactsGetPageRequestObject) (generated.Getcrmv3objectscontactsGetPageResponseObject, error) {
	page, err := s.listPage(ctx, "contacts", request.Params.Archived, request.Params.Properties)
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectscontactsGetPage200JSONResponse(page), nil
}

func (s *server) Postcrmv3objectscontactsCreate(ctx context.Context, request generated.Postcrmv3objectscontactsCreateRequestObject) (generated.Postcrmv3objectscontactsCreateResponseObject, error) {
	object, err := s.createObject(ctx, "contacts", propertiesFromCreate(request.Body))
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectscontactsCreate201JSONResponse(object), nil
}

func (s *server) Postcrmv3objectscontactssearchDoSearch(ctx context.Context, request generated.Postcrmv3objectscontactssearchDoSearchRequestObject) (generated.Postcrmv3objectscontactssearchDoSearchResponseObject, error) {
	page, err := s.search(ctx, "contacts", request.Body)
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectscontactssearchDoSearch200JSONResponse(page), nil
}

func (s *server) Getcrmv3objectscontactsContactIdGetById(ctx context.Context, request generated.Getcrmv3objectscontactsContactIdGetByIdRequestObject) (generated.Getcrmv3objectscontactsContactIdGetByIdResponseObject, error) {
	object, err := s.getObject(ctx, "contacts", request.ContactId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Getcrmv3objectscontactsContactIdGetByIddefaultAsteriskResponse{Body: errorBody("contact not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectscontactsContactIdGetById200JSONResponse(withAssociations(object)), nil
}

func (s *server) Patchcrmv3objectscontactsContactIdUpdate(ctx context.Context, request generated.Patchcrmv3objectscontactsContactIdUpdateRequestObject) (generated.Patchcrmv3objectscontactsContactIdUpdateResponseObject, error) {
	object, err := s.updateObject(ctx, "contacts", request.ContactId, propertiesFromUpdate(request.Body))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Patchcrmv3objectscontactsContactIdUpdatedefaultAsteriskResponse{Body: errorBody("contact not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Patchcrmv3objectscontactsContactIdUpdate200JSONResponse(object), nil
}

func (s *server) Getcrmv3objectscompaniesGetPage(ctx context.Context, request generated.Getcrmv3objectscompaniesGetPageRequestObject) (generated.Getcrmv3objectscompaniesGetPageResponseObject, error) {
	page, err := s.listPage(ctx, "companies", request.Params.Archived, request.Params.Properties)
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectscompaniesGetPage200JSONResponse(page), nil
}

func (s *server) Postcrmv3objectscompaniesCreate(ctx context.Context, request generated.Postcrmv3objectscompaniesCreateRequestObject) (generated.Postcrmv3objectscompaniesCreateResponseObject, error) {
	object, err := s.createObject(ctx, "companies", propertiesFromCreate(request.Body))
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectscompaniesCreate201JSONResponse(object), nil
}

func (s *server) Postcrmv3objectscompaniessearchDoSearch(ctx context.Context, request generated.Postcrmv3objectscompaniessearchDoSearchRequestObject) (generated.Postcrmv3objectscompaniessearchDoSearchResponseObject, error) {
	page, err := s.search(ctx, "companies", request.Body)
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectscompaniessearchDoSearch200JSONResponse(page), nil
}

func (s *server) Getcrmv3objectscompaniesCompanyIdGetById(ctx context.Context, request generated.Getcrmv3objectscompaniesCompanyIdGetByIdRequestObject) (generated.Getcrmv3objectscompaniesCompanyIdGetByIdResponseObject, error) {
	object, err := s.getObject(ctx, "companies", request.CompanyId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Getcrmv3objectscompaniesCompanyIdGetByIddefaultAsteriskResponse{Body: errorBody("company not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectscompaniesCompanyIdGetById200JSONResponse(withAssociations(object)), nil
}

func (s *server) Patchcrmv3objectscompaniesCompanyIdUpdate(ctx context.Context, request generated.Patchcrmv3objectscompaniesCompanyIdUpdateRequestObject) (generated.Patchcrmv3objectscompaniesCompanyIdUpdateResponseObject, error) {
	object, err := s.updateObject(ctx, "companies", request.CompanyId, propertiesFromUpdate(request.Body))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Patchcrmv3objectscompaniesCompanyIdUpdatedefaultAsteriskResponse{Body: errorBody("company not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Patchcrmv3objectscompaniesCompanyIdUpdate200JSONResponse(object), nil
}

func (s *server) Getcrmv3objects03GetPage(ctx context.Context, request generated.Getcrmv3objects03GetPageRequestObject) (generated.Getcrmv3objects03GetPageResponseObject, error) {
	page, err := s.listPage(ctx, "deals", request.Params.Archived, request.Params.Properties)
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objects03GetPage200JSONResponse(page), nil
}

func (s *server) Postcrmv3objects03Create(ctx context.Context, request generated.Postcrmv3objects03CreateRequestObject) (generated.Postcrmv3objects03CreateResponseObject, error) {
	object, err := s.createObject(ctx, "deals", propertiesFromCreate(request.Body))
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objects03Create201JSONResponse(object), nil
}

func (s *server) Postcrmv3objects03searchDoSearch(ctx context.Context, request generated.Postcrmv3objects03searchDoSearchRequestObject) (generated.Postcrmv3objects03searchDoSearchResponseObject, error) {
	page, err := s.search(ctx, "deals", request.Body)
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objects03searchDoSearch200JSONResponse(page), nil
}

func (s *server) Getcrmv3objects03DealIdGetById(ctx context.Context, request generated.Getcrmv3objects03DealIdGetByIdRequestObject) (generated.Getcrmv3objects03DealIdGetByIdResponseObject, error) {
	object, err := s.getObject(ctx, "deals", request.DealId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Getcrmv3objects03DealIdGetByIddefaultAsteriskResponse{Body: errorBody("deal not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objects03DealIdGetById200JSONResponse(withAssociations(object)), nil
}

func (s *server) Patchcrmv3objects03DealIdUpdate(ctx context.Context, request generated.Patchcrmv3objects03DealIdUpdateRequestObject) (generated.Patchcrmv3objects03DealIdUpdateResponseObject, error) {
	object, err := s.updateObject(ctx, "deals", request.DealId, propertiesFromUpdate(request.Body))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Patchcrmv3objects03DealIdUpdatedefaultAsteriskResponse{Body: errorBody("deal not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Patchcrmv3objects03DealIdUpdate200JSONResponse(object), nil
}

func (s *server) Getcrmv3objectsticketsGetPage(ctx context.Context, request generated.Getcrmv3objectsticketsGetPageRequestObject) (generated.Getcrmv3objectsticketsGetPageResponseObject, error) {
	page, err := s.listPage(ctx, "tickets", request.Params.Archived, request.Params.Properties)
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectsticketsGetPage200JSONResponse(page), nil
}

func (s *server) Postcrmv3objectsticketsCreate(ctx context.Context, request generated.Postcrmv3objectsticketsCreateRequestObject) (generated.Postcrmv3objectsticketsCreateResponseObject, error) {
	object, err := s.createObject(ctx, "tickets", propertiesFromCreate(request.Body))
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectsticketsCreate201JSONResponse(object), nil
}

func (s *server) Postcrmv3objectsticketssearchDoSearch(ctx context.Context, request generated.Postcrmv3objectsticketssearchDoSearchRequestObject) (generated.Postcrmv3objectsticketssearchDoSearchResponseObject, error) {
	page, err := s.search(ctx, "tickets", request.Body)
	if err != nil {
		return nil, err
	}
	return generated.Postcrmv3objectsticketssearchDoSearch200JSONResponse(page), nil
}

func (s *server) Getcrmv3objectsticketsTicketIdGetById(ctx context.Context, request generated.Getcrmv3objectsticketsTicketIdGetByIdRequestObject) (generated.Getcrmv3objectsticketsTicketIdGetByIdResponseObject, error) {
	object, err := s.getObject(ctx, "tickets", request.TicketId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Getcrmv3objectsticketsTicketIdGetByIddefaultAsteriskResponse{Body: errorBody("ticket not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Getcrmv3objectsticketsTicketIdGetById200JSONResponse(withAssociations(object)), nil
}

func (s *server) Patchcrmv3objectsticketsTicketIdUpdate(ctx context.Context, request generated.Patchcrmv3objectsticketsTicketIdUpdateRequestObject) (generated.Patchcrmv3objectsticketsTicketIdUpdateResponseObject, error) {
	object, err := s.updateObject(ctx, "tickets", request.TicketId, propertiesFromUpdate(request.Body))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Patchcrmv3objectsticketsTicketIdUpdatedefaultAsteriskResponse{Body: errorBody("ticket not found"), StatusCode: http.StatusNotFound, ContentType: "application/json"}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.Patchcrmv3objectsticketsTicketIdUpdate200JSONResponse(object), nil
}

func (s *server) listPage(ctx context.Context, objectType string, archived *bool, properties *[]string) (generated.CollectionResponseSimplePublicObjectWithAssociationsForwardPaging, error) {
	objects, err := s.listObjects(ctx, objectType, archivedBool(archived))
	if err != nil {
		return generated.CollectionResponseSimplePublicObjectWithAssociationsForwardPaging{}, err
	}
	results := make([]generated.SimplePublicObjectWithAssociations, 0, len(objects))
	for _, object := range objects {
		results = append(results, withAssociations(selectProperties(object, properties)))
	}
	return generated.CollectionResponseSimplePublicObjectWithAssociationsForwardPaging{Results: results}, nil
}

func (s *server) search(ctx context.Context, objectType string, body *generated.PublicObjectSearchRequest) (generated.CollectionResponseWithTotalSimplePublicObject, error) {
	objects, err := s.listObjects(ctx, objectType, false)
	if err != nil {
		return generated.CollectionResponseWithTotalSimplePublicObject{}, err
	}
	var matched []generated.SimplePublicObject
	for _, object := range objects {
		if matchesSearch(object, body) {
			matched = append(matched, selectProperties(object, propertiesFromSearch(body)))
		}
	}
	return generated.CollectionResponseWithTotalSimplePublicObject{Results: matched, Total: int32(len(matched))}, nil
}

func (s *server) listObjects(ctx context.Context, objectType string, archived bool) ([]generated.SimplePublicObject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, properties, archived, created_at, updated_at
		FROM objects WHERE object_type=? AND archived=? ORDER BY id`, objectType, boolInt(archived))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []generated.SimplePublicObject
	for rows.Next() {
		object, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if objects == nil {
		objects = []generated.SimplePublicObject{}
	}
	return objects, rows.Err()
}

func (s *server) getObject(ctx context.Context, objectType, id string) (generated.SimplePublicObject, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, properties, archived, created_at, updated_at
		FROM objects WHERE object_type=? AND id=?`, objectType, id)
	return scanObject(row)
}

func (s *server) createObject(ctx context.Context, objectType string, properties map[string]string) (generated.SimplePublicObject, error) {
	if properties == nil {
		properties = map[string]string{}
	}
	id, err := s.ids.Next(ctx, "hubspot."+objectType)
	if err != nil {
		return generated.SimplePublicObject{}, err
	}
	now := s.clock.Now().UTC()
	raw, err := json.Marshal(properties)
	if err != nil {
		return generated.SimplePublicObject{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO objects(object_type, id, properties, archived, created_at, updated_at)
		VALUES(?, ?, ?, 0, ?, ?)`, objectType, id, string(raw), now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		return generated.SimplePublicObject{}, err
	}
	return generated.SimplePublicObject{Id: id, Properties: properties, CreatedAt: now, UpdatedAt: now, Archived: false}, nil
}

func (s *server) updateObject(ctx context.Context, objectType, id string, patch map[string]string) (generated.SimplePublicObject, error) {
	object, err := s.getObject(ctx, objectType, id)
	if err != nil {
		return generated.SimplePublicObject{}, err
	}
	if object.Properties == nil {
		object.Properties = map[string]string{}
	}
	for key, value := range patch {
		object.Properties[key] = value
	}
	now := s.clock.Now().UTC()
	raw, err := json.Marshal(object.Properties)
	if err != nil {
		return generated.SimplePublicObject{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE objects SET properties=?, updated_at=? WHERE object_type=? AND id=?`,
		string(raw), now.Format(time.RFC3339), objectType, id); err != nil {
		return generated.SimplePublicObject{}, err
	}
	object.UpdatedAt = now
	return object, nil
}

func scanObject(row interface{ Scan(dest ...any) error }) (generated.SimplePublicObject, error) {
	var id, raw, createdAt, updatedAt string
	var archived int
	if err := row.Scan(&id, &raw, &archived, &createdAt, &updatedAt); err != nil {
		return generated.SimplePublicObject{}, err
	}
	properties := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &properties); err != nil {
		return generated.SimplePublicObject{}, err
	}
	created, _ := time.Parse(time.RFC3339, createdAt)
	updated, _ := time.Parse(time.RFC3339, updatedAt)
	return generated.SimplePublicObject{Id: id, Properties: properties, Archived: archived != 0, CreatedAt: created, UpdatedAt: updated}, nil
}

func withAssociations(object generated.SimplePublicObject) generated.SimplePublicObjectWithAssociations {
	return generated.SimplePublicObjectWithAssociations{
		Id: object.Id, Properties: object.Properties, Archived: object.Archived,
		CreatedAt: object.CreatedAt, UpdatedAt: object.UpdatedAt, ArchivedAt: object.ArchivedAt,
	}
}

func selectProperties(object generated.SimplePublicObject, names *[]string) generated.SimplePublicObject {
	if names == nil || len(*names) == 0 {
		return object
	}
	filtered := map[string]string{}
	for _, name := range *names {
		if value, ok := object.Properties[name]; ok {
			filtered[name] = value
		}
	}
	object.Properties = filtered
	return object
}

func matchesSearch(object generated.SimplePublicObject, body *generated.PublicObjectSearchRequest) bool {
	if body == nil {
		return true
	}
	if body.Query != nil && strings.TrimSpace(*body.Query) != "" {
		needle := strings.ToLower(*body.Query)
		found := false
		for _, value := range object.Properties {
			if strings.Contains(strings.ToLower(value), needle) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(body.FilterGroups) == 0 {
		return true
	}
	for _, group := range body.FilterGroups {
		if matchesFilterGroup(object, group) {
			return true
		}
	}
	return false
}

func matchesFilterGroup(object generated.SimplePublicObject, group generated.FilterGroup) bool {
	for _, filter := range group.Filters {
		value := object.Properties[filter.PropertyName]
		wanted := ""
		if filter.Value != nil {
			wanted = *filter.Value
		}
		switch filter.Operator {
		case generated.EQ:
			if value != wanted {
				return false
			}
		case generated.NEQ:
			if value == wanted {
				return false
			}
		case generated.CONTAINSTOKEN:
			if !strings.Contains(strings.ToLower(value), strings.ToLower(wanted)) {
				return false
			}
		default:
			if value != wanted {
				return false
			}
		}
	}
	return true
}

func propertiesFromCreate(body *generated.SimplePublicObjectInputForCreate) map[string]string {
	if body == nil {
		return map[string]string{}
	}
	return body.Properties
}

func propertiesFromUpdate(body *generated.SimplePublicObjectInput) map[string]string {
	if body == nil {
		return map[string]string{}
	}
	return body.Properties
}

func propertiesFromSearch(body *generated.PublicObjectSearchRequest) *[]string {
	if body == nil || len(body.Properties) == 0 {
		return nil
	}
	return &body.Properties
}

func archivedBool(value *bool) bool { return value != nil && *value }

func errorBody(message string) io.ReadCloser {
	payload, _ := json.Marshal(map[string]any{"status": "error", "message": message, "category": "OBJECT_NOT_FOUND"})
	return io.NopCloser(bytes.NewReader(payload))
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"error","message":%q,"category":"VALIDATION_ERROR"}`, message)))
}
