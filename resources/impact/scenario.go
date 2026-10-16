package impact

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
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
		panic(fmt.Sprintf("impact: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("impact-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("impact: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("impact-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("impact: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	AccountSID string           `json:"accountSid"`
	Programs   []fixtureProgram `json:"programs"`
	Partners   []fixturePartner `json:"partners"`
	Actions    []fixtureAction  `json:"actions"`
	Notes      []fixtureNote    `json:"notes"`
}

type fixtureProgram struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	Type             string `json:"type"`
	ShortDescription string `json:"shortDescription"`
}

type fixturePartner struct {
	ID          string `json:"id"`
	CampaignID  int    `json:"campaignId"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Currency    string `json:"currency"`
	Website     string `json:"website"`
	Description string `json:"description"`
	DateCreated string `json:"dateCreated"`
}

type fixtureAction struct {
	ID               string        `json:"id"`
	CampaignID       int           `json:"campaignId"`
	PartnerID        string        `json:"partnerId"`
	Oid              string        `json:"oid"`
	State            string        `json:"state"`
	PayoutPaise      int64         `json:"payoutPaise"`
	AmountPaise      int64         `json:"amountPaise"`
	Currency         string        `json:"currency"`
	EventDate        string        `json:"eventDate"`
	CreationDate     string        `json:"creationDate"`
	CustomerCity     string        `json:"customerCity"`
	CustomerRegion   string        `json:"customerRegion"`
	CustomerCountry  string        `json:"customerCountry"`
	CustomerPostCode string        `json:"customerPostCode"`
	Note             string        `json:"note"`
	Items            []fixtureItem `json:"items"`
}

type fixtureItem struct {
	SKU             string `json:"sku"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	SaleAmountPaise int64  `json:"saleAmountPaise"`
}

type fixtureNote struct {
	ID               string `json:"id"`
	CampaignID       int    `json:"campaignId"`
	PartnerID        string `json:"partnerId"`
	Creator          string `json:"creator"`
	Content          string `json:"content"`
	Type             string `json:"type"`
	CreationDate     string `json:"creationDate"`
	ModificationDate string `json:"modificationDate"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "impact" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("impact scenario: expected resource impact v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("impact scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("impact scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func validateState(state fixtureState) error {
	if state.AccountSID != accountSID {
		return fmt.Errorf("impact scenario: accountSid must be %s", accountSID)
	}
	programs := map[int]struct{}{}
	for _, program := range state.Programs {
		if _, exists := programs[program.ID]; exists {
			return fmt.Errorf("impact scenario: duplicate program id %d", program.ID)
		}
		programs[program.ID] = struct{}{}
	}
	partners := map[string]fixturePartner{}
	for i, partner := range state.Partners {
		if _, ok := programs[partner.CampaignID]; !ok {
			return fmt.Errorf("impact scenario: partners[%d] references unknown program %d", i, partner.CampaignID)
		}
		if _, exists := partners[partner.ID]; exists {
			return fmt.Errorf("impact scenario: duplicate partner id %q", partner.ID)
		}
		parsed, err := url.ParseRequestURI(partner.Website)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("impact scenario: partners[%d].website is not an absolute URL", i)
		}
		if _, err := time.Parse(time.RFC3339, partner.DateCreated); err != nil {
			return fmt.Errorf("impact scenario: partners[%d].dateCreated: %w", i, err)
		}
		partners[partner.ID] = partner
	}
	actions := map[string]struct{}{}
	oids := map[string]struct{}{}
	for i, action := range state.Actions {
		if _, ok := programs[action.CampaignID]; !ok {
			return fmt.Errorf("impact scenario: actions[%d] references unknown program %d", i, action.CampaignID)
		}
		partner, ok := partners[action.PartnerID]
		if !ok {
			return fmt.Errorf("impact scenario: actions[%d] references unknown partner %q", i, action.PartnerID)
		}
		if partner.CampaignID != action.CampaignID {
			return fmt.Errorf("impact scenario: actions[%d] partner %q is not on program %d", i, action.PartnerID, action.CampaignID)
		}
		if _, exists := actions[action.ID]; exists {
			return fmt.Errorf("impact scenario: duplicate action id %q", action.ID)
		}
		if _, exists := oids[action.Oid]; exists {
			return fmt.Errorf("impact scenario: duplicate order id %q", action.Oid)
		}
		actions[action.ID] = struct{}{}
		oids[action.Oid] = struct{}{}
		if _, err := time.Parse(time.RFC3339, action.EventDate); err != nil {
			return fmt.Errorf("impact scenario: actions[%d].eventDate: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, action.CreationDate); err != nil {
			return fmt.Errorf("impact scenario: actions[%d].creationDate: %w", i, err)
		}
		if action.CustomerCountry != "" && len(action.CustomerCountry) != 2 {
			return fmt.Errorf("impact scenario: actions[%d].customerCountry must be a two-letter code", i)
		}
		if action.Items == nil {
			return fmt.Errorf("impact scenario: actions[%d].items is required", i)
		}
		seenSKU := map[string]struct{}{}
		var sum int64
		for j, item := range action.Items {
			if _, exists := seenSKU[item.SKU]; exists {
				return fmt.Errorf("impact scenario: actions[%d] repeats sku %q", i, item.SKU)
			}
			seenSKU[item.SKU] = struct{}{}
			if item.Quantity < 1 {
				return fmt.Errorf("impact scenario: actions[%d].items[%d].quantity must be positive", i, j)
			}
			sum += item.SaleAmountPaise
		}
		if len(action.Items) > 0 && sum != action.AmountPaise {
			return fmt.Errorf("impact scenario: actions[%d] item saleAmountPaise sum %d, want amountPaise %d", i, sum, action.AmountPaise)
		}
	}
	notes := map[string]struct{}{}
	for i, note := range state.Notes {
		if _, ok := programs[note.CampaignID]; !ok {
			return fmt.Errorf("impact scenario: notes[%d] references unknown program %d", i, note.CampaignID)
		}
		partner, ok := partners[note.PartnerID]
		if !ok {
			return fmt.Errorf("impact scenario: notes[%d] references unknown partner %q", i, note.PartnerID)
		}
		if partner.CampaignID != note.CampaignID {
			return fmt.Errorf("impact scenario: notes[%d] partner %q is not on program %d", i, note.PartnerID, note.CampaignID)
		}
		if _, exists := notes[note.ID]; exists {
			return fmt.Errorf("impact scenario: duplicate note id %q", note.ID)
		}
		notes[note.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339, note.CreationDate); err != nil {
			return fmt.Errorf("impact scenario: notes[%d].creationDate: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, note.ModificationDate); err != nil {
			return fmt.Errorf("impact scenario: notes[%d].modificationDate: %w", i, err)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("impact scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("impact scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"notes", "action_items", "actions", "partners", "programs", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("impact scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('accountSid', ?)", state.AccountSID); err != nil {
		return fmt.Errorf("impact scenario: insert account: %w", err)
	}
	for _, program := range state.Programs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO programs(id, name, state, type, short_description) VALUES(?, ?, ?, ?, ?)`,
			program.ID, program.Name, program.State, program.Type, program.ShortDescription); err != nil {
			return fmt.Errorf("impact scenario: insert program %d: %w", program.ID, err)
		}
	}
	for _, partner := range state.Partners {
		if _, err := tx.ExecContext(ctx, `INSERT INTO partners(id, campaign_id, name, state, currency, website, description, date_created)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, partner.ID, partner.CampaignID, partner.Name, partner.State, partner.Currency,
			partner.Website, partner.Description, partner.DateCreated); err != nil {
			return fmt.Errorf("impact scenario: insert partner %s: %w", partner.ID, err)
		}
	}
	for _, action := range state.Actions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO actions(
			id, campaign_id, partner_id, oid, state, payout_paise, amount_paise, currency,
			event_date, creation_date, customer_city, customer_region, customer_country, customer_post_code, note)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			action.ID, action.CampaignID, action.PartnerID, action.Oid, action.State, action.PayoutPaise, action.AmountPaise,
			action.Currency, action.EventDate, action.CreationDate, action.CustomerCity, action.CustomerRegion,
			action.CustomerCountry, action.CustomerPostCode, action.Note); err != nil {
			return fmt.Errorf("impact scenario: insert action %s: %w", action.ID, err)
		}
		for position, item := range action.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO action_items(action_id, position, sku, name, quantity, sale_amount_paise)
				VALUES(?, ?, ?, ?, ?, ?)`, action.ID, position, item.SKU, item.Name, item.Quantity, item.SaleAmountPaise); err != nil {
				return fmt.Errorf("impact scenario: insert item %s on %s: %w", item.SKU, action.ID, err)
			}
		}
	}
	for _, note := range state.Notes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notes(
			id, campaign_id, partner_id, creator, content, type, creation_date, modification_date)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, note.ID, note.CampaignID, note.PartnerID, note.Creator, note.Content,
			note.Type, note.CreationDate, note.ModificationDate); err != nil {
			return fmt.Errorf("impact scenario: insert note %s: %w", note.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("impact scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='accountSid'").Scan(&state.AccountSID); err != nil {
		return scenario.Document{}, fmt.Errorf("impact scenario: dump account: %w", err)
	}
	programs, err := dumpPrograms(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	partners, err := dumpPartners(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	actions, err := dumpActions(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	notes, err := dumpNotes(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	state.Programs = programs
	state.Partners = partners
	state.Actions = actions
	state.Notes = notes
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "impact", ResourceVersion: "v1", State: raw,
	}, nil
}

func dumpPrograms(ctx context.Context, db *sql.DB) ([]fixtureProgram, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, state, type, short_description FROM programs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("impact scenario: dump programs: %w", err)
	}
	defer rows.Close()
	out := []fixtureProgram{}
	for rows.Next() {
		var program fixtureProgram
		if err := rows.Scan(&program.ID, &program.Name, &program.State, &program.Type, &program.ShortDescription); err != nil {
			return nil, err
		}
		out = append(out, program)
	}
	return out, rows.Err()
}

func dumpPartners(ctx context.Context, db *sql.DB) ([]fixturePartner, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, campaign_id, name, state, currency, website, description, date_created
		FROM partners ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("impact scenario: dump partners: %w", err)
	}
	defer rows.Close()
	out := []fixturePartner{}
	for rows.Next() {
		var partner fixturePartner
		if err := rows.Scan(&partner.ID, &partner.CampaignID, &partner.Name, &partner.State, &partner.Currency,
			&partner.Website, &partner.Description, &partner.DateCreated); err != nil {
			return nil, err
		}
		out = append(out, partner)
	}
	return out, rows.Err()
}

func dumpActions(ctx context.Context, db *sql.DB) ([]fixtureAction, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, campaign_id, partner_id, oid, state, payout_paise, amount_paise, currency,
		event_date, creation_date, customer_city, customer_region, customer_country, customer_post_code, note
		FROM actions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("impact scenario: dump actions: %w", err)
	}
	out := []fixtureAction{}
	for rows.Next() {
		var action fixtureAction
		if err := rows.Scan(&action.ID, &action.CampaignID, &action.PartnerID, &action.Oid, &action.State,
			&action.PayoutPaise, &action.AmountPaise, &action.Currency, &action.EventDate, &action.CreationDate,
			&action.CustomerCity, &action.CustomerRegion, &action.CustomerCountry, &action.CustomerPostCode, &action.Note); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, action)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		items, err := dumpItems(ctx, db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Items = items
	}
	return out, nil
}

func dumpItems(ctx context.Context, db *sql.DB, actionID string) ([]fixtureItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT sku, name, quantity, sale_amount_paise FROM action_items
		WHERE action_id=? ORDER BY position, sku`, actionID)
	if err != nil {
		return nil, fmt.Errorf("impact scenario: dump items for %s: %w", actionID, err)
	}
	defer rows.Close()
	out := []fixtureItem{}
	for rows.Next() {
		var item fixtureItem
		if err := rows.Scan(&item.SKU, &item.Name, &item.Quantity, &item.SaleAmountPaise); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func dumpNotes(ctx context.Context, db *sql.DB) ([]fixtureNote, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, campaign_id, partner_id, creator, content, type, creation_date, modification_date
		FROM notes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("impact scenario: dump notes: %w", err)
	}
	defer rows.Close()
	out := []fixtureNote{}
	for rows.Next() {
		var note fixtureNote
		if err := rows.Scan(&note.ID, &note.CampaignID, &note.PartnerID, &note.Creator, &note.Content, &note.Type,
			&note.CreationDate, &note.ModificationDate); err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, rows.Err()
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("impact scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("impact scenario: state has trailing data")
	}
	if state.Programs == nil || state.Partners == nil || state.Actions == nil || state.Notes == nil {
		return fixtureState{}, fmt.Errorf("impact scenario: programs, partners, actions, and notes are required arrays")
	}
	for i := range state.Actions {
		if state.Actions[i].Items == nil {
			return fixtureState{}, fmt.Errorf("impact scenario: actions[%d].items is required", i)
		}
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }
