package ga4

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
		panic(fmt.Sprintf("ga4: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("ga4-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("ga4: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("ga4-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("ga4: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Properties      []fixtureProperty       `json:"properties"`
	Events          []fixtureEvent          `json:"events"`
	AudienceExports []fixtureAudienceExport `json:"audienceExports"`
}

type fixtureProperty struct {
	PropertyID   string `json:"propertyId"`
	DisplayName  string `json:"displayName"`
	CurrencyCode string `json:"currencyCode"`
	TimeZone     string `json:"timeZone"`
}

type fixtureEvent struct {
	PropertyID          string `json:"propertyId"`
	Date                string `json:"date"`
	EventName           string `json:"eventName"`
	TransactionID       string `json:"transactionId"`
	SessionSource       string `json:"sessionSource"`
	SessionMedium       string `json:"sessionMedium"`
	SessionCampaignName string `json:"sessionCampaignName"`
	EventCount          int    `json:"eventCount"`
	Transactions        int    `json:"transactions"`
	PurchaseRevenue     int    `json:"purchaseRevenue"`
	ItemRefundAmount    int    `json:"itemRefundAmount"`
}

type fixtureAudienceExport struct {
	Name                       string   `json:"name"`
	PropertyID                 string   `json:"propertyId"`
	Audience                   string   `json:"audience"`
	AudienceDisplayName        string   `json:"audienceDisplayName"`
	Dimensions                 []string `json:"dimensions"`
	State                      string   `json:"state"`
	RowCount                   int      `json:"rowCount"`
	PercentageCompleted        float64  `json:"percentageCompleted"`
	BeginCreatingTime          string   `json:"beginCreatingTime"`
	CreationQuotaTokensCharged int      `json:"creationQuotaTokensCharged"`
	ErrorMessage               string   `json:"errorMessage"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "ga4" || doc.ResourceVersion != "v1beta" {
		return fmt.Errorf("ga4 scenario: expected resource ga4 v1beta, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("ga4 scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("ga4 scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	properties := map[string]fixtureProperty{}
	for i, property := range state.Properties {
		if _, err := time.LoadLocation(property.TimeZone); err != nil {
			return fmt.Errorf("ga4 scenario: properties[%d].timeZone: %w", i, err)
		}
		if _, exists := properties[property.PropertyID]; exists {
			return fmt.Errorf("ga4 scenario: duplicate property id %q", property.PropertyID)
		}
		properties[property.PropertyID] = property
	}
	events := map[string]struct{}{}
	for i, event := range state.Events {
		if _, exists := properties[event.PropertyID]; !exists {
			return fmt.Errorf("ga4 scenario: events[%d] references unknown property %q", i, event.PropertyID)
		}
		if _, err := time.Parse("2006-01-02", event.Date); err != nil {
			return fmt.Errorf("ga4 scenario: events[%d].date: %w", i, err)
		}
		key := event.PropertyID + "\x00" + event.TransactionID + "\x00" + event.EventName
		if _, exists := events[key]; exists {
			return fmt.Errorf("ga4 scenario: duplicate event %s %s on property %s", event.EventName, event.TransactionID, event.PropertyID)
		}
		events[key] = struct{}{}
	}
	exports := map[string]struct{}{}
	for i, export := range state.AudienceExports {
		if _, exists := properties[export.PropertyID]; !exists {
			return fmt.Errorf("ga4 scenario: audienceExports[%d] references unknown property %q", i, export.PropertyID)
		}
		wantPrefix := "properties/" + export.PropertyID + "/audienceExports/"
		if !strings.HasPrefix(export.Name, wantPrefix) || strings.Contains(strings.TrimPrefix(export.Name, wantPrefix), "/") {
			return fmt.Errorf("ga4 scenario: audienceExports[%d].name must be properties/%s/audienceExports/{id}", i, export.PropertyID)
		}
		if !strings.HasPrefix(export.Audience, "properties/"+export.PropertyID+"/audiences/") {
			return fmt.Errorf("ga4 scenario: audienceExports[%d].audience must belong to property %s", i, export.PropertyID)
		}
		if _, err := time.Parse(time.RFC3339, export.BeginCreatingTime); err != nil {
			return fmt.Errorf("ga4 scenario: audienceExports[%d].beginCreatingTime: %w", i, err)
		}
		if _, exists := exports[export.Name]; exists {
			return fmt.Errorf("ga4 scenario: duplicate audience export %q", export.Name)
		}
		exports[export.Name] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("ga4 scenario: initialize: %w", err)
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
		return fmt.Errorf("ga4 scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"audience_exports", "events", "properties"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("ga4 scenario: clear %s: %w", table, err)
		}
	}
	for _, property := range state.Properties {
		if _, err := tx.ExecContext(ctx, `INSERT INTO properties(property_id, display_name, currency_code, time_zone)
			VALUES(?, ?, ?, ?)`, property.PropertyID, property.DisplayName, property.CurrencyCode, property.TimeZone); err != nil {
			return fmt.Errorf("ga4 scenario: insert property %s: %w", property.PropertyID, err)
		}
	}
	for _, event := range state.Events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(
			property_id, event_date, event_name, transaction_id, session_source, session_medium,
			session_campaign_name, event_count, transactions, purchase_revenue, item_refund_amount)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.PropertyID, event.Date, event.EventName, event.TransactionID, event.SessionSource,
			event.SessionMedium, event.SessionCampaignName, event.EventCount, event.Transactions,
			event.PurchaseRevenue, event.ItemRefundAmount); err != nil {
			return fmt.Errorf("ga4 scenario: insert event %s: %w", event.TransactionID, err)
		}
	}
	for _, export := range state.AudienceExports {
		dimensions, err := json.Marshal(export.Dimensions)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audience_exports(
			name, property_id, audience, audience_display_name, dimensions, state, row_count,
			percentage_completed, begin_creating_time, creation_quota_tokens_charged, error_message)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			export.Name, export.PropertyID, export.Audience, export.AudienceDisplayName, string(dimensions),
			export.State, export.RowCount, export.PercentageCompleted, export.BeginCreatingTime,
			export.CreationQuotaTokensCharged, export.ErrorMessage); err != nil {
			return fmt.Errorf("ga4 scenario: insert audience export %s: %w", export.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ga4 scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	properties, err := db.QueryContext(ctx, `SELECT property_id, display_name, currency_code, time_zone
		FROM properties ORDER BY property_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ga4 scenario: dump properties: %w", err)
	}
	for properties.Next() {
		var property fixtureProperty
		if err := properties.Scan(&property.PropertyID, &property.DisplayName, &property.CurrencyCode, &property.TimeZone); err != nil {
			properties.Close()
			return scenario.Document{}, err
		}
		state.Properties = append(state.Properties, property)
	}
	if err := properties.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := properties.Err(); err != nil {
		return scenario.Document{}, err
	}
	events, err := db.QueryContext(ctx, `SELECT property_id, event_date, event_name, transaction_id, session_source,
		session_medium, session_campaign_name, event_count, transactions, purchase_revenue, item_refund_amount
		FROM events ORDER BY property_id, event_date, transaction_id, event_name`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ga4 scenario: dump events: %w", err)
	}
	for events.Next() {
		var event fixtureEvent
		if err := events.Scan(&event.PropertyID, &event.Date, &event.EventName, &event.TransactionID,
			&event.SessionSource, &event.SessionMedium, &event.SessionCampaignName, &event.EventCount,
			&event.Transactions, &event.PurchaseRevenue, &event.ItemRefundAmount); err != nil {
			events.Close()
			return scenario.Document{}, err
		}
		state.Events = append(state.Events, event)
	}
	if err := events.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := events.Err(); err != nil {
		return scenario.Document{}, err
	}
	exports, err := db.QueryContext(ctx, `SELECT name, property_id, audience, audience_display_name, dimensions, state,
		row_count, percentage_completed, begin_creating_time, creation_quota_tokens_charged, error_message
		FROM audience_exports ORDER BY name`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("ga4 scenario: dump audience exports: %w", err)
	}
	for exports.Next() {
		var export fixtureAudienceExport
		var dimensions string
		if err := exports.Scan(&export.Name, &export.PropertyID, &export.Audience, &export.AudienceDisplayName,
			&dimensions, &export.State, &export.RowCount, &export.PercentageCompleted, &export.BeginCreatingTime,
			&export.CreationQuotaTokensCharged, &export.ErrorMessage); err != nil {
			exports.Close()
			return scenario.Document{}, err
		}
		if err := json.Unmarshal([]byte(dimensions), &export.Dimensions); err != nil {
			exports.Close()
			return scenario.Document{}, fmt.Errorf("ga4 scenario: dump audience export %s dimensions: %w", export.Name, err)
		}
		if export.Dimensions == nil {
			export.Dimensions = []string{}
		}
		state.AudienceExports = append(state.AudienceExports, export)
	}
	if err := exports.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := exports.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Properties == nil {
		state.Properties = []fixtureProperty{}
	}
	if state.Events == nil {
		state.Events = []fixtureEvent{}
	}
	if state.AudienceExports == nil {
		state.AudienceExports = []fixtureAudienceExport{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "ga4", ResourceVersion: "v1beta", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("ga4 scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("ga4 scenario: state has trailing data")
	}
	if state.Properties == nil || state.Events == nil || state.AudienceExports == nil {
		return fixtureState{}, fmt.Errorf("ga4 scenario: properties, events, and audienceExports are required arrays")
	}
	for i := range state.AudienceExports {
		if state.AudienceExports[i].Dimensions == nil {
			return fixtureState{}, fmt.Errorf("ga4 scenario: audienceExports[%d].dimensions is required", i)
		}
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }
