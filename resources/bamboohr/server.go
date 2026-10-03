package bamboohr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/bamboohr/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const pageLimit = 250

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("bamboohr: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("bamboohr: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("bamboohr: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("bamboohr: load OpenAPI: %w", err)
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
			writeError(w, status, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GetMetaCompany(ctx context.Context, _ generated.GetMetaCompanyRequestObject) (generated.GetMetaCompanyResponseObject, error) {
	company, err := s.loadCompany(ctx)
	if err != nil {
		return nil, err
	}
	return generated.GetMetaCompany200JSONResponse(company), nil
}

func (s *server) ListEmployees(ctx context.Context, request generated.ListEmployeesRequestObject) (generated.ListEmployeesResponseObject, error) {
	fields := splitFields(stringValue(request.Params.Fields))
	keys, err := parseSort(stringValue(request.Params.Sort))
	if err != nil {
		return generated.ListEmployeesdefaultJSONResponse{Body: apiError(err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	rows, err := s.loadEmployees(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return employeeLess(rows[i], rows[j], keys) })
	company, err := s.loadCompany(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]generated.EmployeeSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, employeeSummary(row, fields))
	}
	return generated.ListEmployees200JSONResponse{
		Data: items,
		Meta: struct {
			Page  generated.Page `json:"page"`
			Total int            `json:"total"`
		}{Page: generated.Page{Limit: pageLimit}, Total: len(items)},
		UnderscoreLinks: generated.EmployeeListLinks{Self: generated.Link{Href: "https://" + company.Domain + ".bamboohr.com/api/v1/employees"}},
	}, nil
}

func (s *server) GetEmployee(ctx context.Context, request generated.GetEmployeeRequestObject) (generated.GetEmployeeResponseObject, error) {
	id := string(request.Id)
	if id == "0" {
		current, err := s.currentUserID(ctx)
		if err != nil {
			return nil, err
		}
		if current == "" {
			return generated.GetEmployee200JSONResponse{Id: "0"}, nil
		}
		id = current
	}
	row, err := s.loadEmployee(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetEmployeedefaultJSONResponse{Body: apiError("employee not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetEmployee200JSONResponse(employeeRecord(row, splitFields(stringValue(request.Params.Fields)))), nil
}

func (s *server) GetEmployeesDirectory(ctx context.Context, _ generated.GetEmployeesDirectoryRequestObject) (generated.GetEmployeesDirectoryResponseObject, error) {
	rows, err := s.loadEmployees(ctx)
	if err != nil {
		return nil, err
	}
	employees := make([]generated.DirectoryEmployee, 0, len(rows))
	for _, row := range rows {
		employees = append(employees, generated.DirectoryEmployee{
			Id: row.ID, DisplayName: row.FirstName + " " + row.LastName, FirstName: row.FirstName, LastName: row.LastName,
			JobTitle: row.JobTitleName, WorkEmail: openapi_types.Email(row.WorkEmail), MobilePhone: row.MobilePhone,
			Department: row.DepartmentName, Location: row.Location, CanUploadPhoto: generated.N1,
		})
	}
	return generated.GetEmployeesDirectory200JSONResponse{
		Fields:    directoryFields(),
		Employees: employees,
	}, nil
}

func (s *server) ListListFields(ctx context.Context, _ generated.ListListFieldsRequestObject) (generated.ListListFieldsResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name FROM departments ORDER BY id")
	if err != nil {
		return nil, err
	}
	options := []generated.ListOption{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		number, err := numericID(id)
		if err != nil {
			rows.Close()
			return nil, err
		}
		options = append(options, generated.ListOption{Id: number, Name: name, Archived: generated.ListOptionArchivedNo})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return generated.ListListFields200JSONResponse{{
		Id: 1, FieldId: 1, Name: "Department", Alias: "department",
		Manageable: generated.ListFieldManageableYes, Multiple: generated.ListFieldMultipleNo, Options: options,
	}}, nil
}

func (s *server) ListTimeOffTypes(ctx context.Context, _ generated.ListTimeOffTypesRequestObject) (generated.ListTimeOffTypesResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, units, color, icon, source FROM time_off_types ORDER BY id")
	if err != nil {
		return nil, err
	}
	items := []generated.TimeOffType{}
	for rows.Next() {
		var id, name, units, color, icon, source string
		if err := rows.Scan(&id, &name, &units, &color, &icon, &source); err != nil {
			rows.Close()
			return nil, err
		}
		unit, err := amountUnit(units)
		if err != nil {
			rows.Close()
			return nil, err
		}
		origin, err := typeSource(source)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, generated.TimeOffType{
			Id: id, Name: name, Units: generated.TimeOffTypeUnits(unit), Color: color, Icon: icon, Source: origin,
		})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return generated.ListTimeOffTypes200JSONResponse{TimeOffTypes: items, DefaultHours: defaultHours()}, rows.Err()
}

func (s *server) ListTimeOffRequests(ctx context.Context, request generated.ListTimeOffRequestsRequestObject) (generated.ListTimeOffRequestsResponseObject, error) {
	start := stringValue(request.Params.Start)
	end := stringValue(request.Params.End)
	since := stringValue(request.Params.UpdatedSince)
	if (start == "") != (end == "") {
		return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("start and end must be supplied together"), StatusCode: http.StatusBadRequest}, nil
	}
	if start == "" && since == "" {
		return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("start and end, or updatedSince, is required"), StatusCode: http.StatusBadRequest}, nil
	}
	if start != "" {
		if _, err := parseDate(start); err != nil {
			return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("start and end must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
		}
		if _, err := parseDate(end); err != nil {
			return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("start and end must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
		}
		if end < start {
			return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("end is before start"), StatusCode: http.StatusBadRequest}, nil
		}
	}
	var sinceTime time.Time
	if since != "" {
		parsed, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return generated.ListTimeOffRequestsdefaultJSONResponse{Body: apiError("updatedSince must be an ISO 8601 timestamp"), StatusCode: http.StatusBadRequest}, nil
		}
		sinceTime = parsed
	}
	if stringValue(request.Params.EmployeeId) == "0" {
		return generated.ListTimeOffRequests200JSONResponse{}, nil
	}
	current, err := s.currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.loadRequests(ctx)
	if err != nil {
		return nil, err
	}
	action := generated.View
	if request.Params.Action != nil {
		action = *request.Params.Action
	}
	statuses := csvSet(stringValue(request.Params.Status))
	types := csvSet(stringValue(request.Params.Type))
	wantID := stringValue(request.Params.Id)
	excludeNote := stringValue(request.Params.ExcludeNote) != ""
	items := []generated.TimeOffRequest{}
	for _, row := range rows {
		if !requestVisible(row, start, end, since, sinceTime) {
			continue
		}
		if wantID != "" && row.ID != wantID {
			continue
		}
		if employeeID := stringValue(request.Params.EmployeeId); employeeID != "" && row.EmployeeID != employeeID {
			continue
		}
		if action == generated.MyRequests && (current == "" || row.EmployeeID != current) {
			continue
		}
		if len(statuses) > 0 {
			if _, ok := statuses[row.Status]; !ok {
				continue
			}
		}
		if len(types) > 0 {
			if _, ok := types[row.TypeID]; !ok {
				continue
			}
		}
		items = append(items, row.response(excludeNote))
	}
	return generated.ListTimeOffRequests200JSONResponse(items), nil
}

func (s *server) CreateTimeOffRequest(ctx context.Context, request generated.CreateTimeOffRequestRequestObject) (generated.CreateTimeOffRequestResponseObject, error) {
	if request.Body == nil {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	employeeID := request.EmployeeId
	if employeeID == "0" {
		current, err := s.currentUserID(ctx)
		if err != nil {
			return nil, err
		}
		if current == "" {
			return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("employee not found"), StatusCode: http.StatusNotFound}, nil
		}
		employeeID = current
	}
	employee, err := s.loadEmployee(ctx, employeeID)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("employee not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	var typeName, typeIcon, typeUnits string
	err = s.db.QueryRowContext(ctx, "SELECT name, icon, units FROM time_off_types WHERE id=?", request.Body.TimeOffTypeId).Scan(&typeName, &typeIcon, &typeUnits)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("time off type not found"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	if err != nil {
		return nil, err
	}
	start, end := request.Body.Start, request.Body.End
	if _, err := parseDate(start); err != nil {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("start must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
	}
	if _, err := parseDate(end); err != nil {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("end must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
	}
	if end < start {
		return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("end is before start"), StatusCode: http.StatusBadRequest}, nil
	}
	dates := map[string]float64{}
	var amount float64
	if request.Body.Dates != nil && len(*request.Body.Dates) > 0 {
		for _, day := range *request.Body.Dates {
			if _, err := parseDate(day.Ymd); err != nil || day.Ymd < start || day.Ymd > end {
				return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("dates must fall within start and end"), StatusCode: http.StatusBadRequest}, nil
			}
			dates[day.Ymd] += float64(day.Amount)
			amount += float64(day.Amount)
		}
	} else if request.Body.Amount != nil {
		amount = float64(*request.Body.Amount)
	} else {
		days, err := inclusiveDays(start, end)
		if err != nil {
			return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError(err.Error()), StatusCode: http.StatusBadRequest}, nil
		}
		amount = float64(days)
	}
	var employeeNote, managerNote string
	if request.Body.Notes != nil {
		for _, note := range *request.Body.Notes {
			switch note.From {
			case generated.Manager:
				managerNote = note.Note
			default:
				employeeNote = note.Note
			}
		}
	}
	status := normalizeStatus(string(request.Body.Status))
	now := s.clock.Now().UTC()
	created := now.Format("2006-01-02")
	updated := now.Format(time.RFC3339)
	actor, err := s.currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if actor == "" {
		actor = employeeID
	}
	id, err := s.ids.Next(ctx, "bamboohr.time-off-request")
	if err != nil {
		return nil, err
	}
	rawDates, err := json.Marshal(dates)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if request.Body.PreviousRequest != nil && *request.Body.PreviousRequest != "" {
		result, err := tx.ExecContext(ctx, `UPDATE time_off_requests
			SET status='superceded', updated=?, last_changed=?, last_changed_by_user_id=? WHERE id=?`,
			updated, created, actor, *request.Body.PreviousRequest)
		if err != nil {
			return nil, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if changed == 0 {
			return generated.CreateTimeOffRequestdefaultJSONResponse{Body: apiError("previous time off request not found"), StatusCode: http.StatusNotFound}, nil
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO time_off_requests
		(id, employee_id, type_id, start_date, end_date, status, amount, unit, employee_note, manager_note, created, updated, last_changed, last_changed_by_user_id, dates_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, employeeID, request.Body.TimeOffTypeId, start, end, status, amount, typeUnits, employeeNote, nullString(managerNote),
		created, updated, created, actor, string(rawDates)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	row := requestRow{
		ID: id, EmployeeID: employeeID, Name: employee.FirstName + " " + employee.LastName, TypeID: request.Body.TimeOffTypeId,
		TypeName: typeName, TypeIcon: typeIcon, Start: start, End: end, Status: status, Amount: amount, Unit: typeUnits,
		EmployeeNote: employeeNote, ManagerNote: managerNote, Created: created, Updated: updated, LastChanged: created,
		LastChangedByUserID: actor, Dates: dates,
	}
	location := "/api/v1/time_off/requests?id=" + url.QueryEscape(id)
	return generated.CreateTimeOffRequest201JSONResponse{
		Body:    row.response(false),
		Headers: generated.CreateTimeOffRequest201ResponseHeaders{Location: &location},
	}, nil
}

func (s *server) UpdateTimeOffRequestStatus(ctx context.Context, request generated.UpdateTimeOffRequestStatusRequestObject) (generated.UpdateTimeOffRequestStatusResponseObject, error) {
	if request.Body == nil {
		return generated.UpdateTimeOffRequestStatusdefaultJSONResponse{Body: apiError("request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	status := normalizeStatus(string(request.Body.Status))
	now := s.clock.Now().UTC()
	actor, err := s.currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	if actor == "" {
		actor = "0"
	}
	var result sql.Result
	if request.Body.Note != nil {
		result, err = s.db.ExecContext(ctx, `UPDATE time_off_requests
			SET status=?, manager_note=?, updated=?, last_changed=?, last_changed_by_user_id=? WHERE id=?`,
			status, *request.Body.Note, now.Format(time.RFC3339), now.Format("2006-01-02"), actor, request.RequestId)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE time_off_requests
			SET status=?, updated=?, last_changed=?, last_changed_by_user_id=? WHERE id=?`,
			status, now.Format(time.RFC3339), now.Format("2006-01-02"), actor, request.RequestId)
	}
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed == 0 {
		return generated.UpdateTimeOffRequestStatusdefaultJSONResponse{Body: apiError("time off request not found"), StatusCode: http.StatusNotFound}, nil
	}
	return generated.UpdateTimeOffRequestStatus200JSONResponse{}, nil
}

func (s *server) ListWhosOut(ctx context.Context, request generated.ListWhosOutRequestObject) (generated.ListWhosOutResponseObject, error) {
	now := s.clock.Now().UTC()
	start := now.Format("2006-01-02")
	end := now.AddDate(0, 0, 14).Format("2006-01-02")
	if value := stringValue(request.Params.Start); value != "" {
		start = value
	}
	if value := stringValue(request.Params.End); value != "" {
		end = value
	}
	if _, err := parseDate(start); err != nil {
		return generated.ListWhosOutdefaultJSONResponse{Body: apiError("start must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
	}
	if _, err := parseDate(end); err != nil {
		return generated.ListWhosOutdefaultJSONResponse{Body: apiError("end must be YYYY-MM-DD"), StatusCode: http.StatusBadRequest}, nil
	}
	if end < start {
		return generated.ListWhosOutdefaultJSONResponse{Body: apiError("end is before start"), StatusCode: http.StatusBadRequest}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.employee_id, e.first_name, e.last_name, r.start_date, r.end_date
		FROM time_off_requests r JOIN employees e ON e.id = r.employee_id
		WHERE r.status='approved' AND r.end_date >= ? AND r.start_date <= ?
		ORDER BY r.start_date, r.id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.WhosOutEntry{}
	for rows.Next() {
		var id, employeeID, first, last, startDate, endDate string
		if err := rows.Scan(&id, &employeeID, &first, &last, &startDate, &endDate); err != nil {
			return nil, err
		}
		items = append(items, generated.WhosOutEntry{
			Id: id, Type: generated.TimeOff, EmployeeId: &employeeID, Name: first + " " + last, Start: startDate, End: endDate,
		})
	}
	return generated.ListWhosOut200JSONResponse(items), rows.Err()
}

type employeeRow struct {
	ID               string
	FirstName        string
	LastName         string
	PreferredName    string
	WorkEmail        string
	MobilePhone      string
	JobTitleName     string
	DepartmentID     string
	DepartmentName   string
	Status           string
	EmploymentStatus string
	HireDate         string
	Location         string
}

type requestRow struct {
	ID                  string
	EmployeeID          string
	Name                string
	TypeID              string
	TypeName            string
	TypeIcon            string
	Start               string
	End                 string
	Status              string
	Amount              float64
	Unit                string
	EmployeeNote        string
	ManagerNote         string
	Created             string
	Updated             string
	LastChanged         string
	LastChangedByUserID string
	Dates               map[string]float64
}

func (s *server) loadCompany(ctx context.Context) (generated.Company, error) {
	var company generated.Company
	err := s.db.QueryRowContext(ctx, "SELECT id, name, domain FROM company").Scan(&company.Id, &company.Name, &company.Domain)
	if err != nil {
		return generated.Company{}, err
	}
	company.BaseApiUrl = "https://api.bamboohr.com/api/gateway.php/" + company.Domain
	return company, nil
}

func (s *server) currentUserID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserId'").Scan(&id)
	return id, err
}

func (s *server) loadEmployees(ctx context.Context) ([]employeeRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.first_name, e.last_name, e.preferred_name, e.work_email, e.mobile_phone,
		e.job_title_name, COALESCE(e.department_id, ''), COALESCE(d.name, ''), e.status, e.employment_status,
		COALESCE(e.hire_date, ''), e.location
		FROM employees e LEFT JOIN departments d ON d.id = e.department_id ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []employeeRow
	for rows.Next() {
		var row employeeRow
		if err := rows.Scan(&row.ID, &row.FirstName, &row.LastName, &row.PreferredName, &row.WorkEmail, &row.MobilePhone,
			&row.JobTitleName, &row.DepartmentID, &row.DepartmentName, &row.Status, &row.EmploymentStatus, &row.HireDate, &row.Location); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func (s *server) loadEmployee(ctx context.Context, id string) (employeeRow, error) {
	var row employeeRow
	err := s.db.QueryRowContext(ctx, `SELECT e.id, e.first_name, e.last_name, e.preferred_name, e.work_email, e.mobile_phone,
		e.job_title_name, COALESCE(e.department_id, ''), COALESCE(d.name, ''), e.status, e.employment_status,
		COALESCE(e.hire_date, ''), e.location
		FROM employees e LEFT JOIN departments d ON d.id = e.department_id WHERE e.id=?`, id).Scan(
		&row.ID, &row.FirstName, &row.LastName, &row.PreferredName, &row.WorkEmail, &row.MobilePhone,
		&row.JobTitleName, &row.DepartmentID, &row.DepartmentName, &row.Status, &row.EmploymentStatus, &row.HireDate, &row.Location)
	return row, err
}

func (s *server) loadRequests(ctx context.Context) ([]requestRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.employee_id, e.first_name, e.last_name, r.type_id, t.name, t.icon,
		r.start_date, r.end_date, r.status, r.amount, r.unit, r.employee_note, COALESCE(r.manager_note, ''),
		r.created, r.updated, r.last_changed, r.last_changed_by_user_id, r.dates_json
		FROM time_off_requests r
		JOIN employees e ON e.id = r.employee_id
		JOIN time_off_types t ON t.id = r.type_id
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []requestRow
	for rows.Next() {
		var row requestRow
		var first, last, rawDates string
		if err := rows.Scan(&row.ID, &row.EmployeeID, &first, &last, &row.TypeID, &row.TypeName, &row.TypeIcon,
			&row.Start, &row.End, &row.Status, &row.Amount, &row.Unit, &row.EmployeeNote, &row.ManagerNote,
			&row.Created, &row.Updated, &row.LastChanged, &row.LastChangedByUserID, &rawDates); err != nil {
			return nil, err
		}
		row.Name = first + " " + last
		row.Dates = map[string]float64{}
		if err := json.Unmarshal([]byte(rawDates), &row.Dates); err != nil {
			return nil, fmt.Errorf("bamboohr: decode time off %s dates: %w", row.ID, err)
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func employeeSummary(row employeeRow, fields map[string]struct{}) generated.EmployeeSummary {
	item := generated.EmployeeSummary{
		EmployeeId: row.ID, FirstName: row.FirstName, LastName: row.LastName, PreferredName: row.PreferredName,
		JobTitleName: row.JobTitleName, Status: summaryStatus(row.Status), UnderscoreRestrictedFields: []string{},
	}
	if _, ok := fields["workEmail"]; ok {
		item.WorkEmail = &row.WorkEmail
	}
	if _, ok := fields["mobilePhone"]; ok {
		item.MobilePhone = &row.MobilePhone
	}
	if _, ok := fields["departmentId"]; ok && row.DepartmentID != "" {
		item.DepartmentId = &row.DepartmentID
	}
	if _, ok := fields["departmentName"]; ok && row.DepartmentName != "" {
		item.DepartmentName = &row.DepartmentName
	}
	if _, ok := fields["hireDate"]; ok && row.HireDate != "" {
		item.HireDate = &row.HireDate
	}
	if _, ok := fields["employmentStatus"]; ok {
		item.EmploymentStatus = &row.EmploymentStatus
	}
	if _, ok := fields["location"]; ok {
		item.Location = &row.Location
	}
	return item
}

func employeeRecord(row employeeRow, fields map[string]struct{}) generated.EmployeeRecord {
	record := generated.EmployeeRecord{Id: row.ID}
	if _, ok := fields["firstName"]; ok {
		record.FirstName = &row.FirstName
	}
	if _, ok := fields["lastName"]; ok {
		record.LastName = &row.LastName
	}
	if _, ok := fields["preferredName"]; ok {
		record.PreferredName = &row.PreferredName
	}
	if _, ok := fields["workEmail"]; ok {
		record.WorkEmail = &row.WorkEmail
	}
	if _, ok := fields["mobilePhone"]; ok {
		record.MobilePhone = &row.MobilePhone
	}
	if _, ok := fields["jobTitleName"]; ok {
		record.JobTitleName = &row.JobTitleName
	}
	if _, ok := fields["department"]; ok && row.DepartmentName != "" {
		record.Department = &row.DepartmentName
	}
	if _, ok := fields["departmentId"]; ok && row.DepartmentID != "" {
		record.DepartmentId = &row.DepartmentID
	}
	if _, ok := fields["status"]; ok {
		status := recordStatus(row.Status)
		record.Status = &status
	}
	if _, ok := fields["employmentStatus"]; ok {
		record.EmploymentStatus = &row.EmploymentStatus
	}
	if _, ok := fields["hireDate"]; ok && row.HireDate != "" {
		record.HireDate = &row.HireDate
	}
	if _, ok := fields["location"]; ok {
		record.Location = &row.Location
	}
	return record
}

func (row requestRow) response(excludeNote bool) generated.TimeOffRequest {
	unit := generated.TimeOffAmountUnitDays
	if row.Unit == "hours" {
		unit = generated.TimeOffAmountUnitHours
	}
	dates := map[string]float32{}
	for day, amount := range row.Dates {
		dates[day] = float32(amount)
	}
	updated := row.Updated
	item := generated.TimeOffRequest{
		Id: row.ID, EmployeeId: row.EmployeeID, Name: row.Name, Start: row.Start, End: row.End, Created: row.Created,
		Updated: &updated,
		Status:  generated.TimeOffStatus{LastChanged: row.LastChanged, LastChangedByUserId: row.LastChangedByUserID, Status: row.Status},
		Type:    generated.TimeOffTypeRef{Id: row.TypeID, Name: row.TypeName, Icon: row.TypeIcon},
		Amount:  generated.TimeOffAmount{Unit: unit, Amount: float32(row.Amount)},
		Actions: requestActions(row.Status),
		Dates:   dates,
		Notes:   generated.TimeOffNotes{},
	}
	if !excludeNote {
		if row.EmployeeNote != "" {
			note := row.EmployeeNote
			item.Notes.Employee = &note
		}
		if row.ManagerNote != "" {
			note := row.ManagerNote
			item.Notes.Manager = &note
		}
	}
	return item
}

func requestActions(status string) generated.TimeOffActions {
	open := status == "requested"
	return generated.TimeOffActions{
		View:    true,
		Edit:    status == "requested" || status == "approved",
		Cancel:  status == "requested" || status == "approved",
		Approve: open,
		Deny:    open,
		Bypass:  open,
	}
}

func requestVisible(row requestRow, start, end, since string, sinceTime time.Time) bool {
	window := false
	if start != "" && row.End >= start && row.Start <= end {
		window = true
	}
	fresh := false
	if since != "" {
		updated, err := time.Parse(time.RFC3339, row.Updated)
		if err == nil && !updated.Before(sinceTime) {
			fresh = true
		}
	}
	if start != "" && since != "" {
		return window || fresh
	}
	if start != "" {
		return window
	}
	return fresh
}

type sortKey struct {
	field string
	desc  bool
}

func parseSort(raw string) ([]sortKey, error) {
	if strings.TrimSpace(raw) == "" {
		return []sortKey{{field: "employeeId"}}, nil
	}
	var keys []sortKey
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		desc := strings.HasPrefix(part, "-")
		field := strings.TrimPrefix(part, "-")
		switch field {
		case "employeeId", "firstName", "lastName", "preferredName", "jobTitleName", "status":
		default:
			return nil, fmt.Errorf("invalid sort field %q", field)
		}
		keys = append(keys, sortKey{field: field, desc: desc})
	}
	if len(keys) == 0 {
		return []sortKey{{field: "employeeId"}}, nil
	}
	return keys, nil
}

func employeeLess(left, right employeeRow, keys []sortKey) bool {
	for _, key := range keys {
		lv, rv := employeeField(left, key.field), employeeField(right, key.field)
		if lv == rv {
			continue
		}
		if key.desc {
			return lv > rv
		}
		return lv < rv
	}
	return left.ID < right.ID
}

func employeeField(row employeeRow, field string) string {
	switch field {
	case "firstName":
		return row.FirstName
	case "lastName":
		return row.LastName
	case "preferredName":
		return row.PreferredName
	case "jobTitleName":
		return row.JobTitleName
	case "status":
		return row.Status
	default:
		return row.ID
	}
}

func splitFields(raw string) map[string]struct{} {
	return csvSet(raw)
}

func csvSet(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			set[part] = struct{}{}
		}
	}
	return set
}

func summaryStatus(value string) generated.EmployeeSummaryStatus {
	if value == "Inactive" {
		return generated.EmployeeSummaryStatusInactive
	}
	return generated.EmployeeSummaryStatusActive
}

func recordStatus(value string) generated.EmployeeRecordStatus {
	if value == "Inactive" {
		return generated.EmployeeRecordStatusInactive
	}
	return generated.EmployeeRecordStatusActive
}

func normalizeStatus(value string) string {
	switch value {
	case "declined":
		return "denied"
	case "cancelled":
		return "canceled"
	default:
		return value
	}
}

func amountUnit(value string) (generated.TimeOffAmountUnit, error) {
	switch value {
	case "days":
		return generated.TimeOffAmountUnitDays, nil
	case "hours":
		return generated.TimeOffAmountUnitHours, nil
	default:
		return "", fmt.Errorf("bamboohr: unknown time off units %q", value)
	}
}

func typeSource(value string) (generated.TimeOffTypeSource, error) {
	switch value {
	case "internal":
		return generated.Internal, nil
	case "remote":
		return generated.Remote, nil
	case "external":
		return generated.External, nil
	default:
		return "", fmt.Errorf("bamboohr: unknown time off source %q", value)
	}
}

func directoryFields() []generated.DirectoryField {
	return []generated.DirectoryField{
		{Id: "displayName", Type: "text", Name: "Display Name"},
		{Id: "firstName", Type: "text", Name: "First Name"},
		{Id: "lastName", Type: "text", Name: "Last Name"},
		{Id: "jobTitle", Type: "list", Name: "Job Title"},
		{Id: "workEmail", Type: "email", Name: "Work Email"},
		{Id: "mobilePhone", Type: "text", Name: "Mobile Phone"},
		{Id: "department", Type: "list", Name: "Department"},
		{Id: "location", Type: "list", Name: "Location"},
	}
}

func defaultHours() []generated.DefaultHours {
	return []generated.DefaultHours{
		{Name: "Monday", Amount: "8.00"},
		{Name: "Tuesday", Amount: "8.00"},
		{Name: "Wednesday", Amount: "8.00"},
		{Name: "Thursday", Amount: "8.00"},
		{Name: "Friday", Amount: "8.00"},
		{Name: "Saturday", Amount: "0.00"},
		{Name: "Sunday", Amount: "0.00"},
	}
}

func parseDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", value)
}

func inclusiveDays(start, end string) (int, error) {
	first, err := parseDate(start)
	if err != nil {
		return 0, err
	}
	last, err := parseDate(end)
	if err != nil {
		return 0, err
	}
	if last.Before(first) {
		return 0, fmt.Errorf("end is before start")
	}
	return int(last.Sub(first).Hours()/24) + 1, nil
}

func numericID(value string) (int, error) {
	var number int
	if _, err := fmt.Sscan(value, &number); err != nil || number < 1 {
		return 0, fmt.Errorf("bamboohr: department id %q is not numeric", value)
	}
	return number, nil
}

func stringValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(message))
}

func apiError(message string) generated.Error {
	return generated.Error{Message: message}
}
