package asana

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/asana/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const apiPrefix = "/api/1.0"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("asana: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("asana: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("asana: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("asana: load OpenAPI: %w", err)
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
	inner := validator(generatedHandler)
	impl.handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, apiPrefix+"/") || request.URL.Path == apiPrefix {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = strings.TrimPrefix(request.URL.Path, apiPrefix)
			if cloned.URL.Path == "" {
				cloned.URL.Path = "/"
			}
			inner.ServeHTTP(w, cloned)
			return
		}
		inner.ServeHTTP(w, request)
	})
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) GetWorkspaces(ctx context.Context, _ generated.GetWorkspacesRequestObject) (generated.GetWorkspacesResponseObject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT gid, name FROM workspaces ORDER BY gid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.WorkspaceCompact{}
	for rows.Next() {
		var gid, name string
		if err := rows.Scan(&gid, &name); err != nil {
			return nil, err
		}
		items = append(items, workspaceCompact(gid, name))
	}
	return generated.GetWorkspaces200JSONResponse{Data: &items}, rows.Err()
}

func (s *server) GetWorkspace(ctx context.Context, request generated.GetWorkspaceRequestObject) (generated.GetWorkspaceResponseObject, error) {
	workspace, err := s.loadWorkspace(ctx, string(request.WorkspaceGid))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetWorkspace404JSONResponse{NotFoundJSONResponse: notFound("workspace not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetWorkspace200JSONResponse{Data: workspace}, nil
}

func (s *server) GetUsers(ctx context.Context, request generated.GetUsersRequestObject) (generated.GetUsersResponseObject, error) {
	query := "SELECT gid, name, email FROM users"
	args := []any{}
	if request.Params.Workspace != nil && *request.Params.Workspace != "" {
		query += " WHERE EXISTS (SELECT 1 FROM workspaces WHERE gid=?)"
		args = append(args, *request.Params.Workspace)
	}
	query += " ORDER BY gid"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.UserCompact{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, user.compact())
	}
	return generated.GetUsers200JSONResponse{Data: &items}, rows.Err()
}

func (s *server) GetUser(ctx context.Context, request generated.GetUserRequestObject) (generated.GetUserResponseObject, error) {
	user, err := s.loadUser(ctx, string(request.UserGid))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetUser404JSONResponse{NotFoundJSONResponse: notFound("user not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetUser200JSONResponse{Data: user.response()}, nil
}

func (s *server) GetProjects(ctx context.Context, request generated.GetProjectsRequestObject) (generated.GetProjectsResponseObject, error) {
	items, err := s.listProjects(ctx, value(request.Params.Workspace), request.Params.Archived)
	if err != nil {
		return nil, err
	}
	return generated.GetProjects200JSONResponse{Data: &items}, nil
}

func (s *server) GetProjectsForWorkspace(ctx context.Context, request generated.GetProjectsForWorkspaceRequestObject) (generated.GetProjectsForWorkspaceResponseObject, error) {
	if _, err := s.loadWorkspace(ctx, string(request.WorkspaceGid)); errors.Is(err, sql.ErrNoRows) {
		return generated.GetProjectsForWorkspace404JSONResponse{NotFoundJSONResponse: notFound("workspace not found")}, nil
	} else if err != nil {
		return nil, err
	}
	items, err := s.listProjects(ctx, string(request.WorkspaceGid), request.Params.Archived)
	if err != nil {
		return nil, err
	}
	return generated.GetProjectsForWorkspace200JSONResponse{Data: &items}, nil
}

func (s *server) GetProject(ctx context.Context, request generated.GetProjectRequestObject) (generated.GetProjectResponseObject, error) {
	project, err := s.loadProject(ctx, string(request.ProjectGid))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetProject404JSONResponse{NotFoundJSONResponse: notFound("project not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetProject200JSONResponse{Data: project.response()}, nil
}

func (s *server) GetSectionsForProject(ctx context.Context, request generated.GetSectionsForProjectRequestObject) (generated.GetSectionsForProjectResponseObject, error) {
	if _, err := s.loadProject(ctx, string(request.ProjectGid)); errors.Is(err, sql.ErrNoRows) {
		return generated.GetSectionsForProject404JSONResponse{NotFoundJSONResponse: notFound("project not found")}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT gid, name FROM sections WHERE project_gid=? ORDER BY position, gid", request.ProjectGid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.SectionCompact{}
	for rows.Next() {
		var gid, name string
		if err := rows.Scan(&gid, &name); err != nil {
			return nil, err
		}
		items = append(items, generated.SectionCompact{Gid: ptr(gid), Name: ptr(name), ResourceType: ptr("section")})
	}
	return generated.GetSectionsForProject200JSONResponse{Data: &items}, rows.Err()
}

func (s *server) GetTasks(ctx context.Context, request generated.GetTasksRequestObject) (generated.GetTasksResponseObject, error) {
	tasks, err := s.listTasks(ctx, value(request.Params.Project), value(request.Params.Assignee), value(request.Params.Workspace), value(request.Params.Section))
	if err != nil {
		return nil, err
	}
	items := make([]generated.TaskCompact, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, task.compact())
	}
	return generated.GetTasks200JSONResponse{Data: &items}, nil
}

func (s *server) GetTasksForProject(ctx context.Context, request generated.GetTasksForProjectRequestObject) (generated.GetTasksForProjectResponseObject, error) {
	if _, err := s.loadProject(ctx, string(request.ProjectGid)); errors.Is(err, sql.ErrNoRows) {
		return generated.GetTasksForProject404JSONResponse{NotFoundJSONResponse: notFound("project not found")}, nil
	} else if err != nil {
		return nil, err
	}
	tasks, err := s.listTasks(ctx, string(request.ProjectGid), "", "", "")
	if err != nil {
		return nil, err
	}
	items := make([]generated.TaskCompact, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, task.compact())
	}
	return generated.GetTasksForProject200JSONResponse{Data: &items}, nil
}

func (s *server) GetTask(ctx context.Context, request generated.GetTaskRequestObject) (generated.GetTaskResponseObject, error) {
	task, err := s.loadTask(ctx, string(request.TaskGid))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetTask404JSONResponse{NotFoundJSONResponse: notFound("task not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.taskResponse(ctx, task)
	if err != nil {
		return nil, err
	}
	return generated.GetTask200JSONResponse{Data: resp}, nil
}

func (s *server) CreateTask(ctx context.Context, request generated.CreateTaskRequestObject) (generated.CreateTaskResponseObject, error) {
	if request.Body == nil || request.Body.Data == nil {
		return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("data is required")}, nil
	}
	body := request.Body.Data
	name := strings.TrimSpace(value(body.Name))
	if name == "" {
		return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("name is required")}, nil
	}
	workspaceGid := value(body.Workspace)
	projectGids := sliceValue(body.Projects)
	if workspaceGid == "" && len(projectGids) > 0 {
		project, err := s.loadProject(ctx, projectGids[0])
		if err != nil {
			return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("project not found")}, nil
		}
		workspaceGid = project.WorkspaceGid
	}
	if workspaceGid == "" {
		return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("workspace or projects is required")}, nil
	}
	if _, err := s.loadWorkspace(ctx, workspaceGid); errors.Is(err, sql.ErrNoRows) {
		return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("workspace not found")}, nil
	} else if err != nil {
		return nil, err
	}
	for _, projectGid := range projectGids {
		if _, err := s.loadProject(ctx, projectGid); errors.Is(err, sql.ErrNoRows) {
			return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("project not found")}, nil
		} else if err != nil {
			return nil, err
		}
	}
	assignee, err := s.resolveAssignee(ctx, body.Assignee)
	if err != nil {
		return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest(err.Error())}, nil
	}
	id, err := s.ids.Next(ctx, "asana.task")
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO tasks
		(gid, workspace_gid, name, notes, completed, assignee_gid, due_on, created_at, modified_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, workspaceGid, name, value(body.Notes), boolInt(boolValue(body.Completed)),
		nullString(assignee), nil, now, now); err != nil {
		return nil, err
	}
	for _, projectGid := range projectGids {
		if _, err := s.db.ExecContext(ctx, "INSERT INTO task_projects(task_gid, project_gid) VALUES(?, ?)", id, projectGid); err != nil {
			return generated.CreateTask400JSONResponse{BadRequestJSONResponse: badRequest("project not found")}, nil
		}
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}
	resp, err := s.taskResponse(ctx, task)
	if err != nil {
		return nil, err
	}
	return generated.CreateTask201JSONResponse{Data: resp}, nil
}

func (s *server) UpdateTask(ctx context.Context, request generated.UpdateTaskRequestObject) (generated.UpdateTaskResponseObject, error) {
	task, err := s.loadTask(ctx, string(request.TaskGid))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UpdateTask404JSONResponse{NotFoundJSONResponse: notFound("task not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil || request.Body.Data == nil {
		return generated.UpdateTask400JSONResponse{BadRequestJSONResponse: badRequest("data is required")}, nil
	}
	body := request.Body.Data
	if body.Name != nil {
		task.Name = *body.Name
	}
	if body.Notes != nil {
		task.Notes = *body.Notes
	}
	if body.Completed != nil {
		task.Completed = *body.Completed
	}
	if body.Assignee != nil {
		assignee, err := s.resolveAssignee(ctx, body.Assignee)
		if err != nil {
			return generated.UpdateTask400JSONResponse{BadRequestJSONResponse: badRequest(err.Error())}, nil
		}
		task.AssigneeGid = assignee
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET name=?, notes=?, completed=?, assignee_gid=?, modified_at=? WHERE gid=?`,
		task.Name, task.Notes, boolInt(task.Completed), nullString(task.AssigneeGid), now, task.Gid); err != nil {
		return nil, err
	}
	task, err = s.loadTask(ctx, task.Gid)
	if err != nil {
		return nil, err
	}
	resp, err := s.taskResponse(ctx, task)
	if err != nil {
		return nil, err
	}
	return generated.UpdateTask200JSONResponse{Data: resp}, nil
}

func (s *server) GetStoriesForTask(ctx context.Context, request generated.GetStoriesForTaskRequestObject) (generated.GetStoriesForTaskResponseObject, error) {
	if _, err := s.loadTask(ctx, string(request.TaskGid)); errors.Is(err, sql.ErrNoRows) {
		return generated.GetStoriesForTask404JSONResponse{NotFoundJSONResponse: notFound("task not found")}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT gid, text, created_by, created_at, resource_subtype
		FROM stories WHERE task_gid=? ORDER BY created_at, gid`, request.TaskGid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.StoryCompact{}
	for rows.Next() {
		story, err := scanStory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, story.compact())
	}
	return generated.GetStoriesForTask200JSONResponse{Data: &items}, rows.Err()
}

func (s *server) CreateStoryForTask(ctx context.Context, request generated.CreateStoryForTaskRequestObject) (generated.CreateStoryForTaskResponseObject, error) {
	if _, err := s.loadTask(ctx, string(request.TaskGid)); errors.Is(err, sql.ErrNoRows) {
		return generated.CreateStoryForTask404JSONResponse{NotFoundJSONResponse: notFound("task not found")}, nil
	} else if err != nil {
		return nil, err
	}
	if request.Body == nil || request.Body.Data == nil || value(request.Body.Data.Text) == "" {
		return generated.CreateStoryForTask400JSONResponse{BadRequestJSONResponse: badRequest("text is required")}, nil
	}
	id, err := s.ids.Next(ctx, "asana.story")
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	me, err := s.currentUserGid(ctx)
	if err != nil {
		return nil, err
	}
	text := value(request.Body.Data.Text)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO stories(gid, task_gid, text, created_by, created_at, resource_subtype)
		VALUES(?, ?, ?, ?, ?, 'comment_added')`, id, request.TaskGid, text, me, now); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE tasks SET modified_at=? WHERE gid=?", now, request.TaskGid); err != nil {
		return nil, err
	}
	user, err := s.loadUser(ctx, me)
	if err != nil {
		return nil, err
	}
	createdAt, _ := time.Parse(time.RFC3339, now)
	storyType := generated.Comment
	return generated.CreateStoryForTask201JSONResponse{Data: &generated.StoryResponse{
		Gid: ptr(id), ResourceType: ptr("story"), ResourceSubtype: ptr("comment_added"),
		Text: ptr(text), CreatedAt: &createdAt, CreatedBy: ptr(user.compact()), Type: &storyType,
	}}, nil
}

type storedUser struct{ Gid, Name, Email string }

func (u storedUser) compact() generated.UserCompact {
	return generated.UserCompact{Gid: ptr(u.Gid), Name: ptr(u.Name), ResourceType: ptr("user")}
}

func (u storedUser) response() *generated.UserResponse {
	email := openapi_types.Email(u.Email)
	return &generated.UserResponse{Gid: ptr(u.Gid), Name: ptr(u.Name), Email: &email, ResourceType: ptr("user")}
}

type storedProject struct {
	Gid, WorkspaceGid, Name string
	Archived                bool
}

func (p storedProject) compact() generated.ProjectCompact {
	return generated.ProjectCompact{Gid: ptr(p.Gid), Name: ptr(p.Name), ResourceType: ptr("project")}
}

func (p storedProject) response() *generated.ProjectResponse {
	archived := p.Archived
	return &generated.ProjectResponse{
		Gid: ptr(p.Gid), Name: ptr(p.Name), ResourceType: ptr("project"), Archived: &archived,
		Workspace: &struct {
			Gid          *string `json:"gid,omitempty"`
			Name         *string `json:"name,omitempty"`
			ResourceType *string `json:"resource_type,omitempty"`
		}{Gid: ptr(p.WorkspaceGid), ResourceType: ptr("workspace")},
	}
}

type storedTask struct {
	Gid, WorkspaceGid, Name, Notes, AssigneeGid, DueOn, CreatedAt, ModifiedAt string
	Completed                                                                 bool
	ProjectGids                                                               []string
}

func (t storedTask) compact() generated.TaskCompact {
	return generated.TaskCompact{Gid: ptr(t.Gid), Name: ptr(t.Name), ResourceType: ptr("task")}
}

type storedStory struct {
	Gid, Text, CreatedBy, CreatedAt, ResourceSubtype string
}

func (s storedStory) compact() generated.StoryCompact {
	createdAt, _ := time.Parse(time.RFC3339, s.CreatedAt)
	return generated.StoryCompact{
		Gid: ptr(s.Gid), Text: ptr(s.Text), ResourceType: ptr("story"),
		ResourceSubtype: ptr(s.ResourceSubtype), CreatedAt: &createdAt,
	}
}

func (s *server) loadWorkspace(ctx context.Context, gid string) (*generated.WorkspaceResponse, error) {
	var name string
	var org int
	if err := s.db.QueryRowContext(ctx, "SELECT name, is_organization FROM workspaces WHERE gid=?", gid).Scan(&name, &org); err != nil {
		return nil, err
	}
	isOrg := org != 0
	return &generated.WorkspaceResponse{Gid: ptr(gid), Name: ptr(name), ResourceType: ptr("workspace"), IsOrganization: &isOrg}, nil
}

func (s *server) loadUser(ctx context.Context, gid string) (storedUser, error) {
	if gid == "me" {
		var err error
		gid, err = s.currentUserGid(ctx)
		if err != nil {
			return storedUser{}, err
		}
	}
	var user storedUser
	err := s.db.QueryRowContext(ctx, "SELECT gid, name, email FROM users WHERE gid=?", gid).Scan(&user.Gid, &user.Name, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, "SELECT gid, name, email FROM users WHERE email=?", gid).Scan(&user.Gid, &user.Name, &user.Email)
	}
	return user, err
}

func (s *server) currentUserGid(ctx context.Context) (string, error) {
	var gid string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserGid'").Scan(&gid)
	return gid, err
}

func (s *server) loadProject(ctx context.Context, gid string) (storedProject, error) {
	var project storedProject
	var archived int
	err := s.db.QueryRowContext(ctx, "SELECT gid, workspace_gid, name, archived FROM projects WHERE gid=?", gid).
		Scan(&project.Gid, &project.WorkspaceGid, &project.Name, &archived)
	project.Archived = archived != 0
	return project, err
}

func (s *server) listProjects(ctx context.Context, workspace string, archived *generated.ArchivedQueryParam) ([]generated.ProjectCompact, error) {
	query := "SELECT gid, workspace_gid, name, archived FROM projects"
	args := []any{}
	clauses := []string{}
	if workspace != "" {
		clauses = append(clauses, "workspace_gid=?")
		args = append(args, workspace)
	}
	if archived != nil {
		clauses = append(clauses, "archived=?")
		args = append(args, boolInt(bool(*archived)))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY gid"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.ProjectCompact{}
	for rows.Next() {
		var project storedProject
		var flag int
		if err := rows.Scan(&project.Gid, &project.WorkspaceGid, &project.Name, &flag); err != nil {
			return nil, err
		}
		items = append(items, project.compact())
	}
	return items, rows.Err()
}

func (s *server) loadTask(ctx context.Context, gid string) (storedTask, error) {
	var task storedTask
	var completed int
	var assignee, dueOn sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT gid, workspace_gid, name, notes, completed, assignee_gid, due_on, created_at, modified_at
		FROM tasks WHERE gid=?`, gid).Scan(&task.Gid, &task.WorkspaceGid, &task.Name, &task.Notes, &completed, &assignee, &dueOn, &task.CreatedAt, &task.ModifiedAt)
	if err != nil {
		return storedTask{}, err
	}
	task.Completed = completed != 0
	task.AssigneeGid = assignee.String
	task.DueOn = dueOn.String
	rows, err := s.db.QueryContext(ctx, "SELECT project_gid FROM task_projects WHERE task_gid=? ORDER BY project_gid", gid)
	if err != nil {
		return storedTask{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var projectGid string
		if err := rows.Scan(&projectGid); err != nil {
			return storedTask{}, err
		}
		task.ProjectGids = append(task.ProjectGids, projectGid)
	}
	return task, rows.Err()
}

func (s *server) listTasks(ctx context.Context, project, assignee, workspace, section string) ([]storedTask, error) {
	query := `SELECT DISTINCT t.gid FROM tasks t`
	clauses := []string{}
	args := []any{}
	if project != "" || section != "" {
		query += " JOIN task_projects tp ON tp.task_gid=t.gid"
	}
	if project != "" {
		clauses = append(clauses, "tp.project_gid=?")
		args = append(args, project)
	}
	if section != "" {
		clauses = append(clauses, "tp.section_gid=?")
		args = append(args, section)
	}
	if assignee != "" {
		resolved, err := s.resolveAssignee(ctx, &assignee)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, "t.assignee_gid=?")
		args = append(args, resolved)
	}
	if workspace != "" {
		clauses = append(clauses, "t.workspace_gid=?")
		args = append(args, workspace)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY t.gid"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []storedTask
	for rows.Next() {
		var gid string
		if err := rows.Scan(&gid); err != nil {
			return nil, err
		}
		task, err := s.loadTask(ctx, gid)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *server) taskResponse(ctx context.Context, task storedTask) (*generated.TaskResponse, error) {
	completed := task.Completed
	createdAt, _ := time.Parse(time.RFC3339, task.CreatedAt)
	modifiedAt, _ := time.Parse(time.RFC3339, task.ModifiedAt)
	resp := &generated.TaskResponse{
		Gid: ptr(task.Gid), Name: ptr(task.Name), Notes: ptr(task.Notes),
		Completed: &completed, ResourceType: ptr("task"),
		CreatedAt: &createdAt, ModifiedAt: &modifiedAt,
		Workspace: &struct {
			Gid          *string `json:"gid,omitempty"`
			Name         *string `json:"name,omitempty"`
			ResourceType *string `json:"resource_type,omitempty"`
		}{Gid: ptr(task.WorkspaceGid), ResourceType: ptr("workspace")},
	}
	if task.AssigneeGid != "" {
		user, err := s.loadUser(ctx, task.AssigneeGid)
		if err != nil {
			return nil, err
		}
		compact := user.compact()
		resp.Assignee = &struct {
			Gid          *string `json:"gid,omitempty"`
			Name         *string `json:"name,omitempty"`
			ResourceType *string `json:"resource_type,omitempty"`
		}{Gid: compact.Gid, Name: compact.Name, ResourceType: compact.ResourceType}
	}
	projects := []generated.ProjectCompact{}
	for _, projectGid := range task.ProjectGids {
		project, err := s.loadProject(ctx, projectGid)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project.compact())
	}
	if len(projects) > 0 {
		resp.Projects = &projects
	}
	return resp, nil
}

func (s *server) resolveAssignee(ctx context.Context, assignee *string) (string, error) {
	if assignee == nil || *assignee == "" || *assignee == "null" {
		return "", nil
	}
	user, err := s.loadUser(ctx, *assignee)
	if err != nil {
		return "", fmt.Errorf("assignee not found")
	}
	return user.Gid, nil
}

func scanUser(row interface{ Scan(dest ...any) error }) (storedUser, error) {
	var user storedUser
	err := row.Scan(&user.Gid, &user.Name, &user.Email)
	return user, err
}

func scanStory(row interface{ Scan(dest ...any) error }) (storedStory, error) {
	var story storedStory
	err := row.Scan(&story.Gid, &story.Text, &story.CreatedBy, &story.CreatedAt, &story.ResourceSubtype)
	return story, err
}

func workspaceCompact(gid, name string) generated.WorkspaceCompact {
	return generated.WorkspaceCompact{Gid: ptr(gid), Name: ptr(name), ResourceType: ptr("workspace")}
}

func notFound(message string) generated.NotFoundJSONResponse {
	return generated.NotFoundJSONResponse(errorBody(message))
}

func badRequest(message string) generated.BadRequestJSONResponse {
	return generated.BadRequestJSONResponse(errorBody(message))
}

func errorBody(message string) generated.ErrorResponse {
	return generated.ErrorResponse{Errors: &[]generated.Error{{Message: ptr(message)}}}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody(message))
}

func ptr[T any](value T) *T { return &value }

func value[T ~string](pointer *T) string {
	if pointer == nil {
		return ""
	}
	return string(*pointer)
}

func sliceValue[T any](pointer *[]T) []T {
	if pointer == nil {
		return nil
	}
	return *pointer
}

func boolValue(pointer *bool) bool { return pointer != nil && *pointer }
