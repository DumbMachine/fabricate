// Package railway is a stateful mock of Railway's GraphQL API subset
// (backboard.railway.com/graphql/v2) that a cloud-audit agent typically
// inspects: the current user, the Relay-style project tree (services +
// environments), per-service TCP proxies and variables, and workspace
// settings/members. It serves a single POST /graphql/v2 endpoint and
// dispatches on the query text the way the plugin issues it.
//
// It is backed by SQLite, so the write mutations actually mutate state — an
// agent can scan, fix through the same API shapes the real provider exposes,
// and re-scan:
//
//   - mutation tcpProxyDelete(id) — remove a database's public TCP proxy
//   - mutation variableDelete(input:{projectId, environmentId, serviceId,
//     name}) — remove a plain variable (sealing is dashboard-only on the real
//     platform, so deletion is the API-shaped fix)
//
// Workspace 2FA enforcement and member roles are intentionally read-only,
// matching the real platform (dashboard-only operations).
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"me":{"id","name","email"},
//	 "workspaces":[{"id","name","enforceTwoFactor","members":[...]}],
//	 "projects":[{"id","name","environments":[...],
//	              "services":[{"id","name","tcpProxies":[...],"variables":{...}}]}]}
package railway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the railway service.
func New() *mock.Service {
	s := mock.NewService("railway")
	s.Tables = []mock.Table{
		{Name: "meta", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "projects", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.POST("/graphql/v2", handleGraphql)

	return s
}

// ---------- fixture / documents ----------

type tcpProxyDoc struct {
	ID              string `json:"id"`
	ProxyPort       int    `json:"proxyPort"`
	ApplicationPort int    `json:"applicationPort"`
	Domain          string `json:"domain"`
}

type serviceDoc struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	TcpProxies []tcpProxyDoc     `json:"tcpProxies"`
	Variables  map[string]string `json:"variables"`
}

type environmentDoc struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type projectDoc struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Environments []environmentDoc `json:"environments"`
	Services     []serviceDoc     `json:"services"`
}

type memberDoc struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role"`
}

type workspaceDoc struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	EnforceTwoFactor bool        `json:"enforceTwoFactor"`
	Members          []memberDoc `json:"members"`
}

type fixture struct {
	Me         json.RawMessage `json:"me"`
	Workspaces []workspaceDoc  `json:"workspaces"`
	Projects   []projectDoc    `json:"projects"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("railway seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(f.Me) > 0 {
		if _, err := tx.Exec("INSERT OR REPLACE INTO meta(id, data) VALUES('me', ?)", string(f.Me)); err != nil {
			return err
		}
	}
	workspaces, err := json.Marshal(f.Workspaces)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO meta(id, data) VALUES('workspaces', ?)", string(workspaces)); err != nil {
		return err
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

// ---------- graphql plumbing ----------

func gqlData(c *mock.Ctx, data any) error {
	return c.JSON(200, map[string]any{"data": data})
}

func gqlError(c *mock.Ctx, message string) error {
	return c.JSON(200, map[string]any{"errors": []any{map[string]any{"message": message}}})
}

func loadMeta(db *sql.DB, id string) (string, error) {
	var data string
	err := db.QueryRow("SELECT data FROM meta WHERE id=?", id).Scan(&data)
	return data, err
}

func loadProjects(db *sql.DB) ([]projectDoc, error) {
	rows, err := db.Query("SELECT data FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []projectDoc{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p projectDoc
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func saveProject(db *sql.DB, p *projectDoc) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE projects SET data=? WHERE id=?", string(data), p.ID)
	return err
}

func connection(nodes []any) map[string]any {
	edges := make([]any, 0, len(nodes))
	for _, node := range nodes {
		edges = append(edges, map[string]any{"node": node})
	}
	return map[string]any{"edges": edges}
}

func stringVariable(variables map[string]any, key string) string {
	if value, ok := variables[key].(string); ok {
		return value
	}
	return ""
}

// handleGraphql dispatches on the query text the plugin sends. Queries the
// plugin does not issue get a GraphQL error, mirroring an unknown-field
// response from the real API.
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
	case strings.Contains(query, "tcpProxyDelete"):
		return deleteTcpProxy(c, body.Variables)
	case strings.Contains(query, "variableDelete"):
		return deleteVariable(c, body.Variables)
	case strings.Contains(query, "mutation"):
		return gqlError(c, "unsupported mutation")
	case strings.Contains(query, "me {"), strings.Contains(query, "me{"):
		return queryMe(c)
	case strings.Contains(query, "tcpProxies"):
		return queryTcpProxies(c, body.Variables)
	case strings.Contains(query, "variables("):
		return queryVariables(c, body.Variables)
	case strings.Contains(query, "members"):
		return queryWorkspaceMembers(c)
	case strings.Contains(query, "workspaces"):
		return queryWorkspaces(c)
	case strings.Contains(query, "projects"):
		return queryProjects(c)
	}
	return gqlError(c, "unsupported query")
}

// ---------- queries ----------

func queryMe(c *mock.Ctx) error {
	data, err := loadMeta(c.DB, "me")
	if err == sql.ErrNoRows {
		return gqlError(c, "Not Authorized")
	}
	if err != nil {
		return gqlError(c, err.Error())
	}
	return gqlData(c, map[string]any{"me": json.RawMessage(data)})
}

func queryProjects(c *mock.Ctx) error {
	projects, err := loadProjects(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	nodes := []any{}
	for _, p := range projects {
		services := []any{}
		for _, svc := range p.Services {
			services = append(services, map[string]any{"id": svc.ID, "name": svc.Name})
		}
		environments := []any{}
		for _, env := range p.Environments {
			environments = append(environments, map[string]any{"id": env.ID, "name": env.Name})
		}
		nodes = append(nodes, map[string]any{
			"id":           p.ID,
			"name":         p.Name,
			"services":     connection(services),
			"environments": connection(environments),
		})
	}
	return gqlData(c, map[string]any{"projects": connection(nodes)})
}

func queryTcpProxies(c *mock.Ctx, variables map[string]any) error {
	serviceID := stringVariable(variables, "serviceId")
	projects, err := loadProjects(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	for _, p := range projects {
		for _, svc := range p.Services {
			if svc.ID != serviceID {
				continue
			}
			proxies := []any{}
			for _, proxy := range svc.TcpProxies {
				proxies = append(proxies, proxy)
			}
			return gqlData(c, map[string]any{"tcpProxies": proxies})
		}
	}
	return gqlError(c, "Service not found")
}

func queryVariables(c *mock.Ctx, variables map[string]any) error {
	serviceID := stringVariable(variables, "serviceId")
	projects, err := loadProjects(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	for _, p := range projects {
		for _, svc := range p.Services {
			if svc.ID != serviceID {
				continue
			}
			serviceVariables := svc.Variables
			if serviceVariables == nil {
				serviceVariables = map[string]string{}
			}
			return gqlData(c, map[string]any{"variables": serviceVariables})
		}
	}
	return gqlError(c, "Service not found")
}

func loadWorkspaces(db *sql.DB) ([]workspaceDoc, error) {
	data, err := loadMeta(db, "workspaces")
	if err != nil {
		return nil, err
	}
	var workspaces []workspaceDoc
	if err := json.Unmarshal([]byte(data), &workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

func queryWorkspaces(c *mock.Ctx) error {
	workspaces, err := loadWorkspaces(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	nodes := []any{}
	for _, ws := range workspaces {
		nodes = append(nodes, map[string]any{
			"id":               ws.ID,
			"name":             ws.Name,
			"enforceTwoFactor": ws.EnforceTwoFactor,
		})
	}
	return gqlData(c, map[string]any{"workspaces": nodes})
}

func queryWorkspaceMembers(c *mock.Ctx) error {
	workspaces, err := loadWorkspaces(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	nodes := []any{}
	for _, ws := range workspaces {
		members := []any{}
		for _, member := range ws.Members {
			members = append(members, member)
		}
		nodes = append(nodes, map[string]any{
			"id":      ws.ID,
			"name":    ws.Name,
			"members": members,
		})
	}
	return gqlData(c, map[string]any{"workspaces": nodes})
}

// ---------- mutations ----------

func deleteTcpProxy(c *mock.Ctx, variables map[string]any) error {
	proxyID := stringVariable(variables, "id")
	if proxyID == "" {
		return gqlError(c, "id is required")
	}
	projects, err := loadProjects(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	for i := range projects {
		for j := range projects[i].Services {
			svc := &projects[i].Services[j]
			for k := range svc.TcpProxies {
				if svc.TcpProxies[k].ID != proxyID {
					continue
				}
				svc.TcpProxies = append(svc.TcpProxies[:k], svc.TcpProxies[k+1:]...)
				if err := saveProject(c.DB, &projects[i]); err != nil {
					return gqlError(c, err.Error())
				}
				return gqlData(c, map[string]any{"tcpProxyDelete": true})
			}
		}
	}
	return gqlError(c, "TCP proxy not found")
}

func deleteVariable(c *mock.Ctx, variables map[string]any) error {
	input, _ := variables["input"].(map[string]any)
	if input == nil {
		input = variables
	}
	serviceID := stringVariable(input, "serviceId")
	name := stringVariable(input, "name")
	if serviceID == "" || name == "" {
		return gqlError(c, "serviceId and name are required")
	}
	projects, err := loadProjects(c.DB)
	if err != nil {
		return gqlError(c, err.Error())
	}
	for i := range projects {
		for j := range projects[i].Services {
			svc := &projects[i].Services[j]
			if svc.ID != serviceID {
				continue
			}
			if _, exists := svc.Variables[name]; !exists {
				return gqlError(c, "Variable not found")
			}
			delete(svc.Variables, name)
			if err := saveProject(c.DB, &projects[i]); err != nil {
				return gqlError(c, err.Error())
			}
			return gqlData(c, map[string]any{"variableDelete": true})
		}
	}
	return gqlError(c, "Service not found")
}
