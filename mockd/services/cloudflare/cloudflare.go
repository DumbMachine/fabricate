// Package cloudflare is a stateful mock of the Cloudflare REST API v4 subset
// that a cloud-audit agent inspects: token verify, accounts, zones,
// bulk + single zone settings, DNSSEC, Universal SSL, and ruleset phase
// entrypoints. Every response uses the real v4 envelope
// {success, errors, messages, result[, result_info]}.
//
// It is backed by SQLite, so the write endpoints (PATCH zone settings, PATCH
// dnssec, PATCH universal SSL, PUT ruleset entrypoints, PATCH account) actually
// mutate state — an agent can scan, apply a fix through the same API shape the
// real Cloudflare exposes, re-scan, and watch the finding disappear.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"accounts":[{"id","name","enforceTwofactor"}],
//	 "zones":[{"id","name","plan","settings":{...},"dnssecStatus",
//	           "universalSslEnabled","phaseRules":{"<phase>":[{"enabled",...}]}}]}
//
// Omitting a phase from phaseRules makes its entrypoint 404, matching a real
// zone that has never deployed a ruleset in that phase.
package cloudflare

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the cloudflare service.
func New() *mock.Service {
	s := mock.NewService("cloudflare")
	s.Tables = []mock.Table{
		{Name: "accounts", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "zones", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.GET("/user/tokens/verify", verifyToken)
	s.GET("/accounts", listAccounts)
	s.PATCH("/accounts/{accountId}", patchAccount)
	s.GET("/zones", listZones)
	s.GET("/zones/{zoneId}/settings", listZoneSettings)
	s.PATCH("/zones/{zoneId}/settings", patchZoneSettings)
	s.GET("/zones/{zoneId}/settings/{settingId}", getZoneSetting)
	s.PATCH("/zones/{zoneId}/settings/{settingId}", patchZoneSetting)
	s.GET("/zones/{zoneId}/dnssec", getDnssec)
	s.PATCH("/zones/{zoneId}/dnssec", patchDnssec)
	s.GET("/zones/{zoneId}/ssl/universal/settings", getUniversalSsl)
	s.PATCH("/zones/{zoneId}/ssl/universal/settings", patchUniversalSsl)
	s.GET("/zones/{zoneId}/rulesets/phases/{phase}/entrypoint", getPhaseEntrypoint)
	s.PUT("/zones/{zoneId}/rulesets/phases/{phase}/entrypoint", putPhaseEntrypoint)

	return s
}

// ---------- envelope helpers ----------

func ok(c *mock.Ctx, result any) error {
	return c.JSON(200, map[string]any{
		"success": true, "errors": []any{}, "messages": []any{}, "result": result,
	})
}

func okPage(c *mock.Ctx, result any, count int) error {
	return c.JSON(200, map[string]any{
		"success": true, "errors": []any{}, "messages": []any{}, "result": result,
		"result_info": map[string]any{
			"page": 1, "per_page": 50, "total_pages": 1, "count": count, "total_count": count,
		},
	})
}

func apiError(c *mock.Ctx, status, code int, message string) error {
	return c.JSON(status, map[string]any{
		"success":  false,
		"errors":   []any{map[string]any{"code": code, "message": message}},
		"messages": []any{}, "result": nil,
	})
}

// ---------- fixture / documents ----------

type accountDoc struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	EnforceTwofactor bool   `json:"enforceTwofactor"`
}

type zoneDoc struct {
	ID                  string                       `json:"id"`
	Name                string                       `json:"name"`
	Plan                string                       `json:"plan"`
	Settings            map[string]json.RawMessage   `json:"settings"`
	DnssecStatus        string                       `json:"dnssecStatus"`
	UniversalSslEnabled bool                         `json:"universalSslEnabled"`
	PhaseRules          map[string][]json.RawMessage `json:"phaseRules"`
}

type fixture struct {
	Accounts []accountDoc `json:"accounts"`
	Zones    []zoneDoc    `json:"zones"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("cloudflare seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, a := range f.Accounts {
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO accounts(id, data) VALUES(?,?)", a.ID, string(data)); err != nil {
			return err
		}
	}
	for _, z := range f.Zones {
		if z.Settings == nil {
			z.Settings = map[string]json.RawMessage{}
		}
		if z.PhaseRules == nil {
			z.PhaseRules = map[string][]json.RawMessage{}
		}
		data, err := json.Marshal(z)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO zones(id, name, data) VALUES(?,?,?)", z.ID, z.Name, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadZone(db *sql.DB, id string) (*zoneDoc, error) {
	var data string
	err := db.QueryRow("SELECT data FROM zones WHERE id=?", id).Scan(&data)
	if err != nil {
		return nil, err
	}
	var z zoneDoc
	if err := json.Unmarshal([]byte(data), &z); err != nil {
		return nil, err
	}
	return &z, nil
}

func saveZone(db *sql.DB, z *zoneDoc) error {
	data, err := json.Marshal(z)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE zones SET data=? WHERE id=?", string(data), z.ID)
	return err
}

func zoneOr404(c *mock.Ctx) (*zoneDoc, bool, error) {
	z, err := loadZone(c.DB, c.Params["zoneId"])
	if err == sql.ErrNoRows {
		return nil, false, apiError(c, 404, 7003, "Could not route to zone, perhaps your object identifier is invalid?")
	}
	if err != nil {
		return nil, false, apiError(c, 500, 10000, err.Error())
	}
	return z, true, nil
}

// ---------- handlers ----------

func verifyToken(c *mock.Ctx) error {
	return ok(c, map[string]any{"id": "mock-token", "status": "active"})
}

func listAccounts(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT data FROM accounts ORDER BY id")
	if err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	defer rows.Close()
	accounts := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, 10000, err.Error())
		}
		var a accountDoc
		if err := json.Unmarshal([]byte(data), &a); err != nil {
			return apiError(c, 500, 10000, err.Error())
		}
		accounts = append(accounts, map[string]any{
			"id": a.ID, "name": a.Name, "type": "standard",
			"settings": map[string]any{"enforce_twofactor": a.EnforceTwofactor},
		})
	}
	return okPage(c, accounts, len(accounts))
}

func patchAccount(c *mock.Ctx) error {
	var body struct {
		Settings *struct {
			EnforceTwofactor *bool `json:"enforce_twofactor"`
		} `json:"settings"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, 6003, "invalid request body")
	}
	var data string
	err := c.DB.QueryRow("SELECT data FROM accounts WHERE id=?", c.Params["accountId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 404, 7003, "account not found")
	}
	if err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	var a accountDoc
	if err := json.Unmarshal([]byte(data), &a); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	if body.Settings != nil && body.Settings.EnforceTwofactor != nil {
		a.EnforceTwofactor = *body.Settings.EnforceTwofactor
	}
	updated, err := json.Marshal(a)
	if err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	if _, err := c.DB.Exec("UPDATE accounts SET data=? WHERE id=?", string(updated), a.ID); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, map[string]any{
		"id": a.ID, "name": a.Name,
		"settings": map[string]any{"enforce_twofactor": a.EnforceTwofactor},
	})
}

func listZones(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT data FROM zones ORDER BY name")
	if err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	defer rows.Close()
	zones := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, 10000, err.Error())
		}
		var z zoneDoc
		if err := json.Unmarshal([]byte(data), &z); err != nil {
			return apiError(c, 500, 10000, err.Error())
		}
		zones = append(zones, map[string]any{
			"id": z.ID, "name": z.Name, "status": "active",
			"plan": map[string]any{"legacy_id": z.Plan, "name": z.Plan},
		})
	}
	return okPage(c, zones, len(zones))
}

func settingEnvelope(id string, value json.RawMessage) map[string]any {
	return map[string]any{
		"id": id, "value": value, "editable": true, "modified_on": "2026-07-01T00:00:00Z",
	}
}

func listZoneSettings(c *mock.Ctx) error {
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	settings := []any{}
	for id, value := range z.Settings {
		settings = append(settings, settingEnvelope(id, value))
	}
	return ok(c, settings)
}

func getZoneSetting(c *mock.Ctx) error {
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	value, exists := z.Settings[c.Params["settingId"]]
	if !exists {
		return apiError(c, 404, 1006, "unrecognized zone setting name")
	}
	return ok(c, settingEnvelope(c.Params["settingId"], value))
}

func patchZoneSetting(c *mock.Ctx) error {
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := c.Bind(&body); err != nil || len(body.Value) == 0 {
		return apiError(c, 400, 1007, "invalid value")
	}
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	z.Settings[c.Params["settingId"]] = body.Value
	if err := saveZone(c.DB, z); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, settingEnvelope(c.Params["settingId"], body.Value))
}

func patchZoneSettings(c *mock.Ctx) error {
	var body struct {
		Items []struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		} `json:"items"`
	}
	if err := c.Bind(&body); err != nil || len(body.Items) == 0 {
		return apiError(c, 400, 1007, "items is required")
	}
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	updated := []any{}
	for _, item := range body.Items {
		z.Settings[item.ID] = item.Value
		updated = append(updated, settingEnvelope(item.ID, item.Value))
	}
	if err := saveZone(c.DB, z); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, updated)
}

func getDnssec(c *mock.Ctx) error {
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	return ok(c, map[string]any{"status": z.DnssecStatus})
}

func patchDnssec(c *mock.Ctx) error {
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&body); err != nil || body.Status == "" {
		return apiError(c, 400, 1007, "status is required")
	}
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	z.DnssecStatus = body.Status
	if err := saveZone(c.DB, z); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, map[string]any{"status": z.DnssecStatus})
}

func getUniversalSsl(c *mock.Ctx) error {
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	return ok(c, map[string]any{"enabled": z.UniversalSslEnabled, "certificate_authority": "google"})
}

func patchUniversalSsl(c *mock.Ctx) error {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.Bind(&body); err != nil || body.Enabled == nil {
		return apiError(c, 400, 1007, "enabled is required")
	}
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	z.UniversalSslEnabled = *body.Enabled
	if err := saveZone(c.DB, z); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, map[string]any{"enabled": z.UniversalSslEnabled})
}

func getPhaseEntrypoint(c *mock.Ctx) error {
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	phase := c.Params["phase"]
	rules, exists := z.PhaseRules[phase]
	if !exists {
		return apiError(c, 404, 20040, "could not find ruleset for phase")
	}
	if rules == nil {
		rules = []json.RawMessage{}
	}
	return ok(c, map[string]any{
		"id": "entrypoint-" + phase, "name": "zone entrypoint", "kind": "zone",
		"phase": phase, "rules": rules,
	})
}

func putPhaseEntrypoint(c *mock.Ctx) error {
	var body struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, 20217, "invalid ruleset body")
	}
	z, found, err := zoneOr404(c)
	if !found {
		return err
	}
	phase := c.Params["phase"]
	if body.Rules == nil {
		body.Rules = []json.RawMessage{}
	}
	z.PhaseRules[phase] = body.Rules
	if err := saveZone(c.DB, z); err != nil {
		return apiError(c, 500, 10000, err.Error())
	}
	return ok(c, map[string]any{
		"id": "entrypoint-" + phase, "name": "zone entrypoint", "kind": "zone",
		"phase": phase, "rules": body.Rules,
	})
}
