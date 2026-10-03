package statuspage

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("statuspage: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("statuspage-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("statuspage: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("statuspage-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("statuspage: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Page       fixturePage        `json:"page"`
	Components []fixtureComponent `json:"components"`
	Incidents  []fixtureIncident  `json:"incidents"`
}

type fixturePage struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Subdomain       string `json:"subdomain"`
	Domain          string `json:"domain"`
	URL             string `json:"url"`
	PageDescription string `json:"pageDescription"`
	TimeZone        string `json:"timeZone"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type fixtureComponent struct {
	ID                 string `json:"id"`
	PageID             string `json:"pageId"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Status             string `json:"status"`
	Position           int    `json:"position"`
	Showcase           bool   `json:"showcase"`
	Group              bool   `json:"group"`
	OnlyShowIfDegraded bool   `json:"onlyShowIfDegraded"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

type fixtureIncident struct {
	ID             string          `json:"id"`
	PageID         string          `json:"pageId"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Impact         string          `json:"impact"`
	ImpactOverride string          `json:"impactOverride"`
	ComponentIDs   []string        `json:"componentIds"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
	ResolvedAt     string          `json:"resolvedAt"`
	MonitoringAt   string          `json:"monitoringAt"`
	PostmortemBody string          `json:"postmortemBody"`
	Updates        []fixtureUpdate `json:"updates"`
}

type fixtureUpdate struct {
	ID                   string            `json:"id"`
	Status               string            `json:"status"`
	Body                 string            `json:"body"`
	CreatedAt            string            `json:"createdAt"`
	UpdatedAt            string            `json:"updatedAt"`
	DisplayAt            string            `json:"displayAt"`
	DeliverNotifications bool              `json:"deliverNotifications"`
	WantsTwitterUpdate   bool              `json:"wantsTwitterUpdate"`
	Affected             []fixtureAffected `json:"affected"`
}

type fixtureAffected struct {
	ComponentID string `json:"componentId"`
	OldStatus   string `json:"oldStatus"`
	NewStatus   string `json:"newStatus"`
}

var componentStatuses = map[string]struct{}{
	"operational": {}, "degraded_performance": {}, "partial_outage": {}, "major_outage": {}, "under_maintenance": {},
}

var incidentStatuses = map[string]struct{}{
	"investigating": {}, "identified": {}, "monitoring": {}, "resolved": {},
	"scheduled": {}, "in_progress": {}, "verifying": {}, "completed": {}, "postmortem": {},
}

var impacts = map[string]struct{}{"none": {}, "minor": {}, "major": {}, "critical": {}}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "statuspage" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("statuspage scenario: expected resource statuspage v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("statuspage scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("statuspage scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if err := validateTimestamp("page.createdAt", state.Page.CreatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("page.updatedAt", state.Page.UpdatedAt); err != nil {
		return err
	}
	components := map[string]fixtureComponent{}
	for i, component := range state.Components {
		if component.PageID != state.Page.ID {
			return fmt.Errorf("statuspage scenario: components[%d] references unknown page %q", i, component.PageID)
		}
		if _, ok := componentStatuses[component.Status]; !ok {
			return fmt.Errorf("statuspage scenario: components[%d] has unknown status %q", i, component.Status)
		}
		if _, exists := components[component.ID]; exists {
			return fmt.Errorf("statuspage scenario: duplicate component id %q", component.ID)
		}
		if err := validateTimestamp(fmt.Sprintf("components[%d].createdAt", i), component.CreatedAt); err != nil {
			return err
		}
		if err := validateTimestamp(fmt.Sprintf("components[%d].updatedAt", i), component.UpdatedAt); err != nil {
			return err
		}
		components[component.ID] = component
	}
	incidents := map[string]struct{}{}
	updates := map[string]struct{}{}
	for i, incident := range state.Incidents {
		if incident.PageID != state.Page.ID {
			return fmt.Errorf("statuspage scenario: incidents[%d] references unknown page %q", i, incident.PageID)
		}
		if _, ok := incidentStatuses[incident.Status]; !ok {
			return fmt.Errorf("statuspage scenario: incidents[%d] has unknown status %q", i, incident.Status)
		}
		if _, ok := impacts[incident.Impact]; !ok {
			return fmt.Errorf("statuspage scenario: incidents[%d] has unknown impact %q", i, incident.Impact)
		}
		if incident.ImpactOverride != "" {
			if _, ok := impacts[incident.ImpactOverride]; !ok {
				return fmt.Errorf("statuspage scenario: incidents[%d] has unknown impactOverride %q", i, incident.ImpactOverride)
			}
		}
		if _, exists := incidents[incident.ID]; exists {
			return fmt.Errorf("statuspage scenario: duplicate incident id %q", incident.ID)
		}
		incidents[incident.ID] = struct{}{}
		seenComponents := map[string]struct{}{}
		for _, componentID := range incident.ComponentIDs {
			if _, ok := components[componentID]; !ok {
				return fmt.Errorf("statuspage scenario: incidents[%d] references unknown component %q", i, componentID)
			}
			if _, exists := seenComponents[componentID]; exists {
				return fmt.Errorf("statuspage scenario: incidents[%d] repeats component %q", i, componentID)
			}
			seenComponents[componentID] = struct{}{}
		}
		for _, field := range []struct{ name, value string }{
			{"createdAt", incident.CreatedAt},
			{"updatedAt", incident.UpdatedAt},
		} {
			if err := validateTimestamp(fmt.Sprintf("incidents[%d].%s", i, field.name), field.value); err != nil {
				return err
			}
		}
		for _, field := range []struct{ name, value string }{
			{"resolvedAt", incident.ResolvedAt},
			{"monitoringAt", incident.MonitoringAt},
		} {
			if field.value == "" {
				continue
			}
			if err := validateTimestamp(fmt.Sprintf("incidents[%d].%s", i, field.name), field.value); err != nil {
				return err
			}
		}
		for j, update := range incident.Updates {
			if _, ok := incidentStatuses[update.Status]; !ok {
				return fmt.Errorf("statuspage scenario: incidents[%d].updates[%d] has unknown status %q", i, j, update.Status)
			}
			if _, exists := updates[update.ID]; exists {
				return fmt.Errorf("statuspage scenario: duplicate incident update id %q", update.ID)
			}
			updates[update.ID] = struct{}{}
			for _, field := range []struct{ name, value string }{
				{"createdAt", update.CreatedAt},
				{"updatedAt", update.UpdatedAt},
				{"displayAt", update.DisplayAt},
			} {
				if err := validateTimestamp(fmt.Sprintf("incidents[%d].updates[%d].%s", i, j, field.name), field.value); err != nil {
					return err
				}
			}
			for k, affected := range update.Affected {
				if _, ok := components[affected.ComponentID]; !ok {
					return fmt.Errorf("statuspage scenario: incidents[%d].updates[%d].affected[%d] references unknown component %q", i, j, k, affected.ComponentID)
				}
				if _, ok := componentStatuses[affected.OldStatus]; !ok {
					return fmt.Errorf("statuspage scenario: incidents[%d].updates[%d].affected[%d] has unknown oldStatus %q", i, j, k, affected.OldStatus)
				}
				if _, ok := componentStatuses[affected.NewStatus]; !ok {
					return fmt.Errorf("statuspage scenario: incidents[%d].updates[%d].affected[%d] has unknown newStatus %q", i, j, k, affected.NewStatus)
				}
			}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("statuspage scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("statuspage scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"incident_updates", "incident_components", "incidents", "components", "pages"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("statuspage scenario: clear %s: %w", table, err)
		}
	}
	page := state.Page
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages
		(id, name, subdomain, domain, url, page_description, time_zone, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		page.ID, page.Name, page.Subdomain, page.Domain, page.URL, page.PageDescription, page.TimeZone, page.CreatedAt, page.UpdatedAt); err != nil {
		return fmt.Errorf("statuspage scenario: insert page: %w", err)
	}
	for _, component := range state.Components {
		if _, err := tx.ExecContext(ctx, `INSERT INTO components
			(id, page_id, name, description, status, position, showcase, is_group, only_show_if_degraded, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			component.ID, component.PageID, component.Name, component.Description, component.Status, component.Position,
			boolInt(component.Showcase), boolInt(component.Group), boolInt(component.OnlyShowIfDegraded), component.CreatedAt, component.UpdatedAt); err != nil {
			return fmt.Errorf("statuspage scenario: insert component %s: %w", component.ID, err)
		}
	}
	for _, incident := range state.Incidents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incidents
			(id, page_id, name, status, impact, impact_override, created_at, updated_at, resolved_at, monitoring_at, postmortem_body)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			incident.ID, incident.PageID, incident.Name, incident.Status, incident.Impact, incident.ImpactOverride,
			incident.CreatedAt, incident.UpdatedAt, incident.ResolvedAt, incident.MonitoringAt, incident.PostmortemBody); err != nil {
			return fmt.Errorf("statuspage scenario: insert incident %s: %w", incident.ID, err)
		}
		for _, componentID := range incident.ComponentIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO incident_components(incident_id, component_id) VALUES(?, ?)", incident.ID, componentID); err != nil {
				return fmt.Errorf("statuspage scenario: insert incident %s component %s: %w", incident.ID, componentID, err)
			}
		}
		for _, update := range incident.Updates {
			affected, err := json.Marshal(update.Affected)
			if err != nil {
				return fmt.Errorf("statuspage scenario: encode update %s: %w", update.ID, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO incident_updates
				(id, incident_id, status, body, created_at, updated_at, display_at, deliver_notifications, wants_twitter_update, affected_json)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				update.ID, incident.ID, update.Status, update.Body, update.CreatedAt, update.UpdatedAt, update.DisplayAt,
				boolInt(update.DeliverNotifications), boolInt(update.WantsTwitterUpdate), string(affected)); err != nil {
				return fmt.Errorf("statuspage scenario: insert update %s: %w", update.ID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("statuspage scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, `SELECT id, name, subdomain, domain, url, page_description, time_zone, created_at, updated_at FROM pages`).
		Scan(&state.Page.ID, &state.Page.Name, &state.Page.Subdomain, &state.Page.Domain, &state.Page.URL, &state.Page.PageDescription, &state.Page.TimeZone, &state.Page.CreatedAt, &state.Page.UpdatedAt); err != nil {
		return scenario.Document{}, fmt.Errorf("statuspage scenario: dump page: %w", err)
	}
	componentRows, err := db.QueryContext(ctx, `SELECT id, page_id, name, description, status, position, showcase, is_group, only_show_if_degraded, created_at, updated_at
		FROM components ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("statuspage scenario: dump components: %w", err)
	}
	for componentRows.Next() {
		var component fixtureComponent
		var showcase, group, only int
		if err := componentRows.Scan(&component.ID, &component.PageID, &component.Name, &component.Description, &component.Status, &component.Position, &showcase, &group, &only, &component.CreatedAt, &component.UpdatedAt); err != nil {
			componentRows.Close()
			return scenario.Document{}, err
		}
		component.Showcase = showcase != 0
		component.Group = group != 0
		component.OnlyShowIfDegraded = only != 0
		state.Components = append(state.Components, component)
	}
	if err := componentRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := componentRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	incidentRows, err := db.QueryContext(ctx, `SELECT id, page_id, name, status, impact, impact_override, created_at, updated_at, resolved_at, monitoring_at, postmortem_body
		FROM incidents ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("statuspage scenario: dump incidents: %w", err)
	}
	for incidentRows.Next() {
		var incident fixtureIncident
		if err := incidentRows.Scan(&incident.ID, &incident.PageID, &incident.Name, &incident.Status, &incident.Impact, &incident.ImpactOverride, &incident.CreatedAt, &incident.UpdatedAt, &incident.ResolvedAt, &incident.MonitoringAt, &incident.PostmortemBody); err != nil {
			incidentRows.Close()
			return scenario.Document{}, err
		}
		state.Incidents = append(state.Incidents, incident)
	}
	if err := incidentRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := incidentRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.Incidents {
		incident := &state.Incidents[i]
		componentRows, err := db.QueryContext(ctx, "SELECT component_id FROM incident_components WHERE incident_id=? ORDER BY component_id", incident.ID)
		if err != nil {
			return scenario.Document{}, err
		}
		for componentRows.Next() {
			var componentID string
			if err := componentRows.Scan(&componentID); err != nil {
				componentRows.Close()
				return scenario.Document{}, err
			}
			incident.ComponentIDs = append(incident.ComponentIDs, componentID)
		}
		if err := componentRows.Close(); err != nil {
			return scenario.Document{}, err
		}
		updateRows, err := db.QueryContext(ctx, `SELECT id, status, body, created_at, updated_at, display_at, deliver_notifications, wants_twitter_update, affected_json
			FROM incident_updates WHERE incident_id=? ORDER BY id`, incident.ID)
		if err != nil {
			return scenario.Document{}, err
		}
		for updateRows.Next() {
			var update fixtureUpdate
			var deliver, twitter int
			var affected string
			if err := updateRows.Scan(&update.ID, &update.Status, &update.Body, &update.CreatedAt, &update.UpdatedAt, &update.DisplayAt, &deliver, &twitter, &affected); err != nil {
				updateRows.Close()
				return scenario.Document{}, err
			}
			update.DeliverNotifications = deliver != 0
			update.WantsTwitterUpdate = twitter != 0
			if err := json.Unmarshal([]byte(affected), &update.Affected); err != nil {
				updateRows.Close()
				return scenario.Document{}, fmt.Errorf("statuspage scenario: dump update %s: %w", update.ID, err)
			}
			if update.Affected == nil {
				update.Affected = []fixtureAffected{}
			}
			incident.Updates = append(incident.Updates, update)
		}
		if err := updateRows.Close(); err != nil {
			return scenario.Document{}, err
		}
		if incident.ComponentIDs == nil {
			incident.ComponentIDs = []string{}
		}
		if incident.Updates == nil {
			incident.Updates = []fixtureUpdate{}
		}
	}
	if state.Components == nil {
		state.Components = []fixtureComponent{}
	}
	if state.Incidents == nil {
		state.Incidents = []fixtureIncident{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "statuspage", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("statuspage scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("statuspage scenario: state has trailing data")
	}
	if state.Components == nil || state.Incidents == nil {
		return fixtureState{}, fmt.Errorf("statuspage scenario: components and incidents are required arrays")
	}
	for i := range state.Incidents {
		if state.Incidents[i].ComponentIDs == nil || state.Incidents[i].Updates == nil {
			return fixtureState{}, fmt.Errorf("statuspage scenario: incidents[%d] componentIds and updates are required arrays", i)
		}
		for j := range state.Incidents[i].Updates {
			if state.Incidents[i].Updates[j].Affected == nil {
				return fixtureState{}, fmt.Errorf("statuspage scenario: incidents[%d].updates[%d].affected is required", i, j)
			}
		}
	}
	return state, nil
}

func validateTimestamp(name, value string) error {
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("statuspage scenario: %s: %w", name, err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }
