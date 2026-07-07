// Package fly is a stateful mock of the Fly.io Machines REST API subset that
// a cloud-audit agent inspects: app listing per org, machines, and
// certificates with per-hostname check diagnostics (validation errors, DNS
// state, expiry).
//
// It is backed by SQLite, so the write endpoints actually mutate state — an
// agent can scan, fix through the same API shapes the real provider exposes,
// and re-scan:
//
//   - POST /v1/apps/{app}/machines (scale out a single-machine app)
//   - POST /v1/apps/{app}/certificates (re-add a hostname; the mock treats
//     re-issuance as DNS-now-correct and returns a healthy certificate)
//   - DELETE /v1/apps/{app}/certificates/{hostname} (remove a dangling domain)
//
// Certificate expiry is seeded as expires_in_days and materialized to an
// absolute expires_at relative to the current time on every read, so
// "expiring soon" scenarios stay deterministic no matter when they run.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"org":"cd-org",
//	 "apps":[{"id","name","status","network",
//	          "machines":[{"id","name","state","region"}],
//	          "certificates":[{"id","hostname","configured","dns_configured",
//	                           "validation_errors":[...],"expires_in_days":90}]}]}
package fly

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the fly service.
func New() *mock.Service {
	s := mock.NewService("fly")
	s.Tables = []mock.Table{
		{Name: "meta", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "apps", DDL: "name TEXT PRIMARY KEY, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.GET("/v1/apps", listApps)
	s.GET("/v1/apps/{app}", getApp)
	s.GET("/v1/apps/{app}/machines", listMachines)
	s.POST("/v1/apps/{app}/machines", createMachine)
	s.GET("/v1/apps/{app}/certificates", listCertificates)
	s.POST("/v1/apps/{app}/certificates", createCertificate)
	s.GET("/v1/apps/{app}/certificates/{hostname}/check", checkCertificate)
	s.DELETE("/v1/apps/{app}/certificates/{hostname}", deleteCertificate)

	return s
}

// ---------- error helper ----------

func apiError(c *mock.Ctx, status int, message string) error {
	return c.JSON(status, map[string]any{"error": message})
}

// ---------- fixture / documents ----------

type machineDoc struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	State  string `json:"state"`
	Region string `json:"region,omitempty"`
}

type certificateDoc struct {
	ID               string   `json:"id,omitempty"`
	Hostname         string   `json:"hostname"`
	Configured       bool     `json:"configured"`
	DNSConfigured    bool     `json:"dns_configured"`
	ValidationErrors []string `json:"validation_errors"`
	ExpiresInDays    *int     `json:"expires_in_days,omitempty"`
}

type appDoc struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Status       string           `json:"status,omitempty"`
	Network      string           `json:"network,omitempty"`
	Machines     []machineDoc     `json:"machines"`
	Certificates []certificateDoc `json:"certificates"`
}

type fixture struct {
	Org  string   `json:"org"`
	Apps []appDoc `json:"apps"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("fly seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR REPLACE INTO meta(id, data) VALUES('org', ?)", f.Org); err != nil {
		return err
	}
	for _, app := range f.Apps {
		data, err := json.Marshal(app)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO apps(name, data) VALUES(?,?)", app.Name, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func orgSlug(db *sql.DB) string {
	var org string
	if err := db.QueryRow("SELECT data FROM meta WHERE id='org'").Scan(&org); err != nil {
		return "personal"
	}
	return org
}

func loadApp(c *mock.Ctx) (*appDoc, bool, error) {
	var data string
	err := c.DB.QueryRow("SELECT data FROM apps WHERE name=?", c.Params["app"]).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, false, apiError(c, 404, "App not found")
	}
	if err != nil {
		return nil, false, apiError(c, 500, err.Error())
	}
	var app appDoc
	if err := json.Unmarshal([]byte(data), &app); err != nil {
		return nil, false, apiError(c, 500, err.Error())
	}
	return &app, true, nil
}

func saveApp(db *sql.DB, app *appDoc) error {
	data, err := json.Marshal(app)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE apps SET data=? WHERE name=?", string(data), app.Name)
	return err
}

func appEnvelope(app *appDoc) map[string]any {
	return map[string]any{
		"id":            app.ID,
		"name":          app.Name,
		"machine_count": len(app.Machines),
		"network":       app.Network,
		"status":        app.Status,
	}
}

// ---------- handlers ----------

func listApps(c *mock.Ctx) error {
	requestedOrg := c.Query.Get("org_slug")
	if requestedOrg != "" && requestedOrg != orgSlug(c.DB) {
		return c.JSON(200, map[string]any{"total_apps": 0, "apps": []any{}})
	}
	rows, err := c.DB.Query("SELECT data FROM apps ORDER BY name")
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	defer rows.Close()
	apps := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, err.Error())
		}
		var app appDoc
		if err := json.Unmarshal([]byte(data), &app); err != nil {
			return apiError(c, 500, err.Error())
		}
		apps = append(apps, appEnvelope(&app))
	}
	return c.JSON(200, map[string]any{"total_apps": len(apps), "apps": apps})
}

func getApp(c *mock.Ctx) error {
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	return c.JSON(200, appEnvelope(app))
}

func listMachines(c *mock.Ctx) error {
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	machines := []any{}
	for _, machine := range app.Machines {
		machines = append(machines, machine)
	}
	return c.JSON(200, machines)
}

func createMachine(c *mock.Ctx) error {
	var body struct {
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	machine := machineDoc{
		ID:     fmt.Sprintf("m-%s-%d", app.Name, len(app.Machines)+1),
		Name:   body.Name,
		State:  "started",
		Region: body.Region,
	}
	if machine.Name == "" {
		machine.Name = machine.ID
	}
	if machine.Region == "" {
		machine.Region = "iad"
	}
	app.Machines = append(app.Machines, machine)
	if err := saveApp(c.DB, app); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, machine)
}

func certificateEnvelope(cert *certificateDoc) map[string]any {
	return map[string]any{
		"id":         cert.ID,
		"hostname":   cert.Hostname,
		"configured": cert.Configured,
	}
}

func listCertificates(c *mock.Ctx) error {
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	certificates := []any{}
	for i := range app.Certificates {
		certificates = append(certificates, certificateEnvelope(&app.Certificates[i]))
	}
	return c.JSON(200, map[string]any{"certificates": certificates})
}

const healthyCertificateExpiryDays = 90

func createCertificate(c *mock.Ctx) error {
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := c.Bind(&body); err != nil || body.Hostname == "" {
		return apiError(c, 400, "hostname is required")
	}
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	expiresInDays := healthyCertificateExpiryDays
	healthy := certificateDoc{
		ID:               fmt.Sprintf("cert-%s-%d", app.Name, len(app.Certificates)+1),
		Hostname:         body.Hostname,
		Configured:       true,
		DNSConfigured:    true,
		ValidationErrors: []string{},
		ExpiresInDays:    &expiresInDays,
	}
	replaced := false
	for i := range app.Certificates {
		if app.Certificates[i].Hostname == body.Hostname {
			app.Certificates[i] = healthy
			replaced = true
			break
		}
	}
	if !replaced {
		app.Certificates = append(app.Certificates, healthy)
	}
	if err := saveApp(c.DB, app); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(201, certificateEnvelope(&healthy))
}

func checkCertificate(c *mock.Ctx) error {
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	hostname := c.Params["hostname"]
	for i := range app.Certificates {
		cert := &app.Certificates[i]
		if cert.Hostname != hostname {
			continue
		}
		check := map[string]any{
			"hostname":          cert.Hostname,
			"configured":        cert.Configured,
			"dns_configured":    cert.DNSConfigured,
			"validation_errors": cert.ValidationErrors,
		}
		if cert.ValidationErrors == nil {
			check["validation_errors"] = []string{}
		}
		if cert.ExpiresInDays != nil {
			check["expires_at"] = time.Now().UTC().
				Add(time.Duration(*cert.ExpiresInDays) * 24 * time.Hour).
				Format(time.RFC3339)
		}
		return c.JSON(200, check)
	}
	return apiError(c, 404, "Certificate not found")
}

func deleteCertificate(c *mock.Ctx) error {
	app, found, err := loadApp(c)
	if !found {
		return err
	}
	hostname := c.Params["hostname"]
	for i := range app.Certificates {
		if app.Certificates[i].Hostname == hostname {
			app.Certificates = append(app.Certificates[:i], app.Certificates[i+1:]...)
			if err := saveApp(c.DB, app); err != nil {
				return apiError(c, 500, err.Error())
			}
			return c.JSON(200, map[string]any{"deleted": true})
		}
	}
	return apiError(c, 404, "Certificate not found")
}
