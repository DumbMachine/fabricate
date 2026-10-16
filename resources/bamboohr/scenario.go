package bamboohr

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/mail"
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
		panic(fmt.Sprintf("bamboohr: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("bamboohr-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("bamboohr: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("bamboohr-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("bamboohr: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Company         fixtureCompany      `json:"company"`
	CurrentUserID   string              `json:"currentUserId"`
	Departments     []fixtureDepartment `json:"departments"`
	Employees       []fixtureEmployee   `json:"employees"`
	TimeOffTypes    []fixtureType       `json:"timeOffTypes"`
	TimeOffRequests []fixtureRequest    `json:"timeOffRequests"`
}

type fixtureCompany struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

type fixtureDepartment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fixtureEmployee struct {
	ID               string `json:"id"`
	FirstName        string `json:"firstName"`
	LastName         string `json:"lastName"`
	PreferredName    string `json:"preferredName"`
	WorkEmail        string `json:"workEmail"`
	MobilePhone      string `json:"mobilePhone"`
	JobTitleName     string `json:"jobTitleName"`
	DepartmentID     string `json:"departmentId,omitempty"`
	Status           string `json:"status"`
	EmploymentStatus string `json:"employmentStatus"`
	HireDate         string `json:"hireDate,omitempty"`
	Location         string `json:"location"`
}

type fixtureType struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Units  string `json:"units"`
	Color  string `json:"color"`
	Icon   string `json:"icon"`
	Source string `json:"source"`
}

type fixtureRequest struct {
	ID                  string             `json:"id"`
	EmployeeID          string             `json:"employeeId"`
	TypeID              string             `json:"typeId"`
	Start               string             `json:"start"`
	End                 string             `json:"end"`
	Status              string             `json:"status"`
	Amount              float64            `json:"amount"`
	Unit                string             `json:"unit"`
	EmployeeNote        string             `json:"employeeNote"`
	ManagerNote         string             `json:"managerNote,omitempty"`
	Created             string             `json:"created"`
	Updated             string             `json:"updated"`
	LastChanged         string             `json:"lastChanged"`
	LastChangedByUserID string             `json:"lastChangedByUserId"`
	Dates               map[string]float64 `json:"dates"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "bamboohr" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("bamboohr scenario: expected resource bamboohr v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("bamboohr scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("bamboohr scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.Company.Name) == "" || strings.TrimSpace(state.Company.Domain) == "" || state.Company.ID < 1 {
		return fmt.Errorf("bamboohr scenario: company requires id, name, and domain")
	}
	departments := map[string]string{}
	departmentNames := map[string]struct{}{}
	for i, department := range state.Departments {
		if department.ID == "" || department.Name == "" {
			return fmt.Errorf("bamboohr scenario: departments[%d] requires id and name", i)
		}
		if _, exists := departments[department.ID]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate department id %q", department.ID)
		}
		if _, exists := departmentNames[department.Name]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate department name %q", department.Name)
		}
		departments[department.ID] = department.Name
		departmentNames[department.Name] = struct{}{}
	}
	employees := map[string]struct{}{}
	emails := map[string]struct{}{}
	for i, employee := range state.Employees {
		if employee.ID == "" || employee.FirstName == "" || employee.LastName == "" {
			return fmt.Errorf("bamboohr scenario: employees[%d] requires id, firstName, and lastName", i)
		}
		if employee.Status != "Active" && employee.Status != "Inactive" {
			return fmt.Errorf("bamboohr scenario: employees[%d].status must be Active or Inactive", i)
		}
		if _, err := mail.ParseAddress(employee.WorkEmail); err != nil {
			return fmt.Errorf("bamboohr scenario: employees[%d].workEmail: %w", i, err)
		}
		if employee.DepartmentID != "" {
			if _, ok := departments[employee.DepartmentID]; !ok {
				return fmt.Errorf("bamboohr scenario: employee %q references unknown department %q", employee.ID, employee.DepartmentID)
			}
		}
		if employee.HireDate != "" {
			if _, err := time.Parse("2006-01-02", employee.HireDate); err != nil {
				return fmt.Errorf("bamboohr scenario: employees[%d].hireDate: %w", i, err)
			}
		}
		if _, exists := employees[employee.ID]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate employee id %q", employee.ID)
		}
		if _, exists := emails[employee.WorkEmail]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate work email %q", employee.WorkEmail)
		}
		employees[employee.ID] = struct{}{}
		emails[employee.WorkEmail] = struct{}{}
	}
	if state.CurrentUserID != "" {
		if _, ok := employees[state.CurrentUserID]; !ok {
			return fmt.Errorf("bamboohr scenario: currentUserId %q is not an employee", state.CurrentUserID)
		}
	}
	types := map[string]fixtureType{}
	for i, item := range state.TimeOffTypes {
		if item.ID == "" || item.Name == "" {
			return fmt.Errorf("bamboohr scenario: timeOffTypes[%d] requires id and name", i)
		}
		if item.Units != "hours" && item.Units != "days" {
			return fmt.Errorf("bamboohr scenario: timeOffTypes[%d].units must be hours or days", i)
		}
		if _, exists := types[item.ID]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate time off type id %q", item.ID)
		}
		types[item.ID] = item
	}
	requests := map[string]struct{}{}
	for i, request := range state.TimeOffRequests {
		if request.ID == "" {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d] requires id", i)
		}
		if _, ok := employees[request.EmployeeID]; !ok {
			return fmt.Errorf("bamboohr scenario: time off %q references unknown employee %q", request.ID, request.EmployeeID)
		}
		item, ok := types[request.TypeID]
		if !ok {
			return fmt.Errorf("bamboohr scenario: time off %q references unknown type %q", request.ID, request.TypeID)
		}
		if request.Unit != item.Units {
			return fmt.Errorf("bamboohr scenario: time off %q unit %q does not match type %q", request.ID, request.Unit, item.Units)
		}
		start, err := time.Parse("2006-01-02", request.Start)
		if err != nil {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d].start: %w", i, err)
		}
		end, err := time.Parse("2006-01-02", request.End)
		if err != nil {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d].end: %w", i, err)
		}
		if end.Before(start) {
			return fmt.Errorf("bamboohr scenario: time off %q ends before it starts", request.ID)
		}
		if _, err := time.Parse("2006-01-02", request.Created); err != nil {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d].created: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, request.Updated); err != nil {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d].updated: %w", i, err)
		}
		if _, err := time.Parse("2006-01-02", request.LastChanged); err != nil {
			return fmt.Errorf("bamboohr scenario: timeOffRequests[%d].lastChanged: %w", i, err)
		}
		if request.Dates == nil {
			return fmt.Errorf("bamboohr scenario: time off %q dates must be an object", request.ID)
		}
		var sum float64
		for day, amount := range request.Dates {
			when, err := time.Parse("2006-01-02", day)
			if err != nil {
				return fmt.Errorf("bamboohr scenario: time off %q date %q: %w", request.ID, day, err)
			}
			if when.Before(start) || when.After(end) {
				return fmt.Errorf("bamboohr scenario: time off %q date %q is outside the request", request.ID, day)
			}
			if amount < 0 {
				return fmt.Errorf("bamboohr scenario: time off %q date %q amount is negative", request.ID, day)
			}
			sum += amount
		}
		if len(request.Dates) > 0 && math.Abs(sum-request.Amount) > 0.001 {
			return fmt.Errorf("bamboohr scenario: time off %q amount does not match daily amounts", request.ID)
		}
		if _, exists := requests[request.ID]; exists {
			return fmt.Errorf("bamboohr scenario: duplicate time off request id %q", request.ID)
		}
		requests[request.ID] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("bamboohr scenario: initialize: %w", err)
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
		return fmt.Errorf("bamboohr scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"time_off_requests", "employees", "time_off_types", "departments", "company", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("bamboohr scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentUserId', ?)", state.CurrentUserID); err != nil {
		return fmt.Errorf("bamboohr scenario: insert metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO company(id, name, domain) VALUES(?, ?, ?)", state.Company.ID, state.Company.Name, state.Company.Domain); err != nil {
		return fmt.Errorf("bamboohr scenario: insert company: %w", err)
	}
	for _, department := range state.Departments {
		if _, err := tx.ExecContext(ctx, "INSERT INTO departments(id, name) VALUES(?, ?)", department.ID, department.Name); err != nil {
			return fmt.Errorf("bamboohr scenario: insert department %s: %w", department.ID, err)
		}
	}
	for _, employee := range state.Employees {
		if _, err := tx.ExecContext(ctx, `INSERT INTO employees
			(id, first_name, last_name, preferred_name, work_email, mobile_phone, job_title_name, department_id, status, employment_status, hire_date, location)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			employee.ID, employee.FirstName, employee.LastName, employee.PreferredName, employee.WorkEmail, employee.MobilePhone,
			employee.JobTitleName, nullString(employee.DepartmentID), employee.Status, employee.EmploymentStatus, nullString(employee.HireDate), employee.Location); err != nil {
			return fmt.Errorf("bamboohr scenario: insert employee %s: %w", employee.ID, err)
		}
	}
	for _, item := range state.TimeOffTypes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO time_off_types(id, name, units, color, icon, source) VALUES(?, ?, ?, ?, ?, ?)",
			item.ID, item.Name, item.Units, item.Color, item.Icon, item.Source); err != nil {
			return fmt.Errorf("bamboohr scenario: insert time off type %s: %w", item.ID, err)
		}
	}
	for _, request := range state.TimeOffRequests {
		if request.Dates == nil {
			request.Dates = map[string]float64{}
		}
		rawDates, err := json.Marshal(request.Dates)
		if err != nil {
			return fmt.Errorf("bamboohr scenario: encode dates for %s: %w", request.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO time_off_requests
			(id, employee_id, type_id, start_date, end_date, status, amount, unit, employee_note, manager_note, created, updated, last_changed, last_changed_by_user_id, dates_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID, request.EmployeeID, request.TypeID, request.Start, request.End, request.Status, request.Amount, request.Unit,
			request.EmployeeNote, nullString(request.ManagerNote), request.Created, request.Updated, request.LastChanged, request.LastChangedByUserID, string(rawDates)); err != nil {
			return fmt.Errorf("bamboohr scenario: insert time off %s: %w", request.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bamboohr scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserId'").Scan(&state.CurrentUserID); err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump current user: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT id, name, domain FROM company").Scan(&state.Company.ID, &state.Company.Name, &state.Company.Domain); err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump company: %w", err)
	}
	departmentRows, err := db.QueryContext(ctx, "SELECT id, name FROM departments ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump departments: %w", err)
	}
	for departmentRows.Next() {
		var department fixtureDepartment
		if err := departmentRows.Scan(&department.ID, &department.Name); err != nil {
			departmentRows.Close()
			return scenario.Document{}, err
		}
		state.Departments = append(state.Departments, department)
	}
	if err := departmentRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	employeeRows, err := db.QueryContext(ctx, `SELECT id, first_name, last_name, preferred_name, work_email, mobile_phone, job_title_name,
		department_id, status, employment_status, hire_date, location FROM employees ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump employees: %w", err)
	}
	for employeeRows.Next() {
		var employee fixtureEmployee
		var departmentID, hireDate sql.NullString
		if err := employeeRows.Scan(&employee.ID, &employee.FirstName, &employee.LastName, &employee.PreferredName, &employee.WorkEmail,
			&employee.MobilePhone, &employee.JobTitleName, &departmentID, &employee.Status, &employee.EmploymentStatus, &hireDate, &employee.Location); err != nil {
			employeeRows.Close()
			return scenario.Document{}, err
		}
		employee.DepartmentID = departmentID.String
		employee.HireDate = hireDate.String
		state.Employees = append(state.Employees, employee)
	}
	if err := employeeRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	typeRows, err := db.QueryContext(ctx, "SELECT id, name, units, color, icon, source FROM time_off_types ORDER BY id")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump time off types: %w", err)
	}
	for typeRows.Next() {
		var item fixtureType
		if err := typeRows.Scan(&item.ID, &item.Name, &item.Units, &item.Color, &item.Icon, &item.Source); err != nil {
			typeRows.Close()
			return scenario.Document{}, err
		}
		state.TimeOffTypes = append(state.TimeOffTypes, item)
	}
	if err := typeRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	requestRows, err := db.QueryContext(ctx, `SELECT id, employee_id, type_id, start_date, end_date, status, amount, unit, employee_note,
		manager_note, created, updated, last_changed, last_changed_by_user_id, dates_json FROM time_off_requests ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump time off requests: %w", err)
	}
	defer requestRows.Close()
	for requestRows.Next() {
		var request fixtureRequest
		var manager sql.NullString
		var rawDates string
		if err := requestRows.Scan(&request.ID, &request.EmployeeID, &request.TypeID, &request.Start, &request.End, &request.Status,
			&request.Amount, &request.Unit, &request.EmployeeNote, &manager, &request.Created, &request.Updated, &request.LastChanged,
			&request.LastChangedByUserID, &rawDates); err != nil {
			return scenario.Document{}, err
		}
		request.ManagerNote = manager.String
		request.Dates = map[string]float64{}
		if err := json.Unmarshal([]byte(rawDates), &request.Dates); err != nil {
			return scenario.Document{}, fmt.Errorf("bamboohr scenario: dump time off %s dates: %w", request.ID, err)
		}
		if request.Dates == nil {
			request.Dates = map[string]float64{}
		}
		state.TimeOffRequests = append(state.TimeOffRequests, request)
	}
	if err := requestRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Departments == nil {
		state.Departments = []fixtureDepartment{}
	}
	if state.Employees == nil {
		state.Employees = []fixtureEmployee{}
	}
	if state.TimeOffTypes == nil {
		state.TimeOffTypes = []fixtureType{}
	}
	if state.TimeOffRequests == nil {
		state.TimeOffRequests = []fixtureRequest{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "bamboohr", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("bamboohr scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("bamboohr scenario: state has trailing data")
	}
	if state.Departments == nil || state.Employees == nil || state.TimeOffTypes == nil || state.TimeOffRequests == nil {
		return fixtureState{}, fmt.Errorf("bamboohr scenario: departments, employees, timeOffTypes, and timeOffRequests are required arrays")
	}
	for i := range state.TimeOffRequests {
		if state.TimeOffRequests[i].Dates == nil {
			return fixtureState{}, fmt.Errorf("bamboohr scenario: time off %q dates must be an object", state.TimeOffRequests[i].ID)
		}
	}
	return state, nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func ContractName() string { return scenario.Contract }
