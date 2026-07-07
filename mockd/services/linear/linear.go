// Package linear is a stateful mock of the Linear GraphQL API subset a
// project-management agent uses: viewer, teams, users, workflowStates,
// issues (with a filter subset), issue-by-id, and the issueCreate /
// issueUpdate / commentCreate mutations. It serves a single POST
// /graphql endpoint and dispatches on the query text, the same
// pragmatic approach the railway service takes — it is not a full
// GraphQL executor, it answers the operations clients actually send.
//
// Because it is backed by SQLite, mutations really mutate: create an
// issue and the next issues query returns it with a fresh ENG-<n>
// identifier; move it with issueUpdate and its workflow state changes;
// comment and issue(id) shows the comment.
//
// Supported issues(filter:) shapes (ANDed, matched from variables):
//
//	{"filter":{"state":{"name":{"eq":"In Progress"}}}}
//	{"filter":{"state":{"type":{"eq":"started"}}}}
//	{"filter":{"team":{"key":{"eq":"ENG"}}}}
//	{"filter":{"assignee":{"null":true}}}
//	{"filter":{"assignee":{"email":{"eq":"kim@example.com"}}}}
//	{"filter":{"priority":{"lte":2}}}
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"viewer":{"id","name","email"},
//	 "teams":[{"id","key","name"}],
//	 "users":[{"id","name","email"}],
//	 "workflowStates":[{"id","teamId","name","type","position"}],
//	 "issues":[{"id","teamId","number","title","description","priority",
//	            "stateId","assigneeId","createdAt"}],
//	 "comments":[{"id","issueId","userId","body","createdAt"}]}
package linear

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the linear service.
func New() *mock.Service {
	s := mock.NewService("linear")
	s.Tables = []mock.Table{
		{Name: "meta", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "teams", DDL: "id TEXT PRIMARY KEY, key TEXT NOT NULL, name TEXT NOT NULL"},
		{Name: "users", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL"},
		{Name: "states", DDL: "id TEXT PRIMARY KEY, team_id TEXT NOT NULL, name TEXT NOT NULL, type TEXT NOT NULL, position INTEGER NOT NULL"},
		{Name: "issues", DDL: `
			id TEXT PRIMARY KEY,
			team_id TEXT NOT NULL,
			number INTEGER NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			state_id TEXT NOT NULL,
			assignee_id TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL`},
		{Name: "comments", DDL: "id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, user_id TEXT, body TEXT NOT NULL, created_at TEXT NOT NULL"},
	}
	s.Seed = seed

	s.POST("/graphql", handleGraphql)

	return s
}

// ---------- seed ----------

type fixture struct {
	Viewer json.RawMessage `json:"viewer"`
	Teams  []struct {
		ID, Key, Name string
	} `json:"teams"`
	Users []struct {
		ID, Name, Email string
	} `json:"users"`
	WorkflowStates []struct {
		ID       string `json:"id"`
		TeamID   string `json:"teamId"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Position int    `json:"position"`
	} `json:"workflowStates"`
	Issues []struct {
		ID          string `json:"id"`
		TeamID      string `json:"teamId"`
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		StateID     string `json:"stateId"`
		AssigneeID  string `json:"assigneeId"`
		CreatedAt   string `json:"createdAt"`
	} `json:"issues"`
	Comments []struct {
		ID        string `json:"id"`
		IssueID   string `json:"issueId"`
		UserID    string `json:"userId"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
	} `json:"comments"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("linear seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(f.Viewer) > 0 {
		if _, err := tx.Exec("INSERT OR REPLACE INTO meta(id, data) VALUES('viewer', ?)", string(f.Viewer)); err != nil {
			return err
		}
	}
	for _, t := range f.Teams {
		if _, err := tx.Exec("INSERT OR REPLACE INTO teams(id, key, name) VALUES(?,?,?)", t.ID, t.Key, t.Name); err != nil {
			return err
		}
	}
	for _, u := range f.Users {
		if _, err := tx.Exec("INSERT OR REPLACE INTO users(id, name, email) VALUES(?,?,?)", u.ID, u.Name, u.Email); err != nil {
			return err
		}
	}
	for _, st := range f.WorkflowStates {
		if _, err := tx.Exec("INSERT OR REPLACE INTO states(id, team_id, name, type, position) VALUES(?,?,?,?,?)",
			st.ID, st.TeamID, st.Name, st.Type, st.Position); err != nil {
			return err
		}
	}
	for _, i := range f.Issues {
		created := orNow(i.CreatedAt)
		var assignee any
		if i.AssigneeID != "" {
			assignee = i.AssigneeID
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO issues
			(id, team_id, number, title, description, priority, state_id, assignee_id, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			i.ID, i.TeamID, i.Number, i.Title, i.Description, i.Priority, i.StateID, assignee, created, created); err != nil {
			return err
		}
	}
	for _, cm := range f.Comments {
		if _, err := tx.Exec("INSERT OR REPLACE INTO comments(id, issue_id, user_id, body, created_at) VALUES(?,?,?,?,?)",
			cm.ID, cm.IssueID, cm.UserID, cm.Body, orNow(cm.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func orNow(s string) string {
	if s != "" {
		return s
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// ---------- graphql plumbing ----------

func gqlData(c *mock.Ctx, data any) error {
	return c.JSON(200, map[string]any{"data": data})
}

func gqlError(c *mock.Ctx, message string) error {
	return c.JSON(200, map[string]any{"errors": []any{map[string]any{"message": message}}})
}

// handleGraphql dispatches on the query text. Mutations are checked
// first (a mutation document also names the returned entity), then
// queries from most to least specific.
func handleGraphql(c *mock.Ctx) error {
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := c.Bind(&body); err != nil || strings.TrimSpace(body.Query) == "" {
		return gqlError(c, "query is required")
	}
	if body.Variables == nil {
		body.Variables = map[string]any{}
	}

	query := body.Query
	switch {
	case strings.Contains(query, "issueCreate"):
		return createIssue(c, body.Variables)
	case strings.Contains(query, "issueUpdate"):
		return updateIssue(c, body.Variables)
	case strings.Contains(query, "commentCreate"):
		return createComment(c, body.Variables)
	case strings.Contains(query, "mutation"):
		return gqlError(c, "unsupported mutation")
	case strings.Contains(query, "viewer"):
		return queryViewer(c)
	case strings.Contains(query, "workflowStates"):
		return queryWorkflowStates(c)
	case strings.Contains(query, "teams"):
		return queryTeams(c)
	case strings.Contains(query, "users"):
		return queryUsers(c)
	case strings.Contains(query, "issue("):
		return querySingleIssue(c, body.Variables)
	case strings.Contains(query, "issues"):
		return queryIssues(c, body.Variables)
	}
	return gqlError(c, "unsupported query")
}

// ---------- issue row → envelope ----------

type issueRow struct {
	id, teamID, title, description, stateID string
	assigneeID                              sql.NullString
	number, priority                        int
	createdAt, updatedAt                    string
}

const issueCols = "id, team_id, number, title, description, priority, state_id, assignee_id, created_at, updated_at"

func scanIssue(sc interface{ Scan(...any) error }) (*issueRow, error) {
	var r issueRow
	err := sc.Scan(&r.id, &r.teamID, &r.number, &r.title, &r.description, &r.priority,
		&r.stateID, &r.assigneeID, &r.createdAt, &r.updatedAt)
	return &r, err
}

// envelope resolves the issue's relations and renders the node shape
// clients select from: identifier, state, assignee, team.
func (r *issueRow) envelope(db *sql.DB) (map[string]any, error) {
	var teamKey, teamName string
	if err := db.QueryRow("SELECT key, name FROM teams WHERE id=?", r.teamID).Scan(&teamKey, &teamName); err != nil {
		return nil, fmt.Errorf("issue %s: team %q: %w", r.id, r.teamID, err)
	}
	var stateName, stateType string
	if err := db.QueryRow("SELECT name, type FROM states WHERE id=?", r.stateID).Scan(&stateName, &stateType); err != nil {
		return nil, fmt.Errorf("issue %s: state %q: %w", r.id, r.stateID, err)
	}
	out := map[string]any{
		"id":          r.id,
		"identifier":  fmt.Sprintf("%s-%d", teamKey, r.number),
		"number":      r.number,
		"title":       r.title,
		"description": r.description,
		"priority":    r.priority,
		"createdAt":   r.createdAt,
		"updatedAt":   r.updatedAt,
		"state":       map[string]any{"id": r.stateID, "name": stateName, "type": stateType},
		"team":        map[string]any{"id": r.teamID, "key": teamKey, "name": teamName},
		"assignee":    nil,
	}
	if r.assigneeID.Valid {
		var name, email string
		if err := db.QueryRow("SELECT name, email FROM users WHERE id=?", r.assigneeID.String).Scan(&name, &email); err == nil {
			out["assignee"] = map[string]any{"id": r.assigneeID.String, "name": name, "email": email}
		}
	}
	return out, nil
}

// ---------- queries ----------

func queryViewer(c *mock.Ctx) error {
	var data string
	err := c.DB.QueryRow("SELECT data FROM meta WHERE id='viewer'").Scan(&data)
	if err == sql.ErrNoRows {
		return gqlError(c, "authentication required")
	}
	if err != nil {
		return gqlError(c, err.Error())
	}
	return gqlData(c, map[string]any{"viewer": json.RawMessage(data)})
}

func queryTeams(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT id, key, name FROM teams ORDER BY key")
	if err != nil {
		return gqlError(c, err.Error())
	}
	defer rows.Close()
	nodes := []any{}
	for rows.Next() {
		var id, key, name string
		if err := rows.Scan(&id, &key, &name); err != nil {
			return gqlError(c, err.Error())
		}
		nodes = append(nodes, map[string]any{"id": id, "key": key, "name": name})
	}
	return gqlData(c, map[string]any{"teams": map[string]any{"nodes": nodes}})
}

func queryUsers(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT id, name, email FROM users ORDER BY name")
	if err != nil {
		return gqlError(c, err.Error())
	}
	defer rows.Close()
	nodes := []any{}
	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			return gqlError(c, err.Error())
		}
		nodes = append(nodes, map[string]any{"id": id, "name": name, "email": email})
	}
	return gqlData(c, map[string]any{"users": map[string]any{"nodes": nodes}})
}

func queryWorkflowStates(c *mock.Ctx) error {
	rows, err := c.DB.Query(`SELECT s.id, s.name, s.type, s.position, t.id, t.key
		FROM states s JOIN teams t ON t.id = s.team_id ORDER BY t.key, s.position`)
	if err != nil {
		return gqlError(c, err.Error())
	}
	defer rows.Close()
	nodes := []any{}
	for rows.Next() {
		var id, name, typ, teamID, teamKey string
		var pos int
		if err := rows.Scan(&id, &name, &typ, &pos, &teamID, &teamKey); err != nil {
			return gqlError(c, err.Error())
		}
		nodes = append(nodes, map[string]any{
			"id": id, "name": name, "type": typ, "position": pos,
			"team": map[string]any{"id": teamID, "key": teamKey},
		})
	}
	return gqlData(c, map[string]any{"workflowStates": map[string]any{"nodes": nodes}})
}

func querySingleIssue(c *mock.Ctx, variables map[string]any) error {
	id, _ := variables["id"].(string)
	if id == "" {
		return gqlError(c, "id variable is required")
	}
	r, err := findIssue(c.DB, id)
	if err == sql.ErrNoRows {
		return gqlError(c, "issue not found")
	}
	if err != nil {
		return gqlError(c, err.Error())
	}
	env, err := r.envelope(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	comments, err := loadComments(c.DB, r.id)
	if err != nil {
		return gqlError(c, err.Error())
	}
	env["comments"] = map[string]any{"nodes": comments}
	return gqlData(c, map[string]any{"issue": env})
}

// findIssue accepts either the opaque id or the human identifier
// ("ENG-104") — clients use both.
func findIssue(db *sql.DB, id string) (*issueRow, error) {
	row := db.QueryRow("SELECT "+issueCols+" FROM issues WHERE id=?", id)
	r, err := scanIssue(row)
	if err == nil || err != sql.ErrNoRows {
		return r, err
	}
	if key, num, ok := splitIdentifier(id); ok {
		row := db.QueryRow("SELECT "+issueCols+` FROM issues
			WHERE number=? AND team_id=(SELECT id FROM teams WHERE key=?)`, num, key)
		return scanIssue(row)
	}
	return nil, sql.ErrNoRows
}

func splitIdentifier(s string) (key string, num int, ok bool) {
	i := strings.LastIndex(s, "-")
	if i <= 0 {
		return "", 0, false
	}
	if _, err := fmt.Sscanf(s[i+1:], "%d", &num); err != nil {
		return "", 0, false
	}
	return s[:i], num, true
}

func loadComments(db *sql.DB, issueID string) ([]any, error) {
	rows, err := db.Query(`SELECT c.id, c.body, c.created_at, u.id, u.name
		FROM comments c LEFT JOIN users u ON u.id = c.user_id
		WHERE c.issue_id=? ORDER BY c.created_at, c.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, body, created string
		var userID, userName sql.NullString
		if err := rows.Scan(&id, &body, &created, &userID, &userName); err != nil {
			return nil, err
		}
		node := map[string]any{"id": id, "body": body, "createdAt": created, "user": nil}
		if userID.Valid {
			node["user"] = map[string]any{"id": userID.String, "name": userName.String}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func queryIssues(c *mock.Ctx, variables map[string]any) error {
	rows, err := c.DB.Query("SELECT " + issueCols + " FROM issues ORDER BY team_id, number")
	if err != nil {
		return gqlError(c, err.Error())
	}
	defer rows.Close()
	var issues []*issueRow
	for rows.Next() {
		r, err := scanIssue(rows)
		if err != nil {
			return gqlError(c, err.Error())
		}
		issues = append(issues, r)
	}

	filter, _ := variables["filter"].(map[string]any)
	nodes := []any{}
	for _, r := range issues {
		env, err := r.envelope(c.DB)
		if err != nil {
			return gqlError(c, err.Error())
		}
		if matchesFilter(env, filter) {
			nodes = append(nodes, env)
		}
	}
	if first, ok := variables["first"].(float64); ok && int(first) >= 0 && int(first) < len(nodes) {
		nodes = nodes[:int(first)]
	}
	return gqlData(c, map[string]any{"issues": map[string]any{"nodes": nodes}})
}

// matchesFilter applies the documented filter subset against a
// resolved issue envelope. Unknown filter keys are ignored — the mock
// answers what it understands rather than failing a whole query.
func matchesFilter(env map[string]any, filter map[string]any) bool {
	if filter == nil {
		return true
	}
	state, _ := env["state"].(map[string]any)
	team, _ := env["team"].(map[string]any)
	assignee, _ := env["assignee"].(map[string]any)

	if f, ok := filter["state"].(map[string]any); ok {
		if !matchStringField(f, "name", state["name"]) || !matchStringField(f, "type", state["type"]) {
			return false
		}
	}
	if f, ok := filter["team"].(map[string]any); ok {
		if !matchStringField(f, "key", team["key"]) {
			return false
		}
	}
	if f, ok := filter["assignee"].(map[string]any); ok {
		if wantNull, ok := f["null"].(bool); ok {
			if wantNull != (env["assignee"] == nil) {
				return false
			}
		}
		var email any
		if assignee != nil {
			email = assignee["email"]
		}
		if !matchStringField(f, "email", email) {
			return false
		}
	}
	if f, ok := filter["priority"].(map[string]any); ok {
		p, _ := env["priority"].(int)
		if eq, ok := f["eq"].(float64); ok && p != int(eq) {
			return false
		}
		if lte, ok := f["lte"].(float64); ok && p > int(lte) {
			return false
		}
		if gte, ok := f["gte"].(float64); ok && p < int(gte) {
			return false
		}
	}
	return true
}

// matchStringField checks a {"<field>":{"eq":"..."}} comparator if
// present. Missing comparator → pass.
func matchStringField(f map[string]any, field string, got any) bool {
	cmp, ok := f[field].(map[string]any)
	if !ok {
		return true
	}
	want, ok := cmp["eq"].(string)
	if !ok {
		return true
	}
	s, _ := got.(string)
	return strings.EqualFold(s, want)
}

// ---------- mutations ----------

func createIssue(c *mock.Ctx, variables map[string]any) error {
	input, _ := variables["input"].(map[string]any)
	if input == nil {
		return gqlError(c, "input is required")
	}
	title, _ := input["title"].(string)
	if strings.TrimSpace(title) == "" {
		return gqlError(c, "title is required")
	}
	teamID, _ := input["teamId"].(string)
	if teamID == "" {
		// Single-team fixtures can omit teamId.
		if err := c.DB.QueryRow("SELECT id FROM teams LIMIT 1").Scan(&teamID); err != nil {
			return gqlError(c, "teamId is required")
		}
	}
	stateID, _ := input["stateId"].(string)
	if stateID == "" {
		// Default to the team's first state by position (the backlog).
		if err := c.DB.QueryRow("SELECT id FROM states WHERE team_id=? ORDER BY position LIMIT 1", teamID).Scan(&stateID); err != nil {
			return gqlError(c, "team has no workflow states")
		}
	}
	var number int
	if err := c.DB.QueryRow("SELECT COALESCE(MAX(number),0)+1 FROM issues WHERE team_id=?", teamID).Scan(&number); err != nil {
		return gqlError(c, err.Error())
	}
	description, _ := input["description"].(string)
	priority := 0
	if p, ok := input["priority"].(float64); ok {
		priority = int(p)
	}
	var assignee any
	if a, ok := input["assigneeId"].(string); ok && a != "" {
		assignee = a
	}
	id := fmt.Sprintf("iss-%s-%d", strings.TrimPrefix(teamID, "team-"), number)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.DB.Exec(`INSERT INTO issues
		(id, team_id, number, title, description, priority, state_id, assignee_id, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, teamID, number, title, description, priority, stateID, assignee, now, now); err != nil {
		return gqlError(c, err.Error())
	}
	return issueMutationResult(c, "issueCreate", id)
}

func updateIssue(c *mock.Ctx, variables map[string]any) error {
	id, _ := variables["id"].(string)
	input, _ := variables["input"].(map[string]any)
	if id == "" || input == nil {
		return gqlError(c, "id and input are required")
	}
	r, err := findIssue(c.DB, id)
	if err == sql.ErrNoRows {
		return gqlError(c, "issue not found")
	}
	if err != nil {
		return gqlError(c, err.Error())
	}
	if v, ok := input["title"].(string); ok && v != "" {
		r.title = v
	}
	if v, ok := input["description"].(string); ok {
		r.description = v
	}
	if v, ok := input["priority"].(float64); ok {
		r.priority = int(v)
	}
	if v, ok := input["stateId"].(string); ok && v != "" {
		var n int
		if err := c.DB.QueryRow("SELECT COUNT(*) FROM states WHERE id=?", v).Scan(&n); err != nil || n == 0 {
			return gqlError(c, fmt.Sprintf("unknown stateId %q", v))
		}
		r.stateID = v
	}
	if v, ok := input["assigneeId"]; ok {
		switch a := v.(type) {
		case string:
			if a == "" {
				r.assigneeID = sql.NullString{}
			} else {
				r.assigneeID = sql.NullString{String: a, Valid: true}
			}
		case nil:
			r.assigneeID = sql.NullString{}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.DB.Exec(`UPDATE issues SET title=?, description=?, priority=?, state_id=?, assignee_id=?, updated_at=? WHERE id=?`,
		r.title, r.description, r.priority, r.stateID, nullable(r.assigneeID), now, r.id); err != nil {
		return gqlError(c, err.Error())
	}
	return issueMutationResult(c, "issueUpdate", r.id)
}

func createComment(c *mock.Ctx, variables map[string]any) error {
	input, _ := variables["input"].(map[string]any)
	if input == nil {
		return gqlError(c, "input is required")
	}
	issueID, _ := input["issueId"].(string)
	body, _ := input["body"].(string)
	if issueID == "" || strings.TrimSpace(body) == "" {
		return gqlError(c, "issueId and body are required")
	}
	r, err := findIssue(c.DB, issueID)
	if err == sql.ErrNoRows {
		return gqlError(c, "issue not found")
	}
	if err != nil {
		return gqlError(c, err.Error())
	}
	// Comments post as the viewer when the fixture defines one.
	var userID any
	var viewerData string
	if err := c.DB.QueryRow("SELECT data FROM meta WHERE id='viewer'").Scan(&viewerData); err == nil {
		var viewer struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(viewerData), &viewer) == nil && viewer.ID != "" {
			userID = viewer.ID
		}
	}
	var n int
	if err := c.DB.QueryRow("SELECT COUNT(*) FROM comments").Scan(&n); err != nil {
		return gqlError(c, err.Error())
	}
	id := fmt.Sprintf("cmt-%04d", n+1)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.DB.Exec("INSERT INTO comments(id, issue_id, user_id, body, created_at) VALUES(?,?,?,?,?)",
		id, r.id, userID, body, now); err != nil {
		return gqlError(c, err.Error())
	}
	return gqlData(c, map[string]any{"commentCreate": map[string]any{
		"success": true,
		"comment": map[string]any{"id": id, "body": body, "createdAt": now},
	}})
}

func issueMutationResult(c *mock.Ctx, field, id string) error {
	r, err := findIssue(c.DB, id)
	if err != nil {
		return gqlError(c, err.Error())
	}
	env, err := r.envelope(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	return gqlData(c, map[string]any{field: map[string]any{"success": true, "issue": env}})
}

func nullable(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}
