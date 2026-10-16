package gainsight

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"

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
		panic(fmt.Sprintf("gainsight: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("gainsight-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("gainsight: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("gainsight-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("gainsight: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Companies    []fixtureCompany  `json:"companies"`
	CTAs         []fixtureCTA      `json:"ctas"`
	SuccessPlans []fixturePlan     `json:"successPlans"`
	Activities   []fixtureActivity `json:"activities"`
}

type fixtureCompany struct {
	Gsid                 string   `json:"gsid"`
	Name                 string   `json:"name"`
	Industry             string   `json:"industry,omitempty"`
	ARR                  *float64 `json:"arr,omitempty"`
	Employees            *int     `json:"employees,omitempty"`
	LifecycleInWeeks     *int     `json:"lifecycleInWeeks,omitempty"`
	OriginalContractDate string   `json:"originalContractDate,omitempty"`
	RenewalDate          string   `json:"renewalDate,omitempty"`
	Stage                string   `json:"stage"`
	Status               string   `json:"status"`
	Health               string   `json:"health"`
	CSMFirstName         string   `json:"csmFirstName"`
	CSMLastName          string   `json:"csmLastName"`
	CSMEmail             string   `json:"csmEmail"`
	Domain               string   `json:"domain"`
	CreatedDate          int64    `json:"createdDate"`
	ModifiedDate         int64    `json:"modifiedDate"`
}

type fixtureCTA struct {
	Gsid          string `json:"gsid"`
	Name          string `json:"name"`
	CompanyID     string `json:"companyId"`
	SuccessPlanID string `json:"successPlanId"`
	OwnerEmail    string `json:"ownerEmail"`
	OwnerName     string `json:"ownerName"`
	DueDate       string `json:"dueDate"`
	Type          string `json:"type"`
	Reason        string `json:"reason"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	Comments      string `json:"comments"`
	EntityType    string `json:"entityType"`
	IsClosed      bool   `json:"isClosed"`
	IsImportant   bool   `json:"isImportant"`
}

type fixturePlan struct {
	Gsid       string `json:"gsid"`
	Name       string `json:"name"`
	CompanyID  string `json:"companyId"`
	OwnerEmail string `json:"ownerEmail"`
	OwnerName  string `json:"ownerName"`
	DueDate    string `json:"dueDate"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	ActionPlan string `json:"actionPlan"`
	EntityType string `json:"entityType"`
}

type fixtureActivity struct {
	Gsid         string `json:"gsid"`
	ExternalID   string `json:"externalId"`
	ContextName  string `json:"contextName"`
	ContextID    string `json:"contextId,omitempty"`
	CompanyID    string `json:"companyId,omitempty"`
	Author       string `json:"author"`
	AuthorID     string `json:"authorId,omitempty"`
	TypeName     string `json:"typeName"`
	Subject      string `json:"subject"`
	Notes        string `json:"notes"`
	ActivityDate string `json:"activityDate"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "gainsight" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("gainsight scenario: expected resource gainsight v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("gainsight scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("gainsight scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	companies := map[string]fixtureCompany{}
	names := map[string]struct{}{}
	for i, company := range state.Companies {
		if _, exists := companies[company.Gsid]; exists {
			return fmt.Errorf("gainsight scenario: duplicate company gsid %q", company.Gsid)
		}
		if _, exists := names[company.Name]; exists {
			return fmt.Errorf("gainsight scenario: duplicate company name %q", company.Name)
		}
		if _, err := mail.ParseAddress(company.CSMEmail); err != nil {
			return fmt.Errorf("gainsight scenario: companies[%d].csmEmail: %w", i, err)
		}
		companies[company.Gsid] = company
		names[company.Name] = struct{}{}
	}
	plans := map[string]fixturePlan{}
	for i, plan := range state.SuccessPlans {
		if _, exists := plans[plan.Gsid]; exists {
			return fmt.Errorf("gainsight scenario: duplicate success plan gsid %q", plan.Gsid)
		}
		company, ok := companies[plan.CompanyID]
		if !ok {
			return fmt.Errorf("gainsight scenario: success plan %q references unknown company %q", plan.Gsid, plan.CompanyID)
		}
		if company.Gsid == "" {
			return fmt.Errorf("gainsight scenario: success plan %q references unknown company %q", plan.Gsid, plan.CompanyID)
		}
		if _, err := mail.ParseAddress(plan.OwnerEmail); err != nil {
			return fmt.Errorf("gainsight scenario: successPlans[%d].ownerEmail: %w", i, err)
		}
		plans[plan.Gsid] = plan
	}
	ctas := map[string]fixtureCTA{}
	for i, cta := range state.CTAs {
		if _, exists := ctas[cta.Gsid]; exists {
			return fmt.Errorf("gainsight scenario: duplicate cta gsid %q", cta.Gsid)
		}
		if _, ok := companies[cta.CompanyID]; !ok {
			return fmt.Errorf("gainsight scenario: cta %q references unknown company %q", cta.Gsid, cta.CompanyID)
		}
		plan, ok := plans[cta.SuccessPlanID]
		if !ok {
			return fmt.Errorf("gainsight scenario: cta %q references unknown success plan %q", cta.Gsid, cta.SuccessPlanID)
		}
		if plan.CompanyID != cta.CompanyID {
			return fmt.Errorf("gainsight scenario: cta %q company does not match success plan %q", cta.Gsid, cta.SuccessPlanID)
		}
		if _, err := mail.ParseAddress(cta.OwnerEmail); err != nil {
			return fmt.Errorf("gainsight scenario: ctas[%d].ownerEmail: %w", i, err)
		}
		ctas[cta.Gsid] = cta
	}
	seenExternal := map[string]struct{}{}
	for i, activity := range state.Activities {
		if _, exists := seenExternal[activity.ExternalID]; exists {
			return fmt.Errorf("gainsight scenario: duplicate activity externalId %q", activity.ExternalID)
		}
		seenExternal[activity.ExternalID] = struct{}{}
		switch activity.ContextName {
		case "Company":
			if _, ok := companies[activity.CompanyID]; !ok {
				return fmt.Errorf("gainsight scenario: activities[%d] references unknown company %q", i, activity.CompanyID)
			}
		case "CTA":
			cta, ok := ctas[activity.ContextID]
			if !ok {
				return fmt.Errorf("gainsight scenario: activities[%d] references unknown cta %q", i, activity.ContextID)
			}
			if activity.CompanyID != "" && activity.CompanyID != cta.CompanyID {
				return fmt.Errorf("gainsight scenario: activities[%d] company does not match cta %q", i, activity.ContextID)
			}
		case "SuccessPlan":
			plan, ok := plans[activity.ContextID]
			if !ok {
				return fmt.Errorf("gainsight scenario: activities[%d] references unknown success plan %q", i, activity.ContextID)
			}
			if activity.CompanyID != "" && activity.CompanyID != plan.CompanyID {
				return fmt.Errorf("gainsight scenario: activities[%d] company does not match success plan %q", i, activity.ContextID)
			}
		default:
			return fmt.Errorf("gainsight scenario: activities[%d] has unsupported context %q", i, activity.ContextName)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("gainsight scenario: initialize: %w", err)
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
		return fmt.Errorf("gainsight scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"activities", "ctas", "success_plans", "companies"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("gainsight scenario: clear %s: %w", table, err)
		}
	}
	for _, company := range state.Companies {
		if err := insertCompany(ctx, tx, company); err != nil {
			return err
		}
	}
	for _, plan := range state.SuccessPlans {
		if err := insertPlan(ctx, tx, plan); err != nil {
			return err
		}
	}
	for _, cta := range state.CTAs {
		if err := insertCTA(ctx, tx, cta); err != nil {
			return err
		}
	}
	for _, activity := range state.Activities {
		if err := insertActivity(ctx, tx, activity); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gainsight scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	companies, err := loadCompanies(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	plans, err := loadPlans(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	ctas, err := loadCTAs(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	activities, err := loadActivities(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	if companies == nil {
		companies = []fixtureCompany{}
	}
	if plans == nil {
		plans = []fixturePlan{}
	}
	if ctas == nil {
		ctas = []fixtureCTA{}
	}
	if activities == nil {
		activities = []fixtureActivity{}
	}
	raw, err := json.Marshal(fixtureState{Companies: companies, CTAs: ctas, SuccessPlans: plans, Activities: activities})
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "gainsight", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("gainsight scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("gainsight scenario: state has trailing data")
	}
	if state.Companies == nil || state.CTAs == nil || state.SuccessPlans == nil || state.Activities == nil {
		return fixtureState{}, fmt.Errorf("gainsight scenario: companies, ctas, successPlans, and activities are required arrays")
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertCompany(ctx context.Context, db execer, company fixtureCompany) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO companies
		(gsid, name, industry, arr, employees, lifecycle_in_weeks, original_contract_date, renewal_date,
		 stage, status, health, csm_first_name, csm_last_name, csm_email, domain, created_date, modified_date)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		company.Gsid, company.Name, company.Industry, nullFloat(company.ARR), nullInt(company.Employees), nullInt(company.LifecycleInWeeks),
		company.OriginalContractDate, company.RenewalDate, company.Stage, company.Status, company.Health,
		company.CSMFirstName, company.CSMLastName, company.CSMEmail, company.Domain, company.CreatedDate, company.ModifiedDate); err != nil {
		return fmt.Errorf("gainsight scenario: insert company %s: %w", company.Gsid, err)
	}
	return nil
}

func insertPlan(ctx context.Context, db execer, plan fixturePlan) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO success_plans
		(gsid, name, company_id, owner_email, owner_name, due_date, type, status, action_plan, entity_type)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.Gsid, plan.Name, plan.CompanyID, plan.OwnerEmail, plan.OwnerName, plan.DueDate, plan.Type, plan.Status, plan.ActionPlan, plan.EntityType); err != nil {
		return fmt.Errorf("gainsight scenario: insert success plan %s: %w", plan.Gsid, err)
	}
	return nil
}

func insertCTA(ctx context.Context, db execer, cta fixtureCTA) error {
	closed, important := 0, 0
	if cta.IsClosed {
		closed = 1
	}
	if cta.IsImportant {
		important = 1
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ctas
		(gsid, name, company_id, success_plan_id, owner_email, owner_name, due_date, type, reason, status, priority, comments, entity_type, is_closed, is_important)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cta.Gsid, cta.Name, cta.CompanyID, cta.SuccessPlanID, cta.OwnerEmail, cta.OwnerName, cta.DueDate, cta.Type, cta.Reason, cta.Status, cta.Priority, cta.Comments, cta.EntityType, closed, important); err != nil {
		return fmt.Errorf("gainsight scenario: insert cta %s: %w", cta.Gsid, err)
	}
	return nil
}

func insertActivity(ctx context.Context, db execer, activity fixtureActivity) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO activities
		(gsid, external_id, context_name, context_id, company_id, author, author_id, type_name, subject, notes, activity_date)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		activity.Gsid, activity.ExternalID, activity.ContextName, activity.ContextID, activity.CompanyID, activity.Author, activity.AuthorID, activity.TypeName, activity.Subject, activity.Notes, activity.ActivityDate); err != nil {
		return fmt.Errorf("gainsight scenario: insert activity %s: %w", activity.Gsid, err)
	}
	return nil
}

func loadCompanies(ctx context.Context, db execer) ([]fixtureCompany, error) {
	rows, err := db.QueryContext(ctx, `SELECT gsid, name, industry, arr, employees, lifecycle_in_weeks, original_contract_date, renewal_date,
		stage, status, health, csm_first_name, csm_last_name, csm_email, domain, created_date, modified_date
		FROM companies ORDER BY gsid`)
	if err != nil {
		return nil, fmt.Errorf("gainsight scenario: dump companies: %w", err)
	}
	defer rows.Close()
	out := []fixtureCompany{}
	for rows.Next() {
		var company fixtureCompany
		var arr sql.NullFloat64
		var employees, lifecycle sql.NullInt64
		if err := rows.Scan(&company.Gsid, &company.Name, &company.Industry, &arr, &employees, &lifecycle, &company.OriginalContractDate, &company.RenewalDate,
			&company.Stage, &company.Status, &company.Health, &company.CSMFirstName, &company.CSMLastName, &company.CSMEmail, &company.Domain, &company.CreatedDate, &company.ModifiedDate); err != nil {
			return nil, err
		}
		if arr.Valid {
			value := arr.Float64
			company.ARR = &value
		}
		if employees.Valid {
			value := int(employees.Int64)
			company.Employees = &value
		}
		if lifecycle.Valid {
			value := int(lifecycle.Int64)
			company.LifecycleInWeeks = &value
		}
		out = append(out, company)
	}
	return out, rows.Err()
}

func loadPlans(ctx context.Context, db execer) ([]fixturePlan, error) {
	rows, err := db.QueryContext(ctx, `SELECT gsid, name, company_id, owner_email, owner_name, due_date, type, status, action_plan, entity_type
		FROM success_plans ORDER BY gsid`)
	if err != nil {
		return nil, fmt.Errorf("gainsight scenario: dump success plans: %w", err)
	}
	defer rows.Close()
	out := []fixturePlan{}
	for rows.Next() {
		var plan fixturePlan
		if err := rows.Scan(&plan.Gsid, &plan.Name, &plan.CompanyID, &plan.OwnerEmail, &plan.OwnerName, &plan.DueDate, &plan.Type, &plan.Status, &plan.ActionPlan, &plan.EntityType); err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func loadCTAs(ctx context.Context, db execer) ([]fixtureCTA, error) {
	rows, err := db.QueryContext(ctx, `SELECT gsid, name, company_id, success_plan_id, owner_email, owner_name, due_date, type, reason, status, priority, comments, entity_type, is_closed, is_important
		FROM ctas ORDER BY gsid`)
	if err != nil {
		return nil, fmt.Errorf("gainsight scenario: dump ctas: %w", err)
	}
	defer rows.Close()
	out := []fixtureCTA{}
	for rows.Next() {
		var cta fixtureCTA
		var closed, important int
		if err := rows.Scan(&cta.Gsid, &cta.Name, &cta.CompanyID, &cta.SuccessPlanID, &cta.OwnerEmail, &cta.OwnerName, &cta.DueDate, &cta.Type, &cta.Reason, &cta.Status, &cta.Priority, &cta.Comments, &cta.EntityType, &closed, &important); err != nil {
			return nil, err
		}
		cta.IsClosed = closed == 1
		cta.IsImportant = important == 1
		out = append(out, cta)
	}
	return out, rows.Err()
}

func loadActivities(ctx context.Context, db execer) ([]fixtureActivity, error) {
	rows, err := db.QueryContext(ctx, `SELECT gsid, external_id, context_name, context_id, company_id, author, author_id, type_name, subject, notes, activity_date
		FROM activities ORDER BY gsid`)
	if err != nil {
		return nil, fmt.Errorf("gainsight scenario: dump activities: %w", err)
	}
	defer rows.Close()
	out := []fixtureActivity{}
	for rows.Next() {
		var activity fixtureActivity
		if err := rows.Scan(&activity.Gsid, &activity.ExternalID, &activity.ContextName, &activity.ContextID, &activity.CompanyID, &activity.Author, &activity.AuthorID, &activity.TypeName, &activity.Subject, &activity.Notes, &activity.ActivityDate); err != nil {
			return nil, err
		}
		out = append(out, activity)
	}
	return out, rows.Err()
}

func nullFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func nullInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}
