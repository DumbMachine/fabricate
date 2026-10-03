package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/statuspage/generated"
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
		return nil, fmt.Errorf("statuspage: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("statuspage: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("statuspage: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("statuspage: load OpenAPI: %w", err)
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
			message := err.Error()
			if status == http.StatusUnauthorized {
				message = "Could not authenticate"
			}
			writeError(w, status, message)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListPages(ctx context.Context, _ generated.ListPagesRequestObject) (generated.ListPagesResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, pageSelect+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []generated.Page{}
	for rows.Next() {
		page, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page.wire())
	}
	return generated.ListPages200JSONResponse(pages), rows.Err()
}

func (s *server) GetPage(ctx context.Context, request generated.GetPageRequestObject) (generated.GetPageResponseObject, error) {
	page, err := s.loadPage(ctx, request.PageId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetPagedefaultJSONResponse{Body: errorBody("page not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetPage200JSONResponse(page.wire()), nil
}

func (s *server) ListComponents(ctx context.Context, request generated.ListComponentsRequestObject) (generated.ListComponentsResponseObject, error) {
	if _, err := s.loadPage(ctx, request.PageId); errors.Is(err, sql.ErrNoRows) {
		return generated.ListComponentsdefaultJSONResponse{Body: errorBody("page not found"), StatusCode: http.StatusNotFound}, nil
	} else if err != nil {
		return nil, err
	}
	components, err := s.loadComponents(ctx, request.PageId)
	if err != nil {
		return nil, err
	}
	start, end := window(request.Params.Page, request.Params.PerPage, 100, len(components))
	out := make([]generated.Component, 0, end-start)
	for _, component := range components[start:end] {
		out = append(out, component.wire())
	}
	return generated.ListComponents200JSONResponse(out), nil
}

func (s *server) GetComponent(ctx context.Context, request generated.GetComponentRequestObject) (generated.GetComponentResponseObject, error) {
	component, err := s.loadComponent(ctx, request.PageId, request.ComponentId)
	if status, message, ok := notFound(err); ok {
		return generated.GetComponentdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetComponent200JSONResponse(component.wire()), nil
}

func (s *server) CreateComponent(ctx context.Context, request generated.CreateComponentRequestObject) (generated.CreateComponentResponseObject, error) {
	if _, err := s.loadPage(ctx, request.PageId); errors.Is(err, sql.ErrNoRows) {
		return generated.CreateComponentdefaultJSONResponse{Body: errorBody("page not found"), StatusCode: http.StatusNotFound}, nil
	} else if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.CreateComponentdefaultJSONResponse{Body: errorBody("component is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	body := request.Body.Component
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return generated.CreateComponentdefaultJSONResponse{Body: errorBody("name is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	status := generated.Operational
	if body.Status != nil {
		status = *body.Status
	}
	position := 1
	if body.Position != nil {
		position = *body.Position
	} else {
		if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0)+1 FROM components WHERE page_id=?", request.PageId).Scan(&position); err != nil {
			return nil, err
		}
	}
	id, err := s.ids.Next(ctx, "comp")
	if err != nil {
		return nil, fmt.Errorf("statuspage: allocate component id: %w", err)
	}
	now := s.now()
	component := componentRow{
		ID: id, PageID: request.PageId, Name: name, Description: value(body.Description), Status: string(status),
		Position: position, Showcase: boolValue(body.Showcase), Group: false, OnlyShowIfDegraded: boolValue(body.OnlyShowIfDegraded),
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO components
		(id, page_id, name, description, status, position, showcase, is_group, only_show_if_degraded, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		component.ID, component.PageID, component.Name, component.Description, component.Status, component.Position,
		boolInt(component.Showcase), boolInt(component.Group), boolInt(component.OnlyShowIfDegraded), component.CreatedAt, component.UpdatedAt); err != nil {
		return nil, err
	}
	if err := s.touchPage(ctx, request.PageId, now); err != nil {
		return nil, err
	}
	return generated.CreateComponent201JSONResponse(component.wire()), nil
}

func (s *server) UpdateComponent(ctx context.Context, request generated.UpdateComponentRequestObject) (generated.UpdateComponentResponseObject, error) {
	component, err := s.loadComponent(ctx, request.PageId, request.ComponentId)
	if status, message, ok := notFound(err); ok {
		return generated.UpdateComponentdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.UpdateComponentdefaultJSONResponse{Body: errorBody("component is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	body := request.Body.Component
	if body.Name != nil {
		component.Name = strings.TrimSpace(*body.Name)
	}
	if body.Description != nil {
		component.Description = *body.Description
	}
	if body.Status != nil {
		component.Status = string(*body.Status)
	}
	if body.Position != nil {
		component.Position = *body.Position
	}
	if body.Showcase != nil {
		component.Showcase = *body.Showcase
	}
	if body.OnlyShowIfDegraded != nil {
		component.OnlyShowIfDegraded = *body.OnlyShowIfDegraded
	}
	component.UpdatedAt = s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE components
		SET name=?, description=?, status=?, position=?, showcase=?, only_show_if_degraded=?, updated_at=?
		WHERE id=? AND page_id=?`,
		component.Name, component.Description, component.Status, component.Position, boolInt(component.Showcase),
		boolInt(component.OnlyShowIfDegraded), component.UpdatedAt, component.ID, component.PageID); err != nil {
		return nil, err
	}
	if err := s.touchPage(ctx, request.PageId, component.UpdatedAt); err != nil {
		return nil, err
	}
	return generated.UpdateComponent200JSONResponse(component.wire()), nil
}

func (s *server) ListIncidents(ctx context.Context, request generated.ListIncidentsRequestObject) (generated.ListIncidentsResponseObject, error) {
	incidents, err := s.listIncidents(ctx, request.PageId, false, value(request.Params.Q))
	if status, message, ok := notFound(err); ok {
		return generated.ListIncidentsdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	start, end := window(request.Params.Page, request.Params.Limit, 100, len(incidents))
	return generated.ListIncidents200JSONResponse(incidents[start:end]), nil
}

func (s *server) ListUnresolvedIncidents(ctx context.Context, request generated.ListUnresolvedIncidentsRequestObject) (generated.ListUnresolvedIncidentsResponseObject, error) {
	incidents, err := s.listIncidents(ctx, request.PageId, true, "")
	if status, message, ok := notFound(err); ok {
		return generated.ListUnresolvedIncidentsdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	start, end := window(request.Params.Page, request.Params.PerPage, 100, len(incidents))
	return generated.ListUnresolvedIncidents200JSONResponse(incidents[start:end]), nil
}

func (s *server) GetIncident(ctx context.Context, request generated.GetIncidentRequestObject) (generated.GetIncidentResponseObject, error) {
	incident, err := s.loadIncident(ctx, request.PageId, request.IncidentId)
	if status, message, ok := notFound(err); ok {
		return generated.GetIncidentdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetIncident200JSONResponse(incident), nil
}

func (s *server) CreateIncident(ctx context.Context, request generated.CreateIncidentRequestObject) (generated.CreateIncidentResponseObject, error) {
	page, err := s.loadPage(ctx, request.PageId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateIncidentdefaultJSONResponse{Body: errorBody("page not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.CreateIncidentdefaultJSONResponse{Body: errorBody("incident is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	body := request.Body.Incident
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return generated.CreateIncidentdefaultJSONResponse{Body: errorBody("name is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	status := generated.Investigating
	if body.Status != nil {
		status = *body.Status
	}
	ids := componentIDSet(body.ComponentIds, body.Components)
	components, err := s.loadComponentsByID(ctx, request.PageId, ids)
	if errors.Is(err, errUnknownComponent) {
		return generated.CreateIncidentdefaultJSONResponse{Body: errorBody(err.Error()), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	affected, statuses := applyComponentStatuses(components, body.Components)
	override := ""
	if body.ImpactOverride != nil {
		override = string(*body.ImpactOverride)
	}
	impact := override
	if impact == "" {
		impact = calculatedImpact(statuses)
	}
	resolvedAt, monitoringAt := "", ""
	if status == generated.Resolved {
		resolvedAt = now
	}
	if status == generated.Monitoring {
		monitoringAt = now
	}
	incidentID, err := s.ids.Next(ctx, "inc")
	if err != nil {
		return nil, fmt.Errorf("statuspage: allocate incident id: %w", err)
	}
	updateID, err := s.ids.Next(ctx, "upd")
	if err != nil {
		return nil, fmt.Errorf("statuspage: allocate incident update id: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := writeComponentStatuses(ctx, tx, request.PageId, components, body.Components, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO incidents
		(id, page_id, name, status, impact, impact_override, created_at, updated_at, resolved_at, monitoring_at, postmortem_body)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		incidentID, request.PageId, name, string(status), impact, override, now, now, resolvedAt, monitoringAt); err != nil {
		return nil, err
	}
	for _, componentID := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO incident_components(incident_id, component_id) VALUES(?, ?)", incidentID, componentID); err != nil {
			return nil, err
		}
	}
	if err := insertUpdate(ctx, tx, updateID, incidentID, string(status), value(body.Body), now, boolValueDefault(body.DeliverNotifications, true), boolValue(body.WantsTwitterUpdate), affected); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pages SET updated_at=? WHERE id=?", now, request.PageId); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	incident, err := s.loadIncident(ctx, page.ID, incidentID)
	if err != nil {
		return nil, err
	}
	return generated.CreateIncident201JSONResponse(incident), nil
}

func (s *server) UpdateIncident(ctx context.Context, request generated.UpdateIncidentRequestObject) (generated.UpdateIncidentResponseObject, error) {
	row, err := s.loadIncidentRow(ctx, request.PageId, request.IncidentId)
	if status, message, ok := notFound(err); ok {
		return generated.UpdateIncidentdefaultJSONResponse{Body: errorBody(message), StatusCode: status}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.UpdateIncidentdefaultJSONResponse{Body: errorBody("incident is required"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	body := request.Body.Incident
	if body.Name != nil {
		row.Name = strings.TrimSpace(*body.Name)
		if row.Name == "" {
			return generated.UpdateIncidentdefaultJSONResponse{Body: errorBody("name is required"), StatusCode: http.StatusUnprocessableEntity}, nil
		}
	}
	statusChanged := false
	if body.Status != nil && string(*body.Status) != row.Status {
		row.Status = string(*body.Status)
		statusChanged = true
	}
	if body.ImpactOverride != nil {
		row.ImpactOverride = string(*body.ImpactOverride)
	}
	existingIDs, err := s.incidentComponentIDs(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	ids := existingIDs
	if body.ComponentIds != nil || body.Components != nil {
		ids = componentIDSet(body.ComponentIds, body.Components)
		if body.ComponentIds == nil {
			ids = componentIDSet(&existingIDs, body.Components)
		}
	}
	components, err := s.loadComponentsByID(ctx, request.PageId, ids)
	if errors.Is(err, errUnknownComponent) {
		return generated.UpdateIncidentdefaultJSONResponse{Body: errorBody(err.Error()), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	affected, statuses := applyComponentStatuses(components, body.Components)
	if row.ImpactOverride != "" {
		row.Impact = row.ImpactOverride
	} else {
		row.Impact = calculatedImpact(statuses)
	}
	if row.Status == string(generated.Resolved) {
		if row.ResolvedAt == "" {
			row.ResolvedAt = now
		}
	} else {
		row.ResolvedAt = ""
	}
	if row.Status == string(generated.Monitoring) && row.MonitoringAt == "" {
		row.MonitoringAt = now
	}
	row.UpdatedAt = now
	recordUpdate := body.Body != nil || statusChanged || body.Components != nil || body.ComponentIds != nil
	var updateID string
	if recordUpdate {
		updateID, err = s.ids.Next(ctx, "upd")
		if err != nil {
			return nil, fmt.Errorf("statuspage: allocate incident update id: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := writeComponentStatuses(ctx, tx, request.PageId, components, body.Components, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE incidents
		SET name=?, status=?, impact=?, impact_override=?, updated_at=?, resolved_at=?, monitoring_at=?
		WHERE id=? AND page_id=?`,
		row.Name, row.Status, row.Impact, row.ImpactOverride, row.UpdatedAt, row.ResolvedAt, row.MonitoringAt, row.ID, row.PageID); err != nil {
		return nil, err
	}
	if body.ComponentIds != nil || body.Components != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM incident_components WHERE incident_id=?", row.ID); err != nil {
			return nil, err
		}
		for _, componentID := range ids {
			if _, err := tx.ExecContext(ctx, "INSERT INTO incident_components(incident_id, component_id) VALUES(?, ?)", row.ID, componentID); err != nil {
				return nil, err
			}
		}
	}
	if recordUpdate {
		updateStatus := row.Status
		updateBody := ""
		if body.Body != nil {
			updateBody = *body.Body
		}
		if err := insertUpdate(ctx, tx, updateID, row.ID, updateStatus, updateBody, now, boolValueDefault(body.DeliverNotifications, true), boolValue(body.WantsTwitterUpdate), affected); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pages SET updated_at=? WHERE id=?", now, request.PageId); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	incident, err := s.loadIncident(ctx, request.PageId, row.ID)
	if err != nil {
		return nil, err
	}
	return generated.UpdateIncident200JSONResponse(incident), nil
}

type pageRow struct {
	ID, Name, Subdomain, Domain, URL, Description, TimeZone, CreatedAt, UpdatedAt string
}

func (p pageRow) wire() generated.Page {
	return generated.Page{
		Id: p.ID, Name: p.Name, Subdomain: p.Subdomain, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Domain: strPtr(p.Domain), Url: strPtr(p.URL), PageDescription: strPtr(p.Description), TimeZone: strPtr(p.TimeZone),
	}
}

func (p pageRow) shortlink(id string) string {
	base := strings.TrimRight(p.URL, "/")
	if base == "" {
		base = "https://" + p.Subdomain
	}
	return base + "/incidents/" + id
}

type componentRow struct {
	ID, PageID, Name, Description, Status, CreatedAt, UpdatedAt string
	Position                                                    int
	Showcase, Group, OnlyShowIfDegraded                         bool
}

func (c componentRow) wire() generated.Component {
	return generated.Component{
		Id: c.ID, PageId: c.PageID, Name: c.Name, Description: c.Description, Status: generated.ComponentStatus(c.Status),
		Position: c.Position, Showcase: c.Showcase, Group: c.Group, OnlyShowIfDegraded: c.OnlyShowIfDegraded,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

type incidentRow struct {
	ID, PageID, Name, Status, Impact, ImpactOverride, CreatedAt, UpdatedAt, ResolvedAt, MonitoringAt, PostmortemBody string
}

const pageSelect = `SELECT id, name, subdomain, domain, url, page_description, time_zone, created_at, updated_at FROM pages`
const componentSelect = `SELECT id, page_id, name, description, status, position, showcase, is_group, only_show_if_degraded, created_at, updated_at FROM components`
const incidentSelect = `SELECT id, page_id, name, status, impact, impact_override, created_at, updated_at, resolved_at, monitoring_at, postmortem_body FROM incidents`

func (s *server) loadPage(ctx context.Context, id string) (pageRow, error) {
	return scanPage(s.db.QueryRowContext(ctx, pageSelect+" WHERE id=?", id))
}

func (s *server) loadComponents(ctx context.Context, pageID string) ([]componentRow, error) {
	rows, err := s.db.QueryContext(ctx, componentSelect+" WHERE page_id=? ORDER BY position, id", pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []componentRow{}
	for rows.Next() {
		component, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, component)
	}
	return out, rows.Err()
}

func (s *server) loadComponent(ctx context.Context, pageID, id string) (componentRow, error) {
	if _, err := s.loadPage(ctx, pageID); errors.Is(err, sql.ErrNoRows) {
		return componentRow{}, errPageNotFound
	} else if err != nil {
		return componentRow{}, err
	}
	component, err := scanComponent(s.db.QueryRowContext(ctx, componentSelect+" WHERE id=? AND page_id=?", id, pageID))
	if errors.Is(err, sql.ErrNoRows) {
		return componentRow{}, errComponentNotFound
	}
	return component, err
}

func (s *server) loadComponentsByID(ctx context.Context, pageID string, ids []string) (map[string]componentRow, error) {
	found := map[string]componentRow{}
	for _, id := range ids {
		component, err := s.loadComponent(ctx, pageID, id)
		if errors.Is(err, errComponentNotFound) || errors.Is(err, errPageNotFound) {
			return nil, fmt.Errorf("%w %s", errUnknownComponent, id)
		}
		if err != nil {
			return nil, err
		}
		found[id] = component
	}
	return found, nil
}

func (s *server) listIncidents(ctx context.Context, pageID string, unresolved bool, query string) ([]generated.Incident, error) {
	page, err := s.loadPage(ctx, pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPageNotFound
	}
	if err != nil {
		return nil, err
	}
	sqlText := incidentSelect + " WHERE page_id=?"
	if unresolved {
		sqlText += " AND status NOT IN ('resolved', 'completed', 'postmortem')"
	}
	sqlText += " ORDER BY created_at DESC, id"
	rows, err := s.db.QueryContext(ctx, sqlText, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []incidentRow
	for rows.Next() {
		row, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	components, err := s.loadComponents(ctx, pageID)
	if err != nil {
		return nil, err
	}
	byID := map[string]componentRow{}
	for _, component := range components {
		byID[component.ID] = component
	}
	out := []generated.Incident{}
	for _, row := range records {
		incident, err := s.hydrateIncident(ctx, page, row, byID)
		if err != nil {
			return nil, err
		}
		if incidentMatches(incident, query) {
			out = append(out, incident)
		}
	}
	return out, nil
}

func (s *server) loadIncident(ctx context.Context, pageID, id string) (generated.Incident, error) {
	page, err := s.loadPage(ctx, pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Incident{}, errPageNotFound
	}
	if err != nil {
		return generated.Incident{}, err
	}
	row, err := s.loadIncidentRow(ctx, pageID, id)
	if err != nil {
		return generated.Incident{}, err
	}
	components, err := s.loadComponents(ctx, pageID)
	if err != nil {
		return generated.Incident{}, err
	}
	byID := map[string]componentRow{}
	for _, component := range components {
		byID[component.ID] = component
	}
	return s.hydrateIncident(ctx, page, row, byID)
}

func (s *server) loadIncidentRow(ctx context.Context, pageID, id string) (incidentRow, error) {
	if _, err := s.loadPage(ctx, pageID); errors.Is(err, sql.ErrNoRows) {
		return incidentRow{}, errPageNotFound
	} else if err != nil {
		return incidentRow{}, err
	}
	row, err := scanIncident(s.db.QueryRowContext(ctx, incidentSelect+" WHERE id=? AND page_id=?", id, pageID))
	if errors.Is(err, sql.ErrNoRows) {
		return incidentRow{}, errIncidentNotFound
	}
	return row, err
}

func (s *server) hydrateIncident(ctx context.Context, page pageRow, row incidentRow, components map[string]componentRow) (generated.Incident, error) {
	ids, err := s.incidentComponentIDs(ctx, row.ID)
	if err != nil {
		return generated.Incident{}, err
	}
	linked := []generated.Component{}
	for _, id := range ids {
		if component, ok := components[id]; ok {
			linked = append(linked, component.wire())
		}
	}
	updates, err := s.loadUpdates(ctx, row.ID, components)
	if err != nil {
		return generated.Incident{}, err
	}
	incident := generated.Incident{
		Id: row.ID, PageId: row.PageID, Name: row.Name, Status: generated.IncidentStatus(row.Status),
		Impact: generated.IncidentImpact(row.Impact), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Shortlink: page.shortlink(row.ID), Components: linked, IncidentUpdates: updates,
	}
	if row.ImpactOverride != "" {
		override := generated.IncidentImpact(row.ImpactOverride)
		incident.ImpactOverride = &override
	}
	if row.ResolvedAt != "" {
		incident.ResolvedAt = &row.ResolvedAt
	}
	if row.MonitoringAt != "" {
		incident.MonitoringAt = &row.MonitoringAt
	}
	if row.PostmortemBody != "" {
		incident.PostmortemBody = &row.PostmortemBody
	}
	return incident, nil
}

func (s *server) incidentComponentIDs(ctx context.Context, incidentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT component_id FROM incident_components WHERE incident_id=? ORDER BY component_id", incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *server) loadUpdates(ctx context.Context, incidentID string, components map[string]componentRow) ([]generated.IncidentUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, status, body, created_at, updated_at, display_at, deliver_notifications, wants_twitter_update, affected_json
		FROM incident_updates WHERE incident_id=? ORDER BY created_at DESC, id DESC`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []generated.IncidentUpdate{}
	for rows.Next() {
		var update generated.IncidentUpdate
		var status, affected string
		var deliver, twitter int
		if err := rows.Scan(&update.Id, &status, &update.Body, &update.CreatedAt, &update.UpdatedAt, &update.DisplayAt, &deliver, &twitter, &affected); err != nil {
			return nil, err
		}
		update.IncidentId = incidentID
		update.Status = generated.IncidentStatus(status)
		update.DeliverNotifications = deliver != 0
		update.WantsTwitterUpdate = twitter != 0
		var stored []fixtureAffected
		if err := json.Unmarshal([]byte(affected), &stored); err != nil {
			return nil, fmt.Errorf("statuspage: decode affected components for %s: %w", update.Id, err)
		}
		update.AffectedComponents = []generated.AffectedComponent{}
		for _, item := range stored {
			name := item.ComponentID
			if component, ok := components[item.ComponentID]; ok {
				name = component.Name
			}
			update.AffectedComponents = append(update.AffectedComponents, generated.AffectedComponent{
				Code: item.ComponentID, Name: name, OldStatus: generated.ComponentStatus(item.OldStatus), NewStatus: generated.ComponentStatus(item.NewStatus),
			})
		}
		out = append(out, update)
	}
	return out, rows.Err()
}

func (s *server) touchPage(ctx context.Context, pageID, now string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE pages SET updated_at=? WHERE id=?", now, pageID)
	return err
}

func (s *server) now() string { return s.clock.Now().UTC().Format(time.RFC3339) }

func applyComponentStatuses(components map[string]componentRow, changes *map[string]generated.ComponentStatus) ([]fixtureAffected, []string) {
	ids := make([]string, 0, len(components))
	for id := range components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	affected := []fixtureAffected{}
	statuses := make([]string, 0, len(ids))
	for _, id := range ids {
		component := components[id]
		old := component.Status
		next := old
		if changes != nil {
			if status, ok := (*changes)[id]; ok {
				next = string(status)
			}
		}
		statuses = append(statuses, next)
		affected = append(affected, fixtureAffected{ComponentID: id, OldStatus: old, NewStatus: next})
	}
	return affected, statuses
}

func writeComponentStatuses(ctx context.Context, tx *sql.Tx, pageID string, components map[string]componentRow, changes *map[string]generated.ComponentStatus, now string) error {
	if changes == nil {
		return nil
	}
	for id, status := range *changes {
		component, ok := components[id]
		if !ok {
			return fmt.Errorf("unknown component %s", id)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE components SET status=?, updated_at=? WHERE id=? AND page_id=?", string(status), now, component.ID, pageID); err != nil {
			return err
		}
	}
	return nil
}

func insertUpdate(ctx context.Context, tx *sql.Tx, id, incidentID, status, body, now string, deliver, twitter bool, affected []fixtureAffected) error {
	if affected == nil {
		affected = []fixtureAffected{}
	}
	raw, err := json.Marshal(affected)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO incident_updates
		(id, incident_id, status, body, created_at, updated_at, display_at, deliver_notifications, wants_twitter_update, affected_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, incidentID, status, body, now, now, now, boolInt(deliver), boolInt(twitter), string(raw))
	return err
}

func componentIDSet(ids *[]string, statuses *map[string]generated.ComponentStatus) []string {
	set := map[string]struct{}{}
	if ids != nil {
		for _, id := range *ids {
			set[id] = struct{}{}
		}
	}
	if statuses != nil {
		for id := range *statuses {
			set[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func calculatedImpact(statuses []string) string {
	best := 0
	for _, status := range statuses {
		rank := 0
		switch status {
		case "degraded_performance":
			rank = 1
		case "partial_outage":
			rank = 2
		case "major_outage":
			rank = 3
		}
		if rank > best {
			best = rank
		}
	}
	switch best {
	case 3:
		return "critical"
	case 2:
		return "major"
	case 1:
		return "minor"
	default:
		return "none"
	}
}

func incidentMatches(incident generated.Incident, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(incident.Name), query) || strings.Contains(strings.ToLower(string(incident.Status)), query) {
		return true
	}
	if incident.PostmortemBody != nil && strings.Contains(strings.ToLower(*incident.PostmortemBody), query) {
		return true
	}
	for _, update := range incident.IncidentUpdates {
		if strings.Contains(strings.ToLower(update.Body), query) || strings.Contains(strings.ToLower(string(update.Status)), query) {
			return true
		}
	}
	return false
}

func window[P ~int, L ~int](page *P, limit *L, fallback, n int) (int, int) {
	number := 1
	if page != nil && *page > 0 {
		number = int(*page)
	}
	size := fallback
	if limit != nil && *limit > 0 {
		size = int(*limit)
	}
	start := (number - 1) * size
	if start > n {
		start = n
	}
	end := start + size
	if end > n {
		end = n
	}
	return start, end
}

var (
	errPageNotFound      = errors.New("page not found")
	errComponentNotFound = errors.New("component not found")
	errIncidentNotFound  = errors.New("incident not found")
	errUnknownComponent  = errors.New("unknown component")
)

func notFound(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errPageNotFound):
		return http.StatusNotFound, "page not found", true
	case errors.Is(err, errComponentNotFound):
		return http.StatusNotFound, "component not found", true
	case errors.Is(err, errIncidentNotFound):
		return http.StatusNotFound, "incident not found", true
	default:
		return 0, "", false
	}
}

func scanPage(row interface{ Scan(...any) error }) (pageRow, error) {
	var page pageRow
	err := row.Scan(&page.ID, &page.Name, &page.Subdomain, &page.Domain, &page.URL, &page.Description, &page.TimeZone, &page.CreatedAt, &page.UpdatedAt)
	return page, err
}

func scanComponent(row interface{ Scan(...any) error }) (componentRow, error) {
	var component componentRow
	var showcase, group, only int
	err := row.Scan(&component.ID, &component.PageID, &component.Name, &component.Description, &component.Status, &component.Position, &showcase, &group, &only, &component.CreatedAt, &component.UpdatedAt)
	component.Showcase = showcase != 0
	component.Group = group != 0
	component.OnlyShowIfDegraded = only != 0
	return component, err
}

func scanIncident(row interface{ Scan(...any) error }) (incidentRow, error) {
	var incident incidentRow
	err := row.Scan(&incident.ID, &incident.PageID, &incident.Name, &incident.Status, &incident.Impact, &incident.ImpactOverride, &incident.CreatedAt, &incident.UpdatedAt, &incident.ResolvedAt, &incident.MonitoringAt, &incident.PostmortemBody)
	return incident, err
}

func errorBody(message string) generated.ErrorBody { return generated.ErrorBody{Error: message} }

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody(message))
}

func strPtr(value string) *string { return &value }

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func boolValue(pointer *bool) bool { return pointer != nil && *pointer }

func boolValueDefault(pointer *bool, fallback bool) bool {
	if pointer == nil {
		return fallback
	}
	return *pointer
}
