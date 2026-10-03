package okta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/okta/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

var errAmbiguous = errors.New("login shortname is ambiguous")

var eqExpression = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*)\s+[Ee][Qq]\s+"([^"]*)"$`)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("okta: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("okta: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("okta: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("okta: load OpenAPI: %w", err)
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
			code := "E0000001"
			summary := err.Error()
			if status == http.StatusUnauthorized {
				code = "E0000011"
				summary = "Invalid token provided"
			}
			writeOktaError(w, status, code, summary)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListUsers(ctx context.Context, request generated.ListUsersRequestObject) (generated.ListUsersResponseObject, error) {
	users, err := s.listUsers(ctx, "")
	if err != nil {
		return nil, err
	}
	filtered, err := filterUsers(users, deref(request.Params.Q), deref(request.Params.Search), deref(request.Params.Filter))
	if err != nil {
		return generated.ListUsersdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	page, err := paginate(filtered, deref(request.Params.After), limitValue(request.Params.Limit, 200), func(user storedUser) string { return user.ID })
	if err != nil {
		return generated.ListUsersdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	out := make([]generated.User, 0, len(page))
	for _, user := range page {
		out = append(out, user.response())
	}
	return generated.ListUsers200JSONResponse(out), nil
}

func (s *server) GetUser(ctx context.Context, request generated.GetUserRequestObject) (generated.GetUserResponseObject, error) {
	user, err := s.findUser(ctx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetUserdefaultJSONResponse{Body: notFound(request.Id), StatusCode: http.StatusNotFound}, nil
	}
	if errors.Is(err, errAmbiguous) {
		return generated.GetUserdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetUser200JSONResponse(user.response()), nil
}

func (s *server) CreateUser(ctx context.Context, request generated.CreateUserRequestObject) (generated.CreateUserResponseObject, error) {
	if request.Body == nil {
		return generated.CreateUserdefaultJSONResponse{Body: apiError("E0000001", "Request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	profile := request.Body.Profile
	email := string(profile.Email)
	if _, err := mail.ParseAddress(email); err != nil {
		return generated.CreateUserdefaultJSONResponse{Body: apiError("E0000001", "profile.email is invalid"), StatusCode: http.StatusBadRequest}, nil
	}
	login := profile.Login
	now := formatOktaTime(s.clock.Now())
	status := "ACTIVE"
	activated := now
	if request.Params.Activate != nil && !*request.Params.Activate {
		status = "STAGED"
		activated = ""
	}
	groupIDs := uniqueIDs(request.Body.GroupIds)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(login) = lower(?)`, login).Scan(&existing)
	if err == nil {
		return generated.CreateUserdefaultJSONResponse{Body: apiError("E0000001", "login: An object with this field already exists in the current organization"), StatusCode: http.StatusBadRequest}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for _, groupID := range groupIDs {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, groupID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return generated.CreateUserdefaultJSONResponse{Body: apiError("E0000001", "group not found: "+groupID), StatusCode: http.StatusBadRequest}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	id, err := s.ids.Next(ctx, "user")
	if err != nil {
		return nil, fmt.Errorf("okta: allocate user ID: %w", err)
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM users`).Scan(&position); err != nil {
		return nil, err
	}
	user := storedUser{
		ID: id, Status: status, Created: now, Activated: activated, StatusChanged: now, LastUpdated: now,
		FirstName: deref(profile.FirstName), LastName: deref(profile.LastName), Email: email, Login: login,
		PrimaryPhone: deref(profile.PrimaryPhone), Title: deref(profile.Title), Department: deref(profile.Department),
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users
		(id, status, created, activated, status_changed, last_login, last_updated,
		 first_name, last_name, email, login, primary_phone, title, department, position)
		VALUES(?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Status, user.Created, nullIfEmpty(user.Activated), user.StatusChanged, user.LastUpdated,
		user.FirstName, user.LastName, user.Email, user.Login, nullIfEmpty(user.PrimaryPhone),
		nullIfEmpty(user.Title), nullIfEmpty(user.Department), position); err != nil {
		return nil, err
	}
	for _, groupID := range groupIDs {
		var memberPosition int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM group_users WHERE group_id = ?`, groupID).Scan(&memberPosition); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_users(group_id, user_id, position) VALUES(?, ?, ?)`, groupID, user.ID, memberPosition); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groups SET last_membership_updated = ? WHERE id = ?`, now, groupID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.CreateUser200JSONResponse(user.response()), nil
}

func (s *server) ListGroups(ctx context.Context, request generated.ListGroupsRequestObject) (generated.ListGroupsResponseObject, error) {
	groups, err := s.listGroups(ctx)
	if err != nil {
		return nil, err
	}
	q := deref(request.Params.Q)
	filtered := make([]storedGroup, 0, len(groups))
	for _, group := range groups {
		if q != "" && !hasPrefixFold(group.Name, q) {
			continue
		}
		if search := deref(request.Params.Search); search != "" {
			ok, err := matchExpression(search, true, group.fields())
			if err != nil {
				return generated.ListGroupsdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
			}
			if !ok {
				continue
			}
		}
		if filter := deref(request.Params.Filter); filter != "" {
			ok, err := matchExpression(filter, false, group.fields())
			if err != nil {
				return generated.ListGroupsdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
			}
			if !ok {
				continue
			}
		}
		filtered = append(filtered, group)
	}
	page, err := paginate(filtered, deref(request.Params.After), limitValue(request.Params.Limit, 200), func(group storedGroup) string { return group.ID })
	if err != nil {
		return generated.ListGroupsdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	out := make([]generated.Group, 0, len(page))
	for _, group := range page {
		out = append(out, group.response())
	}
	return generated.ListGroups200JSONResponse(out), nil
}

func (s *server) ListGroupUsers(ctx context.Context, request generated.ListGroupUsersRequestObject) (generated.ListGroupUsersResponseObject, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, request.GroupId).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ListGroupUsersdefaultJSONResponse{Body: notFound(request.GroupId), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	users, err := s.listUsers(ctx, request.GroupId)
	if err != nil {
		return nil, err
	}
	page, err := paginate(users, deref(request.Params.After), limitValue(request.Params.Limit, 200), func(user storedUser) string { return user.ID })
	if err != nil {
		return generated.ListGroupUsersdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	out := make([]generated.User, 0, len(page))
	for _, user := range page {
		out = append(out, user.response())
	}
	return generated.ListGroupUsers200JSONResponse(out), nil
}

func (s *server) ListApplications(ctx context.Context, request generated.ListApplicationsRequestObject) (generated.ListApplicationsResponseObject, error) {
	apps, err := s.listApps(ctx)
	if err != nil {
		return nil, err
	}
	q := deref(request.Params.Q)
	filtered := make([]storedApp, 0, len(apps))
	for _, app := range apps {
		if q != "" && !hasPrefixFold(app.Name, q) && !hasPrefixFold(app.Label, q) {
			continue
		}
		if filter := deref(request.Params.Filter); filter != "" {
			ok, err := matchExpression(filter, false, app.fields())
			if err != nil {
				return generated.ListApplicationsdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
			}
			if !ok {
				continue
			}
		}
		filtered = append(filtered, app)
	}
	page, err := paginate(filtered, deref(request.Params.After), limitValue(request.Params.Limit, 200), func(app storedApp) string { return app.ID })
	if err != nil {
		return generated.ListApplicationsdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	out := make([]generated.Application, 0, len(page))
	for _, app := range page {
		out = append(out, app.response())
	}
	return generated.ListApplications200JSONResponse(out), nil
}

func (s *server) GetApplication(ctx context.Context, request generated.GetApplicationRequestObject) (generated.GetApplicationResponseObject, error) {
	app, err := s.getApp(ctx, request.AppId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetApplicationdefaultJSONResponse{Body: notFound(request.AppId), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetApplication200JSONResponse(app.response()), nil
}

func (s *server) ListApplicationUsers(ctx context.Context, request generated.ListApplicationUsersRequestObject) (generated.ListApplicationUsersResponseObject, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM apps WHERE id = ?`, request.AppId).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ListApplicationUsersdefaultJSONResponse{Body: notFound(request.AppId), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT au.user_id, au.scope, au.status, u.email, u.login
		FROM app_users au JOIN users u ON u.id = au.user_id
		WHERE au.app_id = ? ORDER BY au.position, au.user_id`, request.AppId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := []generated.AppUser{}
	for rows.Next() {
		var id, scope, status, email, login string
		if err := rows.Scan(&id, &scope, &status, &email, &login); err != nil {
			return nil, err
		}
		address := openapi_types.Email(email)
		assignments = append(assignments, generated.AppUser{
			Id:     id,
			Scope:  generated.AppUserScope(scope),
			Status: generated.AppUserStatus(status),
			Profile: &generated.AppUserProfile{
				Email: &address,
				Login: strPtr(login),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page, err := paginate(assignments, deref(request.Params.After), limitValue(request.Params.Limit, 200), func(user generated.AppUser) string { return user.Id })
	if err != nil {
		return generated.ListApplicationUsersdefaultJSONResponse{Body: apiError("E0000001", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	return generated.ListApplicationUsers200JSONResponse(page), nil
}

type storedUser struct {
	ID, Status, Created, Activated, StatusChanged, LastLogin, LastUpdated string
	FirstName, LastName, Email, Login, PrimaryPhone, Title, Department    string
}

func (u storedUser) response() generated.User {
	user := generated.User{
		Id:          u.ID,
		Status:      generated.UserStatus(u.Status),
		Created:     u.Created,
		LastUpdated: u.LastUpdated,
		Profile: generated.UserProfile{
			Email:        openapi_types.Email(u.Email),
			Login:        u.Login,
			FirstName:    strPtr(u.FirstName),
			LastName:     strPtr(u.LastName),
			PrimaryPhone: strPtr(u.PrimaryPhone),
			Title:        strPtr(u.Title),
			Department:   strPtr(u.Department),
		},
		Activated:     strPtr(u.Activated),
		StatusChanged: strPtr(u.StatusChanged),
		LastLogin:     strPtr(u.LastLogin),
	}
	return user
}

func (u storedUser) fields() map[string]string {
	return map[string]string{
		"id": u.ID, "status": u.Status,
		"profile.login": u.Login, "profile.email": u.Email,
		"profile.firstName": u.FirstName, "profile.lastName": u.LastName,
	}
}

func (u storedUser) matchesQ(q string) bool {
	return hasPrefixFold(u.FirstName, q) || hasPrefixFold(u.LastName, q) || hasPrefixFold(u.Email, q)
}

type storedGroup struct {
	ID, Type, Created, LastUpdated, LastMembershipUpdated, Name, Description string
}

func (g storedGroup) response() generated.Group {
	objectClass := []string{"okta:user_group"}
	if g.Type != "OKTA_GROUP" {
		objectClass = []string{}
	}
	return generated.Group{
		Id: g.ID, Type: generated.GroupType(g.Type), Created: g.Created, LastUpdated: g.LastUpdated,
		LastMembershipUpdated: g.LastMembershipUpdated, ObjectClass: &objectClass,
		Profile: generated.OktaUserGroupProfile{Name: g.Name, Description: strPtr(g.Description)},
	}
}

func (g storedGroup) fields() map[string]string {
	return map[string]string{"id": g.ID, "type": g.Type, "profile.name": g.Name}
}

type storedApp struct {
	ID, Name, Label, Status, SignOnMode, Created, LastUpdated, NotesAdmin, SsoAcsURL string
}

func (a storedApp) response() generated.Application {
	app := generated.Application{
		Id: a.ID, Name: a.Name, Label: a.Label, Status: generated.ApplicationLifecycleStatus(a.Status),
		SignOnMode: generated.ApplicationSignOnMode(a.SignOnMode), Created: a.Created, LastUpdated: a.LastUpdated,
	}
	if a.NotesAdmin != "" || a.SsoAcsURL != "" {
		app.Settings = &generated.ApplicationSettings{}
		if a.NotesAdmin != "" {
			app.Settings.Notes = &generated.ApplicationSettingsNotes{Admin: strPtr(a.NotesAdmin)}
		}
		if a.SsoAcsURL != "" {
			app.Settings.SignOn = &generated.SamlApplicationSettingsSignOn{SsoAcsUrl: strPtr(a.SsoAcsURL)}
		}
	}
	return app
}

func (a storedApp) fields() map[string]string {
	return map[string]string{"id": a.ID, "status": a.Status, "name": a.Name, "label": a.Label}
}

const userColumns = `id, status, created, IFNULL(activated, ''), IFNULL(status_changed, ''), IFNULL(last_login, ''), last_updated,
	first_name, last_name, email, login, IFNULL(primary_phone, ''), IFNULL(title, ''), IFNULL(department, '')`

func (s *server) listUsers(ctx context.Context, groupID string) ([]storedUser, error) {
	query := `SELECT ` + userColumns + ` FROM users`
	args := []any{}
	if groupID != "" {
		query = `SELECT ` + userColumns + ` FROM users u JOIN group_users gu ON gu.user_id = u.id WHERE gu.group_id = ? ORDER BY gu.position, u.id`
		args = append(args, groupID)
	} else {
		query += ` ORDER BY position, id`
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []storedUser{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *server) findUser(ctx context.Context, key string) (storedUser, error) {
	user, err := s.userWhere(ctx, `id = ?`, key)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return user, err
	}
	user, err = s.userWhere(ctx, `login = ?`, key)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return user, err
	}
	if strings.Contains(key, "@") || strings.ContainsAny(key, "%_") {
		return storedUser{}, sql.ErrNoRows
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users
		WHERE instr(login, '@') > 1 AND lower(substr(login, 1, instr(login, '@') - 1)) = lower(?)`, key)
	if err != nil {
		return storedUser{}, err
	}
	defer rows.Close()
	var matches []storedUser
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return storedUser{}, err
		}
		matches = append(matches, user)
	}
	if err := rows.Err(); err != nil {
		return storedUser{}, err
	}
	switch len(matches) {
	case 0:
		return storedUser{}, sql.ErrNoRows
	case 1:
		return matches[0], nil
	default:
		return storedUser{}, errAmbiguous
	}
}

func (s *server) userWhere(ctx context.Context, clause, key string) (storedUser, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+clause, key))
}

func scanUser(row interface{ Scan(...any) error }) (storedUser, error) {
	var user storedUser
	err := row.Scan(&user.ID, &user.Status, &user.Created, &user.Activated, &user.StatusChanged, &user.LastLogin, &user.LastUpdated,
		&user.FirstName, &user.LastName, &user.Email, &user.Login, &user.PrimaryPhone, &user.Title, &user.Department)
	return user, err
}

func (s *server) listGroups(ctx context.Context) ([]storedGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, type, created, last_updated, last_membership_updated, name, IFNULL(description, '')
		FROM groups ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []storedGroup{}
	for rows.Next() {
		var group storedGroup
		if err := rows.Scan(&group.ID, &group.Type, &group.Created, &group.LastUpdated, &group.LastMembershipUpdated, &group.Name, &group.Description); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *server) listApps(ctx context.Context) ([]storedApp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, label, status, sign_on_mode, created, last_updated, IFNULL(notes_admin, ''), IFNULL(sso_acs_url, '')
		FROM apps ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	apps := []storedApp{}
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (s *server) getApp(ctx context.Context, id string) (storedApp, error) {
	return scanApp(s.db.QueryRowContext(ctx, `SELECT id, name, label, status, sign_on_mode, created, last_updated, IFNULL(notes_admin, ''), IFNULL(sso_acs_url, '')
		FROM apps WHERE id = ?`, id))
}

func scanApp(row interface{ Scan(...any) error }) (storedApp, error) {
	var app storedApp
	err := row.Scan(&app.ID, &app.Name, &app.Label, &app.Status, &app.SignOnMode, &app.Created, &app.LastUpdated, &app.NotesAdmin, &app.SsoAcsURL)
	return app, err
}

func filterUsers(users []storedUser, q, search, filter string) ([]storedUser, error) {
	includeDeprovisioned := search != "" || filter != ""
	out := make([]storedUser, 0, len(users))
	for _, user := range users {
		if !includeDeprovisioned && user.Status == "DEPROVISIONED" {
			continue
		}
		if search != "" {
			ok, err := matchExpression(search, true, user.fields())
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if filter != "" {
			ok, err := matchExpression(filter, false, user.fields())
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if q != "" && !user.matchesQ(q) {
			continue
		}
		out = append(out, user)
	}
	return out, nil
}

func hasPrefixFold(value, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix))
}

func matchExpression(expr string, fold bool, fields map[string]string) (bool, error) {
	for _, part := range splitAnd(expr) {
		matches := eqExpression.FindStringSubmatch(part)
		if matches == nil {
			return false, fmt.Errorf("unsupported filter expression")
		}
		value, ok := fields[matches[1]]
		if !ok {
			return false, fmt.Errorf("unsupported filter property %s", matches[1])
		}
		if fold {
			if !strings.EqualFold(value, matches[2]) {
				return false, nil
			}
			continue
		}
		if value != matches[2] {
			return false, nil
		}
	}
	return true, nil
}

func splitAnd(expr string) []string {
	var parts []string
	rest := strings.TrimSpace(expr)
	for {
		lower := strings.ToLower(rest)
		idx := strings.Index(lower, " and ")
		if idx < 0 {
			return append(parts, strings.TrimSpace(rest))
		}
		parts = append(parts, strings.TrimSpace(rest[:idx]))
		rest = rest[idx+len(" and "):]
	}
}

func paginate[T any](items []T, after string, limit int, id func(T) string) ([]T, error) {
	start := 0
	if after != "" {
		found := false
		for i, item := range items {
			if id(item) == after {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid after cursor")
		}
	}
	if limit <= 0 {
		limit = 200
	}
	if start > len(items) {
		return []T{}, nil
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	if page == nil {
		page = []T{}
	}
	return page, nil
}

func uniqueIDs(values *[]string) []string {
	if values == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(*values))
	for _, value := range *values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func limitValue(limit *int, fallback int) int {
	if limit == nil {
		return fallback
	}
	return *limit
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	copied := value
	return &copied
}

func formatOktaTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func apiError(code, summary string) generated.Error {
	causes := []generated.ErrorCause{}
	errorID := "fab-okta-" + code
	link := code
	return generated.Error{
		ErrorCauses:  &causes,
		ErrorCode:    &code,
		ErrorId:      &errorID,
		ErrorLink:    &link,
		ErrorSummary: &summary,
	}
}

func notFound(id string) generated.Error {
	return apiError("E0000007", "Not found: Resource not found: "+id)
}

func writeOktaError(w http.ResponseWriter, status int, code, summary string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(code, summary))
}
