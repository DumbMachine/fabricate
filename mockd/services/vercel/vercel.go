// Package vercel is a stateful mock of the Vercel REST API subset that
// a cloud-audit agent inspects: current user, project listing (v10
// pagination shape), project environment variables, and team settings.
// Error responses use Vercel's {"error":{"code","message"}} shape.
//
// It is backed by SQLite, so the write endpoints (PATCH project for deployment
// protection / fork protection / directory listing / source maps, PATCH an env
// var's type, PATCH team SAML enforcement) actually mutate state — an agent can
// scan, fix through the same API shape the real Vercel exposes, and re-scan.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"user":{"id","username","email"},
//	 "teams":[{"id","slug","name","samlConfigured","samlEnforced"}],
//	 "projects":[{"id","name","ssoProtection","passwordProtection",
//	              "gitForkProtection","directoryListing","protectedSourcemaps",
//	              "deploymentExpiration","env":[{"id","key","type","target"}]}]}
package vercel

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the vercel service.
func New() *mock.Service {
	s := mock.NewService("vercel")
	s.Tables = []mock.Table{
		{Name: "account", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "teams", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "projects", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.GET("/v2/user", getUser)
	s.GET("/v10/projects", listProjects)
	s.PATCH("/v9/projects/{projectId}", patchProject)
	s.GET("/v9/projects/{projectId}/env", listEnv)
	s.PATCH("/v9/projects/{projectId}/env/{envId}", patchEnv)
	s.GET("/v2/teams/{teamId}", getTeam)
	s.PATCH("/v2/teams/{teamId}", patchTeam)

	return s
}

// ---------- error helper ----------

func apiError(c *mock.Ctx, status int, code, message string) error {
	return c.JSON(status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// ---------- fixture / documents ----------

type envDoc struct {
	ID     string   `json:"id"`
	Key    string   `json:"key"`
	Type   string   `json:"type"`
	Target []string `json:"target"`
}

type projectDoc struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	SsoProtection        json.RawMessage `json:"ssoProtection"`
	PasswordProtection   json.RawMessage `json:"passwordProtection"`
	GitForkProtection    *bool           `json:"gitForkProtection"`
	DirectoryListing     bool            `json:"directoryListing"`
	ProtectedSourcemaps  *bool           `json:"protectedSourcemaps"`
	DeploymentExpiration json.RawMessage `json:"deploymentExpiration"`
	Env                  []envDoc        `json:"env"`
}

type teamDoc struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	SamlConfigured bool   `json:"samlConfigured"`
	SamlEnforced   bool   `json:"samlEnforced"`
}

type fixture struct {
	User     json.RawMessage `json:"user"`
	Teams    []teamDoc       `json:"teams"`
	Projects []projectDoc    `json:"projects"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("vercel seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(f.User) > 0 {
		if _, err := tx.Exec("INSERT OR REPLACE INTO account(id, data) VALUES('user', ?)", string(f.User)); err != nil {
			return err
		}
	}
	for _, t := range f.Teams {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO teams(id, data) VALUES(?,?)", t.ID, string(data)); err != nil {
			return err
		}
	}
	for _, p := range f.Projects {
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO projects(id, name, data) VALUES(?,?,?)", p.ID, p.Name, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadProject(db *sql.DB, id string) (*projectDoc, error) {
	var data string
	err := db.QueryRow("SELECT data FROM projects WHERE id=? OR name=?", id, id).Scan(&data)
	if err != nil {
		return nil, err
	}
	var p projectDoc
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func saveProject(db *sql.DB, p *projectDoc) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE projects SET data=? WHERE id=?", string(data), p.ID)
	return err
}

func projectOr404(c *mock.Ctx) (*projectDoc, bool, error) {
	p, err := loadProject(c.DB, c.Params["projectId"])
	if err == sql.ErrNoRows {
		return nil, false, apiError(c, 404, "not_found", "Project not found")
	}
	if err != nil {
		return nil, false, apiError(c, 500, "internal_error", err.Error())
	}
	return p, true, nil
}

// projectEnvelope mirrors the fields an audit client reads from GET /v10/projects.
func projectEnvelope(p *projectDoc) map[string]any {
	gitForkProtection := true
	if p.GitForkProtection != nil {
		gitForkProtection = *p.GitForkProtection
	}
	envelope := map[string]any{
		"id":                   p.ID,
		"name":                 p.Name,
		"ssoProtection":        nullableRaw(p.SsoProtection),
		"passwordProtection":   nullableRaw(p.PasswordProtection),
		"gitForkProtection":    gitForkProtection,
		"directoryListing":     p.DirectoryListing,
		"deploymentExpiration": nullableRaw(p.DeploymentExpiration),
	}
	if p.ProtectedSourcemaps != nil {
		envelope["protectedSourcemaps"] = *p.ProtectedSourcemaps
	}
	return envelope
}

func nullableRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// ---------- handlers ----------

func getUser(c *mock.Ctx) error {
	var data string
	err := c.DB.QueryRow("SELECT data FROM account WHERE id='user'").Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 401, "forbidden", "Not authorized")
	}
	if err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	return c.JSON(200, map[string]any{"user": json.RawMessage(data)})
}

func listProjects(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT data FROM projects ORDER BY name")
	if err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	defer rows.Close()
	projects := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, "internal_error", err.Error())
		}
		var p projectDoc
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return apiError(c, 500, "internal_error", err.Error())
		}
		projects = append(projects, projectEnvelope(&p))
	}
	return c.JSON(200, map[string]any{
		"projects":   projects,
		"pagination": map[string]any{"count": len(projects), "next": nil, "prev": nil},
	})
}

func patchProject(c *mock.Ctx) error {
	var body struct {
		SsoProtection        json.RawMessage `json:"ssoProtection"`
		PasswordProtection   json.RawMessage `json:"passwordProtection"`
		GitForkProtection    *bool           `json:"gitForkProtection"`
		DirectoryListing     *bool           `json:"directoryListing"`
		ProtectedSourcemaps  *bool           `json:"protectedSourcemaps"`
		DeploymentExpiration json.RawMessage `json:"deploymentExpiration"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "bad_request", "invalid request body")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	if len(body.SsoProtection) > 0 {
		p.SsoProtection = normalizeNull(body.SsoProtection)
	}
	if len(body.PasswordProtection) > 0 {
		p.PasswordProtection = normalizeNull(body.PasswordProtection)
	}
	if body.GitForkProtection != nil {
		p.GitForkProtection = body.GitForkProtection
	}
	if body.DirectoryListing != nil {
		p.DirectoryListing = *body.DirectoryListing
	}
	if body.ProtectedSourcemaps != nil {
		p.ProtectedSourcemaps = body.ProtectedSourcemaps
	}
	if len(body.DeploymentExpiration) > 0 {
		p.DeploymentExpiration = normalizeNull(body.DeploymentExpiration)
	}
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	return c.JSON(200, projectEnvelope(p))
}

// normalizeNull maps a JSON literal null to an empty RawMessage so envelopes
// render it as null consistently.
func normalizeNull(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}

func listEnv(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	envs := []any{}
	for _, variable := range p.Env {
		envs = append(envs, variable)
	}
	return c.JSON(200, map[string]any{"envs": envs})
}

func patchEnv(c *mock.Ctx) error {
	var body struct {
		Type *string `json:"type"`
	}
	if err := c.Bind(&body); err != nil || body.Type == nil {
		return apiError(c, 400, "bad_request", "type is required")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	envID := c.Params["envId"]
	for i := range p.Env {
		if p.Env[i].ID == envID || p.Env[i].Key == envID {
			p.Env[i].Type = *body.Type
			if err := saveProject(c.DB, p); err != nil {
				return apiError(c, 500, "internal_error", err.Error())
			}
			return c.JSON(200, p.Env[i])
		}
	}
	return apiError(c, 404, "not_found", "Environment variable not found")
}

func teamEnvelope(t *teamDoc) map[string]any {
	envelope := map[string]any{"id": t.ID, "slug": t.Slug, "name": t.Name}
	if t.SamlConfigured {
		envelope["saml"] = map[string]any{
			"connection": map[string]any{"status": "linked"},
			"enforced":   t.SamlEnforced,
		}
	}
	return envelope
}

func getTeam(c *mock.Ctx) error {
	var data string
	err := c.DB.QueryRow("SELECT data FROM teams WHERE id=?", c.Params["teamId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 404, "not_found", "Team not found")
	}
	if err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	var t teamDoc
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	return c.JSON(200, teamEnvelope(&t))
}

func patchTeam(c *mock.Ctx) error {
	var body struct {
		Saml *struct {
			Enforced *bool `json:"enforced"`
		} `json:"saml"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "bad_request", "invalid request body")
	}
	var data string
	err := c.DB.QueryRow("SELECT data FROM teams WHERE id=?", c.Params["teamId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 404, "not_found", "Team not found")
	}
	if err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	var t teamDoc
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	if body.Saml != nil && body.Saml.Enforced != nil {
		t.SamlEnforced = *body.Saml.Enforced
	}
	updated, err := json.Marshal(t)
	if err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	if _, err := c.DB.Exec("UPDATE teams SET data=? WHERE id=?", string(updated), t.ID); err != nil {
		return apiError(c, 500, "internal_error", err.Error())
	}
	return c.JSON(200, teamEnvelope(&t))
}
