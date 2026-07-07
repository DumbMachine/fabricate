// Package supabase is a stateful mock of the Supabase Management API subset
// that a cloud-audit agent inspects: project listing, the security
// advisors (Splinter lints), auth config, network restrictions, SSL
// enforcement, legacy API keys, and backups/PITR state.
//
// It is backed by SQLite, so the write endpoints actually mutate state — an
// agent can scan, fix through the same API shapes the real provider exposes,
// and re-scan:
//
//   - PATCH /v1/projects/{ref}/config/auth (mailer_autoconfirm, anonymous
//     sign-ins, OTP expiry)
//   - POST /v1/projects/{ref}/network-restrictions/apply (allowed CIDRs)
//   - PUT /v1/projects/{ref}/ssl-enforcement (requestedConfig.database)
//   - PUT /v1/projects/{ref}/api-keys/legacy (enabled=false disables legacy JWTs)
//   - POST /v1/projects/{ref}/database/query — recognized DDL fixes clear the
//     matching advisor lints (e.g. ALTER TABLE … ENABLE ROW LEVEL SECURITY
//     clears rls_disabled_in_public/policy_exists_rls_disabled for that table),
//     mirroring how a real fix would make the linter go quiet.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"projects":[{"id","organization_id","name","region","status",
//	              "lints":[{"name","level","metadata":{...},...}],
//	              "auth":{...},"network":{...},"ssl":{...},
//	              "legacyApiKeys":{"enabled"},"backups":{...}}]}
package supabase

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the supabase service.
func New() *mock.Service {
	s := mock.NewService("supabase")
	s.Tables = []mock.Table{
		{Name: "projects", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.GET("/v1/projects", listProjects)
	s.GET("/v1/projects/{ref}/advisors/security", getSecurityAdvisors)
	s.GET("/v1/projects/{ref}/config/auth", getAuthConfig)
	s.PATCH("/v1/projects/{ref}/config/auth", patchAuthConfig)
	s.GET("/v1/projects/{ref}/network-restrictions", getNetworkRestrictions)
	s.POST("/v1/projects/{ref}/network-restrictions/apply", applyNetworkRestrictions)
	s.GET("/v1/projects/{ref}/ssl-enforcement", getSslEnforcement)
	s.PUT("/v1/projects/{ref}/ssl-enforcement", putSslEnforcement)
	s.GET("/v1/projects/{ref}/api-keys/legacy", getLegacyAPIKeys)
	s.PUT("/v1/projects/{ref}/api-keys/legacy", putLegacyAPIKeys)
	s.GET("/v1/projects/{ref}/database/backups", getBackups)
	s.POST("/v1/projects/{ref}/database/query", runDatabaseQuery)

	return s
}

// ---------- error helper ----------

func apiError(c *mock.Ctx, status int, message string) error {
	return c.JSON(status, map[string]any{"message": message})
}

// ---------- fixture / documents ----------

type lintMetadata struct {
	Name   string `json:"name"`
	Schema string `json:"schema,omitempty"`
	Type   string `json:"type,omitempty"`
}

type lintDoc struct {
	Name        string       `json:"name"`
	Level       string       `json:"level"`
	Facing      string       `json:"facing,omitempty"`
	Description string       `json:"description,omitempty"`
	Detail      string       `json:"detail,omitempty"`
	Remediation string       `json:"remediation,omitempty"`
	Metadata    lintMetadata `json:"metadata"`
	CacheKey    string       `json:"cache_key,omitempty"`
}

type authConfigDoc struct {
	MailerAutoconfirm             bool `json:"mailer_autoconfirm"`
	ExternalAnonymousUsersEnabled bool `json:"external_anonymous_users_enabled"`
	MailerOtpExp                  int  `json:"mailer_otp_exp"`
}

type networkConfigDoc struct {
	DbAllowedCidrs   []string `json:"dbAllowedCidrs"`
	DbAllowedCidrsV6 []string `json:"dbAllowedCidrsV6"`
}

type networkDoc struct {
	Entitlement string           `json:"entitlement"`
	Config      networkConfigDoc `json:"config"`
	Status      string           `json:"status"`
}

type sslDoc struct {
	CurrentConfig struct {
		Database bool `json:"database"`
	} `json:"currentConfig"`
	AppliedSuccessfully bool `json:"appliedSuccessfully"`
}

type legacyAPIKeysDoc struct {
	Enabled bool `json:"enabled"`
}

type backupsDoc struct {
	PitrEnabled bool `json:"pitr_enabled"`
	WalgEnabled bool `json:"walg_enabled"`
}

type projectDoc struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organization_id"`
	Name           string           `json:"name"`
	Region         string           `json:"region"`
	Status         string           `json:"status"`
	Lints          []lintDoc        `json:"lints"`
	Auth           authConfigDoc    `json:"auth"`
	Network        networkDoc       `json:"network"`
	Ssl            sslDoc           `json:"ssl"`
	LegacyAPIKeys  legacyAPIKeysDoc `json:"legacyApiKeys"`
	Backups        backupsDoc       `json:"backups"`
}

type fixture struct {
	Projects []projectDoc `json:"projects"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("supabase seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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

func loadProject(db *sql.DB, ref string) (*projectDoc, error) {
	var data string
	err := db.QueryRow("SELECT data FROM projects WHERE id=?", ref).Scan(&data)
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
	p, err := loadProject(c.DB, c.Params["ref"])
	if err == sql.ErrNoRows {
		return nil, false, apiError(c, 404, "Project not found")
	}
	if err != nil {
		return nil, false, apiError(c, 500, err.Error())
	}
	return p, true, nil
}

// projectEnvelope mirrors the fields an audit client reads from GET /v1/projects.
func projectEnvelope(p *projectDoc) map[string]any {
	return map[string]any{
		"id":              p.ID,
		"organization_id": p.OrganizationID,
		"name":            p.Name,
		"region":          p.Region,
		"status":          p.Status,
		"created_at":      "2026-01-01T00:00:00Z",
	}
}

// ---------- handlers ----------

func listProjects(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT data FROM projects ORDER BY name")
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	defer rows.Close()
	projects := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, err.Error())
		}
		var p projectDoc
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return apiError(c, 500, err.Error())
		}
		projects = append(projects, projectEnvelope(&p))
	}
	return c.JSON(200, projects)
}

func getSecurityAdvisors(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	lints := []any{}
	for _, l := range p.Lints {
		lints = append(lints, l)
	}
	return c.JSON(200, map[string]any{"lints": lints})
}

func getAuthConfig(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	return c.JSON(200, p.Auth)
}

func patchAuthConfig(c *mock.Ctx) error {
	var body struct {
		MailerAutoconfirm             *bool `json:"mailer_autoconfirm"`
		ExternalAnonymousUsersEnabled *bool `json:"external_anonymous_users_enabled"`
		MailerOtpExp                  *int  `json:"mailer_otp_exp"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	if body.MailerAutoconfirm != nil {
		p.Auth.MailerAutoconfirm = *body.MailerAutoconfirm
	}
	if body.ExternalAnonymousUsersEnabled != nil {
		p.Auth.ExternalAnonymousUsersEnabled = *body.ExternalAnonymousUsersEnabled
	}
	if body.MailerOtpExp != nil {
		p.Auth.MailerOtpExp = *body.MailerOtpExp
	}
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, p.Auth)
}

func getNetworkRestrictions(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	return c.JSON(200, p.Network)
}

func applyNetworkRestrictions(c *mock.Ctx) error {
	var body networkConfigDoc
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	if body.DbAllowedCidrs != nil {
		p.Network.Config.DbAllowedCidrs = body.DbAllowedCidrs
	}
	if body.DbAllowedCidrsV6 != nil {
		p.Network.Config.DbAllowedCidrsV6 = body.DbAllowedCidrsV6
	}
	p.Network.Status = "applied"
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(201, p.Network)
}

func getSslEnforcement(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	return c.JSON(200, p.Ssl)
}

func putSslEnforcement(c *mock.Ctx) error {
	var body struct {
		RequestedConfig struct {
			Database *bool `json:"database"`
		} `json:"requestedConfig"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	if body.RequestedConfig.Database != nil {
		p.Ssl.CurrentConfig.Database = *body.RequestedConfig.Database
	}
	p.Ssl.AppliedSuccessfully = true
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, p.Ssl)
}

func getLegacyAPIKeys(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	return c.JSON(200, p.LegacyAPIKeys)
}

func putLegacyAPIKeys(c *mock.Ctx) error {
	var body legacyAPIKeysDoc
	enabledQuery := c.Query.Get("enabled")
	if err := c.Bind(&body); err != nil && enabledQuery == "" {
		return apiError(c, 400, "invalid request body")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	if enabledQuery != "" {
		p.LegacyAPIKeys.Enabled = enabledQuery == "true"
	} else {
		p.LegacyAPIKeys.Enabled = body.Enabled
	}
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, p.LegacyAPIKeys)
}

func getBackups(c *mock.Ctx) error {
	p, found, err := projectOr404(c)
	if !found {
		return err
	}
	return c.JSON(200, p.Backups)
}

// lintFixKeywords maps a lint name to SQL fragments that count as fixing it:
// a query mentioning the lint's entity plus one of these fragments clears the
// lint, mirroring how the real linter goes quiet after the DDL fix.
var lintFixKeywords = map[string][]string{
	"rls_disabled_in_public":       {"enable row level security"},
	"policy_exists_rls_disabled":   {"enable row level security"},
	"auth_users_exposed":           {"drop view", "revoke"},
	"sensitive_columns_exposed":    {"revoke", "drop view", "drop table"},
	"security_definer_view":        {"security_invoker", "drop view"},
	"public_bucket_allows_listing": {"storage.buckets"},
	"function_search_path_mutable": {"set search_path"},
	"extension_in_public":          {"set schema", "drop extension"},
}

func runDatabaseQuery(c *mock.Ctx) error {
	var body struct {
		Query string `json:"query"`
	}
	if err := c.Bind(&body); err != nil || strings.TrimSpace(body.Query) == "" {
		return apiError(c, 400, "query is required")
	}
	p, found, err := projectOr404(c)
	if !found {
		return err
	}

	query := strings.ToLower(body.Query)
	remaining := p.Lints[:0]
	for _, l := range p.Lints {
		if lintIsFixedBy(l, query) {
			continue
		}
		remaining = append(remaining, l)
	}
	p.Lints = remaining
	if err := saveProject(c.DB, p); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(201, []any{})
}

func lintIsFixedBy(l lintDoc, query string) bool {
	keywords, known := lintFixKeywords[l.Name]
	if !known || l.Metadata.Name == "" {
		return false
	}
	if !strings.Contains(query, strings.ToLower(l.Metadata.Name)) {
		return false
	}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}
