package carta

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"regexp"
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
		panic(fmt.Sprintf("carta: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("carta-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("carta: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("carta-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("carta: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

var (
	datePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	dateTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,3})?Z$`)
	decimalPattern  = regexp.MustCompile(`^[+\-]?((0|[1-9][0-9]*)(\.[0-9]*)?|\.[0-9]+)([eE][+\-]?[0-9]+)?$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

type scenarioCodec struct{}

type fixtureState struct {
	Issuer            fixtureIssuer        `json:"issuer"`
	Stakeholders      []fixtureStakeholder `json:"stakeholders"`
	Certificates      []fixtureCertificate `json:"certificates"`
	OptionGrants      []fixtureOptionGrant `json:"optionGrants"`
	FairMarketValues  []fixtureFMV         `json:"fairMarketValues"`
	DraftOptionGrants []fixtureDraft       `json:"draftOptionGrants"`
}

type fixtureIssuer struct {
	ID                  string `json:"id"`
	LegalName           string `json:"legalName"`
	DoingBusinessAsName string `json:"doingBusinessAsName"`
	Website             string `json:"website"`
}

type fixtureStakeholder struct {
	ID           string          `json:"id"`
	IssuerID     string          `json:"issuerId"`
	FullName     string          `json:"fullName"`
	Email        string          `json:"email,omitempty"`
	Relationship string          `json:"relationship"`
	EntityType   string          `json:"entityType"`
	Address      *fixtureAddress `json:"address,omitempty"`
}

type fixtureAddress struct {
	Country string `json:"country"`
}

type stringValue struct {
	Value string `json:"value"`
}

type fixtureMoney struct {
	CurrencyCode stringValue `json:"currencyCode"`
	Amount       stringValue `json:"amount"`
}

type fixtureCertificate struct {
	ID             string       `json:"id"`
	IssuerID       string       `json:"issuerId"`
	StakeholderID  string       `json:"stakeholderId"`
	ShareClassName string       `json:"shareClassName"`
	SecurityLabel  string       `json:"securityLabel"`
	IssueDate      stringValue  `json:"issueDate"`
	Quantity       stringValue  `json:"quantity"`
	PricePerShare  fixtureMoney `json:"pricePerShare"`
}

type fixtureOptionGrant struct {
	ID                      string       `json:"id"`
	IssuerID                string       `json:"issuerId"`
	StakeholderID           string       `json:"stakeholderId"`
	EquityIncentivePlanName string       `json:"equityIncentivePlanName"`
	SecurityLabel           string       `json:"securityLabel"`
	StockOptionType         string       `json:"stockOptionType"`
	IssueDate               stringValue  `json:"issueDate"`
	Quantity                stringValue  `json:"quantity"`
	OutstandingQuantity     stringValue  `json:"outstandingQuantity"`
	ExercisePrice           fixtureMoney `json:"exercisePrice"`
}

type fixtureFMV struct {
	ID                   string              `json:"id"`
	EffectiveDate        stringValue         `json:"effectiveDate"`
	ExpirationDate       stringValue         `json:"expirationDate"`
	ShareClassValuations []fixtureShareClass `json:"shareClassValuations"`
}

type fixtureShareClass struct {
	ShareClassID   string       `json:"shareClassId"`
	ShareClassName string       `json:"shareClassName"`
	Common         bool         `json:"common"`
	Price          fixtureMoney `json:"price"`
}

type fixtureDraft struct {
	ID              string                  `json:"id"`
	SetID           string                  `json:"draftOptionGrantSetId"`
	IssuerID        string                  `json:"issuerId"`
	State           string                  `json:"state"`
	StockOptionType string                  `json:"stockOptionType"`
	GrantReason     string                  `json:"grantReason,omitempty"`
	Quantity        stringValue             `json:"quantity"`
	EarlyExercise   *bool                   `json:"earlyExercise,omitempty"`
	ExercisePrice   *fixtureMoney           `json:"exercisePrice,omitempty"`
	Stakeholder     fixtureDraftStakeholder `json:"stakeholder"`
	Notes           string                  `json:"notes,omitempty"`
	CreateTime      stringValue             `json:"createTime"`
	UpdateTime      stringValue             `json:"updateTime"`
}

type fixtureDraftStakeholder struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	EmployeeID   string `json:"employeeId,omitempty"`
	Type         string `json:"type,omitempty"`
	Relationship string `json:"relationship,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "carta" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("carta scenario: expected resource carta v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("carta scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("carta scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("carta scenario: initialize: %w", err)
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
		return fmt.Errorf("carta scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"draft_option_grants", "draft_sets", "fair_market_values", "option_grants", "certificates", "stakeholders", "issuer"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("carta scenario: clear %s: %w", table, err)
		}
	}
	issuer := state.Issuer
	if _, err := tx.ExecContext(ctx, `INSERT INTO issuer(id, legal_name, doing_business_as_name, website) VALUES(?, ?, ?, ?)`,
		issuer.ID, issuer.LegalName, issuer.DoingBusinessAsName, issuer.Website); err != nil {
		return fmt.Errorf("carta scenario: insert issuer: %w", err)
	}
	for i, item := range state.Stakeholders {
		country := ""
		if item.Address != nil {
			country = item.Address.Country
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stakeholders(position, id, issuer_id, full_name, email, relationship, entity_type, country)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, i, item.ID, item.IssuerID, item.FullName, item.Email, item.Relationship, item.EntityType, country); err != nil {
			return fmt.Errorf("carta scenario: insert stakeholder %s: %w", item.ID, err)
		}
	}
	for i, item := range state.Certificates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO certificates(position, id, issuer_id, stakeholder_id, share_class_name, security_label, issue_date, quantity, currency, price)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, i, item.ID, item.IssuerID, item.StakeholderID, item.ShareClassName, item.SecurityLabel,
			item.IssueDate.Value, item.Quantity.Value, item.PricePerShare.CurrencyCode.Value, item.PricePerShare.Amount.Value); err != nil {
			return fmt.Errorf("carta scenario: insert certificate %s: %w", item.ID, err)
		}
	}
	for i, item := range state.OptionGrants {
		if _, err := tx.ExecContext(ctx, `INSERT INTO option_grants(position, id, issuer_id, stakeholder_id, plan_name, security_label, stock_option_type, issue_date, quantity, outstanding_quantity, currency, exercise_price)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, i, item.ID, item.IssuerID, item.StakeholderID, item.EquityIncentivePlanName, item.SecurityLabel,
			item.StockOptionType, item.IssueDate.Value, item.Quantity.Value, item.OutstandingQuantity.Value, item.ExercisePrice.CurrencyCode.Value, item.ExercisePrice.Amount.Value); err != nil {
			return fmt.Errorf("carta scenario: insert option grant %s: %w", item.ID, err)
		}
	}
	for i, item := range state.FairMarketValues {
		raw, err := json.Marshal(item.ShareClassValuations)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fair_market_values(position, id, effective_date, expiration_date, valuations) VALUES(?, ?, ?, ?, ?)`,
			i, item.ID, item.EffectiveDate.Value, item.ExpirationDate.Value, string(raw)); err != nil {
			return fmt.Errorf("carta scenario: insert fair market value %s: %w", item.ID, err)
		}
	}
	setSeq := map[string]int{}
	for i, item := range state.DraftOptionGrants {
		if _, ok := setSeq[item.SetID]; !ok {
			setSeq[item.SetID] = len(setSeq) + 1
			if _, err := tx.ExecContext(ctx, `INSERT INTO draft_sets(id, issuer_id, created_seq) VALUES(?, ?, ?)`, item.SetID, item.IssuerID, setSeq[item.SetID]); err != nil {
				return fmt.Errorf("carta scenario: insert draft set %s: %w", item.SetID, err)
			}
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO draft_option_grants(position, id, issuer_id, set_id, body) VALUES(?, ?, ?, ?, ?)`,
			i, item.ID, item.IssuerID, item.SetID, string(raw)); err != nil {
			return fmt.Errorf("carta scenario: insert draft option grant %s: %w", item.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("carta scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, `SELECT id, legal_name, doing_business_as_name, website FROM issuer`).Scan(
		&state.Issuer.ID, &state.Issuer.LegalName, &state.Issuer.DoingBusinessAsName, &state.Issuer.Website); err != nil {
		return scenario.Document{}, fmt.Errorf("carta scenario: dump issuer: %w", err)
	}
	stakeholderRows, err := db.QueryContext(ctx, `SELECT id, issuer_id, full_name, email, relationship, entity_type, country FROM stakeholders ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, err
	}
	for stakeholderRows.Next() {
		var item fixtureStakeholder
		var country string
		if err := stakeholderRows.Scan(&item.ID, &item.IssuerID, &item.FullName, &item.Email, &item.Relationship, &item.EntityType, &country); err != nil {
			stakeholderRows.Close()
			return scenario.Document{}, err
		}
		if country != "" {
			item.Address = &fixtureAddress{Country: country}
		}
		state.Stakeholders = append(state.Stakeholders, item)
	}
	if err := stakeholderRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	certificateRows, err := db.QueryContext(ctx, `SELECT id, issuer_id, stakeholder_id, share_class_name, security_label, issue_date, quantity, currency, price FROM certificates ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, err
	}
	for certificateRows.Next() {
		var item fixtureCertificate
		if err := certificateRows.Scan(&item.ID, &item.IssuerID, &item.StakeholderID, &item.ShareClassName, &item.SecurityLabel, &item.IssueDate.Value, &item.Quantity.Value, &item.PricePerShare.CurrencyCode.Value, &item.PricePerShare.Amount.Value); err != nil {
			certificateRows.Close()
			return scenario.Document{}, err
		}
		state.Certificates = append(state.Certificates, item)
	}
	if err := certificateRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	grantRows, err := db.QueryContext(ctx, `SELECT id, issuer_id, stakeholder_id, plan_name, security_label, stock_option_type, issue_date, quantity, outstanding_quantity, currency, exercise_price FROM option_grants ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, err
	}
	for grantRows.Next() {
		var item fixtureOptionGrant
		if err := grantRows.Scan(&item.ID, &item.IssuerID, &item.StakeholderID, &item.EquityIncentivePlanName, &item.SecurityLabel, &item.StockOptionType, &item.IssueDate.Value, &item.Quantity.Value, &item.OutstandingQuantity.Value, &item.ExercisePrice.CurrencyCode.Value, &item.ExercisePrice.Amount.Value); err != nil {
			grantRows.Close()
			return scenario.Document{}, err
		}
		state.OptionGrants = append(state.OptionGrants, item)
	}
	if err := grantRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	fmvRows, err := db.QueryContext(ctx, `SELECT id, effective_date, expiration_date, valuations FROM fair_market_values ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, err
	}
	for fmvRows.Next() {
		var item fixtureFMV
		var raw string
		if err := fmvRows.Scan(&item.ID, &item.EffectiveDate.Value, &item.ExpirationDate.Value, &raw); err != nil {
			fmvRows.Close()
			return scenario.Document{}, err
		}
		if err := json.Unmarshal([]byte(raw), &item.ShareClassValuations); err != nil {
			fmvRows.Close()
			return scenario.Document{}, fmt.Errorf("carta scenario: dump fair market value %s: %w", item.ID, err)
		}
		if item.ShareClassValuations == nil {
			item.ShareClassValuations = []fixtureShareClass{}
		}
		state.FairMarketValues = append(state.FairMarketValues, item)
	}
	if err := fmvRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	draftRows, err := db.QueryContext(ctx, `SELECT body FROM draft_option_grants ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, err
	}
	for draftRows.Next() {
		var raw string
		if err := draftRows.Scan(&raw); err != nil {
			draftRows.Close()
			return scenario.Document{}, err
		}
		var item fixtureDraft
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&item); err != nil {
			draftRows.Close()
			return scenario.Document{}, fmt.Errorf("carta scenario: dump draft option grant: %w", err)
		}
		state.DraftOptionGrants = append(state.DraftOptionGrants, item)
	}
	if err := draftRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if state.Stakeholders == nil {
		state.Stakeholders = []fixtureStakeholder{}
	}
	if state.Certificates == nil {
		state.Certificates = []fixtureCertificate{}
	}
	if state.OptionGrants == nil {
		state.OptionGrants = []fixtureOptionGrant{}
	}
	if state.FairMarketValues == nil {
		state.FairMarketValues = []fixtureFMV{}
	}
	if state.DraftOptionGrants == nil {
		state.DraftOptionGrants = []fixtureDraft{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "carta", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("carta scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("carta scenario: state has trailing data")
	}
	if state.Stakeholders == nil || state.Certificates == nil || state.OptionGrants == nil || state.FairMarketValues == nil || state.DraftOptionGrants == nil {
		return fixtureState{}, fmt.Errorf("carta scenario: stakeholders, certificates, optionGrants, fairMarketValues, and draftOptionGrants are required arrays")
	}
	return state, nil
}

func validateState(state fixtureState) error {
	issuerID := state.Issuer.ID
	stakeholders := map[string]struct{}{}
	for i, item := range state.Stakeholders {
		if item.IssuerID != issuerID {
			return fmt.Errorf("carta scenario: stakeholders[%d] issuerId must be %s", i, issuerID)
		}
		if _, exists := stakeholders[item.ID]; exists {
			return fmt.Errorf("carta scenario: duplicate stakeholder id %q", item.ID)
		}
		stakeholders[item.ID] = struct{}{}
		if item.Email != "" {
			if _, err := mail.ParseAddress(item.Email); err != nil {
				return fmt.Errorf("carta scenario: stakeholders[%d] email: %w", i, err)
			}
		}
	}
	if err := uniqueSecurities("certificates", certificatesOf(state), stakeholders, issuerID); err != nil {
		return err
	}
	for i, item := range state.Certificates {
		if err := checkDate(item.IssueDate.Value); err != nil {
			return fmt.Errorf("carta scenario: certificates[%d].issueDate: %w", i, err)
		}
		if err := checkMoney(item.Quantity.Value, item.PricePerShare); err != nil {
			return fmt.Errorf("carta scenario: certificates[%d]: %w", i, err)
		}
	}
	seenGrants := map[string]struct{}{}
	for i, item := range state.OptionGrants {
		if item.IssuerID != issuerID {
			return fmt.Errorf("carta scenario: optionGrants[%d] issuerId must be %s", i, issuerID)
		}
		if _, ok := stakeholders[item.StakeholderID]; !ok {
			return fmt.Errorf("carta scenario: optionGrants[%d] references unknown stakeholder %q", i, item.StakeholderID)
		}
		if _, exists := seenGrants[item.ID]; exists {
			return fmt.Errorf("carta scenario: duplicate option grant id %q", item.ID)
		}
		seenGrants[item.ID] = struct{}{}
		if err := checkDate(item.IssueDate.Value); err != nil {
			return fmt.Errorf("carta scenario: optionGrants[%d].issueDate: %w", i, err)
		}
		if err := checkDecimal(item.Quantity.Value); err != nil || checkDecimal(item.OutstandingQuantity.Value) != nil {
			return fmt.Errorf("carta scenario: optionGrants[%d] quantity must be a decimal string", i)
		}
		if err := checkMoneyValue(item.ExercisePrice); err != nil {
			return fmt.Errorf("carta scenario: optionGrants[%d].exercisePrice: %w", i, err)
		}
	}
	seenFMV := map[string]struct{}{}
	for i, item := range state.FairMarketValues {
		if _, exists := seenFMV[item.ID]; exists {
			return fmt.Errorf("carta scenario: duplicate fair market value id %q", item.ID)
		}
		seenFMV[item.ID] = struct{}{}
		if err := checkDate(item.EffectiveDate.Value); err != nil {
			return fmt.Errorf("carta scenario: fairMarketValues[%d].effectiveDate: %w", i, err)
		}
		if err := checkDate(item.ExpirationDate.Value); err != nil {
			return fmt.Errorf("carta scenario: fairMarketValues[%d].expirationDate: %w", i, err)
		}
		for j, valuation := range item.ShareClassValuations {
			if err := checkMoneyValue(valuation.Price); err != nil {
				return fmt.Errorf("carta scenario: fairMarketValues[%d].shareClassValuations[%d]: %w", i, j, err)
			}
		}
	}
	seenDrafts := map[string]struct{}{}
	for i, item := range state.DraftOptionGrants {
		if item.IssuerID != issuerID {
			return fmt.Errorf("carta scenario: draftOptionGrants[%d] issuerId must be %s", i, issuerID)
		}
		if _, exists := seenDrafts[item.ID]; exists {
			return fmt.Errorf("carta scenario: duplicate draft option grant id %q", item.ID)
		}
		seenDrafts[item.ID] = struct{}{}
		if _, err := mail.ParseAddress(item.Stakeholder.Email); err != nil {
			return fmt.Errorf("carta scenario: draftOptionGrants[%d] stakeholder email: %w", i, err)
		}
		if err := checkDecimal(item.Quantity.Value); err != nil {
			return fmt.Errorf("carta scenario: draftOptionGrants[%d].quantity: %w", i, err)
		}
		if item.ExercisePrice != nil {
			if err := checkMoneyValue(*item.ExercisePrice); err != nil {
				return fmt.Errorf("carta scenario: draftOptionGrants[%d].exercisePrice: %w", i, err)
			}
		}
		if err := checkDateTime(item.CreateTime.Value); err != nil {
			return fmt.Errorf("carta scenario: draftOptionGrants[%d].createTime: %w", i, err)
		}
		if err := checkDateTime(item.UpdateTime.Value); err != nil {
			return fmt.Errorf("carta scenario: draftOptionGrants[%d].updateTime: %w", i, err)
		}
	}
	return nil
}

func certificatesOf(state fixtureState) []idRef {
	out := make([]idRef, len(state.Certificates))
	for i, item := range state.Certificates {
		out[i] = idRef{ID: item.ID, IssuerID: item.IssuerID, StakeholderID: item.StakeholderID}
	}
	return out
}

type idRef struct {
	ID, IssuerID, StakeholderID string
}

func uniqueSecurities(name string, items []idRef, stakeholders map[string]struct{}, issuerID string) error {
	seen := map[string]struct{}{}
	for i, item := range items {
		if item.IssuerID != issuerID {
			return fmt.Errorf("carta scenario: %s[%d] issuerId must be %s", name, i, issuerID)
		}
		if _, ok := stakeholders[item.StakeholderID]; !ok {
			return fmt.Errorf("carta scenario: %s[%d] references unknown stakeholder %q", name, i, item.StakeholderID)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("carta scenario: duplicate %s id %q", name, item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	return nil
}

func checkDate(value string) error {
	if !datePattern.MatchString(value) {
		return fmt.Errorf("must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return err
	}
	return nil
}

func checkDateTime(value string) error {
	if !dateTimePattern.MatchString(value) {
		return fmt.Errorf("must be an ISO-8601 UTC timestamp")
	}
	return nil
}

func checkDecimal(value string) error {
	if !decimalPattern.MatchString(value) {
		return fmt.Errorf("must be a decimal string")
	}
	return nil
}

func checkMoney(quantity string, money fixtureMoney) error {
	if err := checkDecimal(quantity); err != nil {
		return err
	}
	return checkMoneyValue(money)
}

func checkMoneyValue(money fixtureMoney) error {
	if !currencyPattern.MatchString(money.CurrencyCode.Value) {
		return fmt.Errorf("currency must be an ISO 4217 code")
	}
	return checkDecimal(money.Amount.Value)
}

func ContractName() string { return scenario.Contract }
