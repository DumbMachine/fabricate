package okta

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

// builtInScenarios keeps the reference worlds inside the fab binary so
// foreground runs do not depend on the source checkout being present.
//
//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("okta: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("okta-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("okta: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("okta-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("okta: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Users    []fixtureUser    `json:"users"`
	Groups   []fixtureGroup   `json:"groups"`
	Apps     []fixtureApp     `json:"apps"`
	AppUsers []fixtureAppUser `json:"appUsers"`
}

type fixtureUser struct {
	ID            string         `json:"id"`
	Status        string         `json:"status"`
	Created       string         `json:"created"`
	Activated     string         `json:"activated,omitempty"`
	StatusChanged string         `json:"statusChanged,omitempty"`
	LastLogin     string         `json:"lastLogin,omitempty"`
	LastUpdated   string         `json:"lastUpdated"`
	Profile       fixtureProfile `json:"profile"`
}

type fixtureProfile struct {
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	Email        string `json:"email"`
	Login        string `json:"login"`
	PrimaryPhone string `json:"primaryPhone,omitempty"`
	Title        string `json:"title,omitempty"`
	Department   string `json:"department,omitempty"`
}

type fixtureGroup struct {
	ID                    string              `json:"id"`
	Type                  string              `json:"type"`
	Created               string              `json:"created"`
	LastUpdated           string              `json:"lastUpdated"`
	LastMembershipUpdated string              `json:"lastMembershipUpdated"`
	Profile               fixtureGroupProfile `json:"profile"`
	UserIDs               []string            `json:"userIds"`
}

type fixtureGroupProfile struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type fixtureApp struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Label       string              `json:"label"`
	Status      string              `json:"status"`
	SignOnMode  string              `json:"signOnMode"`
	Created     string              `json:"created"`
	LastUpdated string              `json:"lastUpdated"`
	Settings    *fixtureAppSettings `json:"settings,omitempty"`
}

type fixtureAppSettings struct {
	Notes  *fixtureAppNotes  `json:"notes,omitempty"`
	SignOn *fixtureAppSignOn `json:"signOn,omitempty"`
}

type fixtureAppNotes struct {
	Admin   string `json:"admin,omitempty"`
	Enduser string `json:"enduser,omitempty"`
}

type fixtureAppSignOn struct {
	SsoAcsURL string `json:"ssoAcsUrl,omitempty"`
}

type fixtureAppUser struct {
	AppID  string `json:"appId"`
	UserID string `json:"userId"`
	Scope  string `json:"scope"`
	Status string `json:"status"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "okta" || doc.ResourceVersion != "2024-07" {
		return fmt.Errorf("okta scenario: expected resource okta 2024-07, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("okta scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("okta scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	users := map[string]struct{}{}
	logins := map[string]struct{}{}
	emails := map[string]struct{}{}
	for i, user := range state.Users {
		if _, exists := users[user.ID]; exists {
			return fmt.Errorf("okta scenario: duplicate user id %q", user.ID)
		}
		users[user.ID] = struct{}{}
		if _, err := mail.ParseAddress(user.Profile.Email); err != nil {
			return fmt.Errorf("okta scenario: users[%d].profile.email: %w", i, err)
		}
		loginKey := strings.ToLower(user.Profile.Login)
		if _, exists := logins[loginKey]; exists {
			return fmt.Errorf("okta scenario: duplicate login %q", user.Profile.Login)
		}
		logins[loginKey] = struct{}{}
		emailKey := strings.ToLower(user.Profile.Email)
		if _, exists := emails[emailKey]; exists {
			return fmt.Errorf("okta scenario: duplicate email %q", user.Profile.Email)
		}
		emails[emailKey] = struct{}{}
	}
	groups := map[string]struct{}{}
	for i, group := range state.Groups {
		if _, exists := groups[group.ID]; exists {
			return fmt.Errorf("okta scenario: duplicate group id %q", group.ID)
		}
		groups[group.ID] = struct{}{}
		seen := map[string]struct{}{}
		for _, userID := range group.UserIDs {
			if _, ok := users[userID]; !ok {
				return fmt.Errorf("okta scenario: groups[%d] references unknown user %q", i, userID)
			}
			if _, exists := seen[userID]; exists {
				return fmt.Errorf("okta scenario: groups[%d] repeats user %q", i, userID)
			}
			seen[userID] = struct{}{}
		}
	}
	apps := map[string]struct{}{}
	for i, app := range state.Apps {
		if _, exists := apps[app.ID]; exists {
			return fmt.Errorf("okta scenario: duplicate app id %q", app.ID)
		}
		apps[app.ID] = struct{}{}
		if app.Settings != nil && app.Settings.Notes == nil && app.Settings.SignOn == nil {
			return fmt.Errorf("okta scenario: apps[%d].settings is empty", i)
		}
	}
	seenAssignments := map[string]struct{}{}
	for i, assignment := range state.AppUsers {
		if _, ok := apps[assignment.AppID]; !ok {
			return fmt.Errorf("okta scenario: appUsers[%d] references unknown app %q", i, assignment.AppID)
		}
		if _, ok := users[assignment.UserID]; !ok {
			return fmt.Errorf("okta scenario: appUsers[%d] references unknown user %q", i, assignment.UserID)
		}
		key := assignment.AppID + "\x00" + assignment.UserID
		if _, exists := seenAssignments[key]; exists {
			return fmt.Errorf("okta scenario: duplicate app assignment %s %s", assignment.AppID, assignment.UserID)
		}
		seenAssignments[key] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("okta scenario: initialize: %w", err)
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
		return fmt.Errorf("okta scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"app_users", "group_users", "apps", "groups", "users"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("okta scenario: clear %s: %w", table, err)
		}
	}
	for i, user := range state.Users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users
			(id, status, created, activated, status_changed, last_login, last_updated,
			 first_name, last_name, email, login, primary_phone, title, department, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			user.ID, user.Status, user.Created, nullIfEmpty(user.Activated), nullIfEmpty(user.StatusChanged),
			nullIfEmpty(user.LastLogin), user.LastUpdated, user.Profile.FirstName, user.Profile.LastName,
			user.Profile.Email, user.Profile.Login, nullIfEmpty(user.Profile.PrimaryPhone),
			nullIfEmpty(user.Profile.Title), nullIfEmpty(user.Profile.Department), i); err != nil {
			return fmt.Errorf("okta scenario: insert user %s: %w", user.ID, err)
		}
	}
	for i, group := range state.Groups {
		if _, err := tx.ExecContext(ctx, `INSERT INTO groups
			(id, type, created, last_updated, last_membership_updated, name, description, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			group.ID, group.Type, group.Created, group.LastUpdated, group.LastMembershipUpdated,
			group.Profile.Name, nullIfEmpty(group.Profile.Description), i); err != nil {
			return fmt.Errorf("okta scenario: insert group %s: %w", group.ID, err)
		}
		for position, userID := range group.UserIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_users(group_id, user_id, position) VALUES(?, ?, ?)`,
				group.ID, userID, position); err != nil {
				return fmt.Errorf("okta scenario: insert group member %s %s: %w", group.ID, userID, err)
			}
		}
	}
	for i, app := range state.Apps {
		var notes, acs any
		if app.Settings != nil {
			if app.Settings.Notes != nil {
				notes = nullIfEmpty(app.Settings.Notes.Admin)
			}
			if app.Settings.SignOn != nil {
				acs = nullIfEmpty(app.Settings.SignOn.SsoAcsURL)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO apps
			(id, name, label, status, sign_on_mode, created, last_updated, notes_admin, sso_acs_url, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, app.Name, app.Label, app.Status, app.SignOnMode, app.Created, app.LastUpdated, notes, acs, i); err != nil {
			return fmt.Errorf("okta scenario: insert app %s: %w", app.ID, err)
		}
	}
	for i, assignment := range state.AppUsers {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_users(app_id, user_id, scope, status, position) VALUES(?, ?, ?, ?, ?)`,
			assignment.AppID, assignment.UserID, assignment.Scope, assignment.Status, i); err != nil {
			return fmt.Errorf("okta scenario: insert app user %s %s: %w", assignment.AppID, assignment.UserID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("okta scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	users, err := dumpUsers(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	groups, err := dumpGroups(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	apps, err := dumpApps(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	appUsers, err := dumpAppUsers(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(fixtureState{Users: users, Groups: groups, Apps: apps, AppUsers: appUsers})
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "okta", ResourceVersion: "2024-07", State: raw,
	}, nil
}

func dumpUsers(ctx context.Context, db *sql.DB) ([]fixtureUser, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, status, created, activated, status_changed, last_login, last_updated,
		first_name, last_name, email, login, primary_phone, title, department
		FROM users ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("okta scenario: dump users: %w", err)
	}
	defer rows.Close()
	users := []fixtureUser{}
	for rows.Next() {
		var user fixtureUser
		var activated, statusChanged, lastLogin, phone, title, department sql.NullString
		if err := rows.Scan(&user.ID, &user.Status, &user.Created, &activated, &statusChanged, &lastLogin, &user.LastUpdated,
			&user.Profile.FirstName, &user.Profile.LastName, &user.Profile.Email, &user.Profile.Login,
			&phone, &title, &department); err != nil {
			return nil, err
		}
		user.Activated = activated.String
		user.StatusChanged = statusChanged.String
		user.LastLogin = lastLogin.String
		user.Profile.PrimaryPhone = phone.String
		user.Profile.Title = title.String
		user.Profile.Department = department.String
		users = append(users, user)
	}
	return users, rows.Err()
}

func dumpGroups(ctx context.Context, db *sql.DB) ([]fixtureGroup, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, type, created, last_updated, last_membership_updated, name, description
		FROM groups ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("okta scenario: dump groups: %w", err)
	}
	defer rows.Close()
	groups := []fixtureGroup{}
	for rows.Next() {
		var group fixtureGroup
		var description sql.NullString
		if err := rows.Scan(&group.ID, &group.Type, &group.Created, &group.LastUpdated, &group.LastMembershipUpdated,
			&group.Profile.Name, &description); err != nil {
			return nil, err
		}
		group.Profile.Description = description.String
		group.UserIDs = []string{}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range groups {
		members, err := db.QueryContext(ctx, `SELECT user_id FROM group_users WHERE group_id = ? ORDER BY position, user_id`, groups[i].ID)
		if err != nil {
			return nil, err
		}
		for members.Next() {
			var userID string
			if err := members.Scan(&userID); err != nil {
				members.Close()
				return nil, err
			}
			groups[i].UserIDs = append(groups[i].UserIDs, userID)
		}
		if err := members.Err(); err != nil {
			members.Close()
			return nil, err
		}
		if err := members.Close(); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

func dumpApps(ctx context.Context, db *sql.DB) ([]fixtureApp, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, label, status, sign_on_mode, created, last_updated, notes_admin, sso_acs_url
		FROM apps ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("okta scenario: dump apps: %w", err)
	}
	defer rows.Close()
	apps := []fixtureApp{}
	for rows.Next() {
		var app fixtureApp
		var notes, acs sql.NullString
		if err := rows.Scan(&app.ID, &app.Name, &app.Label, &app.Status, &app.SignOnMode, &app.Created, &app.LastUpdated, &notes, &acs); err != nil {
			return nil, err
		}
		if notes.Valid || acs.Valid {
			app.Settings = &fixtureAppSettings{}
			if notes.Valid {
				app.Settings.Notes = &fixtureAppNotes{Admin: notes.String}
			}
			if acs.Valid {
				app.Settings.SignOn = &fixtureAppSignOn{SsoAcsURL: acs.String}
			}
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func dumpAppUsers(ctx context.Context, db *sql.DB) ([]fixtureAppUser, error) {
	rows, err := db.QueryContext(ctx, `SELECT app_id, user_id, scope, status FROM app_users ORDER BY position, app_id, user_id`)
	if err != nil {
		return nil, fmt.Errorf("okta scenario: dump app users: %w", err)
	}
	defer rows.Close()
	assignments := []fixtureAppUser{}
	for rows.Next() {
		var assignment fixtureAppUser
		if err := rows.Scan(&assignment.AppID, &assignment.UserID, &assignment.Scope, &assignment.Status); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("okta scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("okta scenario: state has trailing data")
	}
	if state.Users == nil || state.Groups == nil || state.Apps == nil || state.AppUsers == nil {
		return fixtureState{}, fmt.Errorf("okta scenario: users, groups, apps, and appUsers are required arrays")
	}
	return state, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func ContractName() string { return scenario.Contract }
