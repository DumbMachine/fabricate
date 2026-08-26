package asana

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

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("asana: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("asana-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("asana: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("asana-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("asana: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CurrentUserGid string             `json:"currentUserGid"`
	Workspaces     []fixtureWorkspace `json:"workspaces"`
	Users          []fixtureUser      `json:"users"`
	Projects       []fixtureProject   `json:"projects"`
	Sections       []fixtureSection   `json:"sections"`
	Tasks          []fixtureTask      `json:"tasks"`
	Stories        []fixtureStory     `json:"stories"`
}

type fixtureWorkspace struct {
	Gid            string `json:"gid"`
	Name           string `json:"name"`
	IsOrganization bool   `json:"isOrganization"`
}

type fixtureUser struct {
	Gid   string `json:"gid"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type fixtureProject struct {
	Gid          string `json:"gid"`
	WorkspaceGid string `json:"workspaceGid"`
	Name         string `json:"name"`
	Archived     bool   `json:"archived"`
}

type fixtureSection struct {
	Gid        string `json:"gid"`
	ProjectGid string `json:"projectGid"`
	Name       string `json:"name"`
	Position   int    `json:"position"`
}

type fixtureTask struct {
	Gid          string   `json:"gid"`
	WorkspaceGid string   `json:"workspaceGid"`
	Name         string   `json:"name"`
	Notes        string   `json:"notes"`
	Completed    bool     `json:"completed"`
	AssigneeGid  string   `json:"assigneeGid,omitempty"`
	DueOn        string   `json:"dueOn,omitempty"`
	SectionGid   string   `json:"sectionGid,omitempty"`
	ProjectGids  []string `json:"projectGids"`
	CreatedAt    string   `json:"createdAt"`
	ModifiedAt   string   `json:"modifiedAt"`
}

type fixtureStory struct {
	Gid             string `json:"gid"`
	TaskGid         string `json:"taskGid"`
	Text            string `json:"text"`
	CreatedBy       string `json:"createdBy"`
	CreatedAt       string `json:"createdAt,omitempty"`
	ResourceSubtype string `json:"resourceSubtype,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "asana" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("asana scenario: expected resource asana v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("asana scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("asana scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	workspaces := map[string]struct{}{}
	for i, workspace := range state.Workspaces {
		if workspace.Gid == "" || workspace.Name == "" {
			return fmt.Errorf("asana scenario: workspaces[%d] requires gid and name", i)
		}
		if _, exists := workspaces[workspace.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate workspace gid %q", workspace.Gid)
		}
		workspaces[workspace.Gid] = struct{}{}
	}
	users := map[string]struct{}{}
	for i, user := range state.Users {
		if user.Gid == "" || user.Name == "" {
			return fmt.Errorf("asana scenario: users[%d] requires gid and name", i)
		}
		if _, err := mail.ParseAddress(user.Email); err != nil {
			return fmt.Errorf("asana scenario: users[%d].email: %w", i, err)
		}
		if _, exists := users[user.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate user gid %q", user.Gid)
		}
		users[user.Gid] = struct{}{}
	}
	if _, ok := users[state.CurrentUserGid]; !ok {
		return fmt.Errorf("asana scenario: currentUserGid %q is not a seeded user", state.CurrentUserGid)
	}
	projects := map[string]struct{}{}
	for i, project := range state.Projects {
		if _, ok := workspaces[project.WorkspaceGid]; !ok {
			return fmt.Errorf("asana scenario: projects[%d] references unknown workspace %q", i, project.WorkspaceGid)
		}
		if _, exists := projects[project.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate project gid %q", project.Gid)
		}
		projects[project.Gid] = struct{}{}
	}
	sections := map[string]struct{}{}
	for i, section := range state.Sections {
		if _, ok := projects[section.ProjectGid]; !ok {
			return fmt.Errorf("asana scenario: sections[%d] references unknown project %q", i, section.ProjectGid)
		}
		if _, exists := sections[section.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate section gid %q", section.Gid)
		}
		sections[section.Gid] = struct{}{}
	}
	tasks := map[string]struct{}{}
	for i, task := range state.Tasks {
		if _, ok := workspaces[task.WorkspaceGid]; !ok {
			return fmt.Errorf("asana scenario: tasks[%d] references unknown workspace %q", i, task.WorkspaceGid)
		}
		if task.AssigneeGid != "" {
			if _, ok := users[task.AssigneeGid]; !ok {
				return fmt.Errorf("asana scenario: tasks[%d] references unknown assignee %q", i, task.AssigneeGid)
			}
		}
		if task.SectionGid != "" {
			if _, ok := sections[task.SectionGid]; !ok {
				return fmt.Errorf("asana scenario: tasks[%d] references unknown section %q", i, task.SectionGid)
			}
		}
		if len(task.ProjectGids) == 0 {
			return fmt.Errorf("asana scenario: tasks[%d] requires projectGids", i)
		}
		for _, projectGid := range task.ProjectGids {
			if _, ok := projects[projectGid]; !ok {
				return fmt.Errorf("asana scenario: tasks[%d] references unknown project %q", i, projectGid)
			}
		}
		if _, exists := tasks[task.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate task gid %q", task.Gid)
		}
		tasks[task.Gid] = struct{}{}
	}
	stories := map[string]struct{}{}
	for i, story := range state.Stories {
		if _, ok := tasks[story.TaskGid]; !ok {
			return fmt.Errorf("asana scenario: stories[%d] references unknown task %q", i, story.TaskGid)
		}
		if _, ok := users[story.CreatedBy]; !ok {
			return fmt.Errorf("asana scenario: stories[%d] references unknown user %q", i, story.CreatedBy)
		}
		if _, exists := stories[story.Gid]; exists {
			return fmt.Errorf("asana scenario: duplicate story gid %q", story.Gid)
		}
		stories[story.Gid] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("asana scenario: initialize: %w", err)
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
		return fmt.Errorf("asana scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"stories", "task_projects", "tasks", "sections", "projects", "users", "workspaces", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("asana scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentUserGid', ?)", state.CurrentUserGid); err != nil {
		return fmt.Errorf("asana scenario: insert current user: %w", err)
	}
	for _, workspace := range state.Workspaces {
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspaces(gid, name, is_organization) VALUES(?, ?, ?)",
			workspace.Gid, workspace.Name, boolInt(workspace.IsOrganization)); err != nil {
			return fmt.Errorf("asana scenario: insert workspace %s: %w", workspace.Gid, err)
		}
	}
	for _, user := range state.Users {
		if _, err := tx.ExecContext(ctx, "INSERT INTO users(gid, name, email) VALUES(?, ?, ?)", user.Gid, user.Name, user.Email); err != nil {
			return fmt.Errorf("asana scenario: insert user %s: %w", user.Gid, err)
		}
	}
	for _, project := range state.Projects {
		if _, err := tx.ExecContext(ctx, "INSERT INTO projects(gid, workspace_gid, name, archived) VALUES(?, ?, ?, ?)",
			project.Gid, project.WorkspaceGid, project.Name, boolInt(project.Archived)); err != nil {
			return fmt.Errorf("asana scenario: insert project %s: %w", project.Gid, err)
		}
	}
	for _, section := range state.Sections {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sections(gid, project_gid, name, position) VALUES(?, ?, ?, ?)",
			section.Gid, section.ProjectGid, section.Name, section.Position); err != nil {
			return fmt.Errorf("asana scenario: insert section %s: %w", section.Gid, err)
		}
	}
	for _, task := range state.Tasks {
		createdAt := defaultTime(task.CreatedAt)
		modifiedAt := defaultTime(task.ModifiedAt)
		if modifiedAt == "" {
			modifiedAt = createdAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(gid, workspace_gid, name, notes, completed, assignee_gid, due_on, created_at, modified_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.Gid, task.WorkspaceGid, task.Name, task.Notes, boolInt(task.Completed),
			nullString(task.AssigneeGid), nullString(task.DueOn), createdAt, modifiedAt); err != nil {
			return fmt.Errorf("asana scenario: insert task %s: %w", task.Gid, err)
		}
		for _, projectGid := range task.ProjectGids {
			if _, err := tx.ExecContext(ctx, "INSERT INTO task_projects(task_gid, project_gid, section_gid) VALUES(?, ?, ?)",
				task.Gid, projectGid, nullString(task.SectionGid)); err != nil {
				return fmt.Errorf("asana scenario: insert task %s project %s: %w", task.Gid, projectGid, err)
			}
		}
	}
	for _, story := range state.Stories {
		subtype := story.ResourceSubtype
		if subtype == "" {
			subtype = "comment_added"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stories(gid, task_gid, text, created_by, created_at, resource_subtype)
			VALUES(?, ?, ?, ?, ?, ?)`, story.Gid, story.TaskGid, story.Text, story.CreatedBy, defaultTime(story.CreatedAt), subtype); err != nil {
			return fmt.Errorf("asana scenario: insert story %s: %w", story.Gid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asana scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserGid'").Scan(&state.CurrentUserGid); err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump current user: %w", err)
	}
	workspaceRows, err := db.QueryContext(ctx, "SELECT gid, name, is_organization FROM workspaces ORDER BY gid")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump workspaces: %w", err)
	}
	for workspaceRows.Next() {
		var workspace fixtureWorkspace
		var org int
		if err := workspaceRows.Scan(&workspace.Gid, &workspace.Name, &org); err != nil {
			workspaceRows.Close()
			return scenario.Document{}, err
		}
		workspace.IsOrganization = org != 0
		state.Workspaces = append(state.Workspaces, workspace)
	}
	if err := workspaceRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	userRows, err := db.QueryContext(ctx, "SELECT gid, name, email FROM users ORDER BY gid")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump users: %w", err)
	}
	for userRows.Next() {
		var user fixtureUser
		if err := userRows.Scan(&user.Gid, &user.Name, &user.Email); err != nil {
			userRows.Close()
			return scenario.Document{}, err
		}
		state.Users = append(state.Users, user)
	}
	if err := userRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	projectRows, err := db.QueryContext(ctx, "SELECT gid, workspace_gid, name, archived FROM projects ORDER BY gid")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump projects: %w", err)
	}
	for projectRows.Next() {
		var project fixtureProject
		var archived int
		if err := projectRows.Scan(&project.Gid, &project.WorkspaceGid, &project.Name, &archived); err != nil {
			projectRows.Close()
			return scenario.Document{}, err
		}
		project.Archived = archived != 0
		state.Projects = append(state.Projects, project)
	}
	if err := projectRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	sectionRows, err := db.QueryContext(ctx, "SELECT gid, project_gid, name, position FROM sections ORDER BY gid")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump sections: %w", err)
	}
	for sectionRows.Next() {
		var section fixtureSection
		if err := sectionRows.Scan(&section.Gid, &section.ProjectGid, &section.Name, &section.Position); err != nil {
			sectionRows.Close()
			return scenario.Document{}, err
		}
		state.Sections = append(state.Sections, section)
	}
	if err := sectionRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	taskRows, err := db.QueryContext(ctx, `SELECT gid, workspace_gid, name, notes, completed, assignee_gid, due_on, created_at, modified_at
		FROM tasks ORDER BY gid`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump tasks: %w", err)
	}
	var tasks []fixtureTask
	for taskRows.Next() {
		var task fixtureTask
		var completed int
		var assignee, dueOn sql.NullString
		if err := taskRows.Scan(&task.Gid, &task.WorkspaceGid, &task.Name, &task.Notes, &completed, &assignee, &dueOn, &task.CreatedAt, &task.ModifiedAt); err != nil {
			taskRows.Close()
			return scenario.Document{}, err
		}
		task.Completed = completed != 0
		task.AssigneeGid = assignee.String
		task.DueOn = dueOn.String
		tasks = append(tasks, task)
	}
	if err := taskRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := taskRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	for i := range tasks {
		task := &tasks[i]
		memberships, err := db.QueryContext(ctx, "SELECT project_gid, section_gid FROM task_projects WHERE task_gid=? ORDER BY project_gid", task.Gid)
		if err != nil {
			return scenario.Document{}, err
		}
		for memberships.Next() {
			var projectGid string
			var section sql.NullString
			if err := memberships.Scan(&projectGid, &section); err != nil {
				memberships.Close()
				return scenario.Document{}, err
			}
			task.ProjectGids = append(task.ProjectGids, projectGid)
			if task.SectionGid == "" {
				task.SectionGid = section.String
			}
		}
		if err := memberships.Close(); err != nil {
			return scenario.Document{}, err
		}
	}
	state.Tasks = tasks
	storyRows, err := db.QueryContext(ctx, "SELECT gid, task_gid, text, created_by, created_at, resource_subtype FROM stories ORDER BY gid")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("asana scenario: dump stories: %w", err)
	}
	defer storyRows.Close()
	for storyRows.Next() {
		var story fixtureStory
		if err := storyRows.Scan(&story.Gid, &story.TaskGid, &story.Text, &story.CreatedBy, &story.CreatedAt, &story.ResourceSubtype); err != nil {
			return scenario.Document{}, err
		}
		state.Stories = append(state.Stories, story)
	}
	if err := storyRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Workspaces == nil {
		state.Workspaces = []fixtureWorkspace{}
	}
	if state.Users == nil {
		state.Users = []fixtureUser{}
	}
	if state.Projects == nil {
		state.Projects = []fixtureProject{}
	}
	if state.Sections == nil {
		state.Sections = []fixtureSection{}
	}
	if state.Tasks == nil {
		state.Tasks = []fixtureTask{}
	}
	if state.Stories == nil {
		state.Stories = []fixtureStory{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "asana", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("asana scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("asana scenario: state has trailing data")
	}
	if state.Workspaces == nil || state.Users == nil || state.Projects == nil || state.Sections == nil || state.Tasks == nil || state.Stories == nil {
		return fixtureState{}, fmt.Errorf("asana scenario: workspaces, users, projects, sections, tasks, and stories are required arrays")
	}
	return state, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func defaultTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return "2026-08-01T00:00:00Z"
	}
	return value
}

func ContractName() string { return scenario.Contract }
