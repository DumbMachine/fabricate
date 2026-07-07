// Package render is a stateful mock of the Render REST API subset that
// a cloud-audit agent inspects: workspace owners, services with custom
// domains and env vars, Postgres instances (IP allow lists, high
// availability), and Key Value instances. List endpoints use Render's
// wrapper-array shape ([{"service":{...},"cursor":"..."}]).
//
// It is backed by SQLite, so the write endpoints actually mutate state — an
// agent can scan, fix through the same API shapes the real provider exposes,
// and re-scan:
//
//   - PATCH /v1/postgres/{id} and /v1/key-value/{id} (ipAllowList, HA)
//   - POST /v1/services/{id}/custom-domains/{domainId}/verify
//   - DELETE /v1/services/{id}/custom-domains/{domainId}
//   - PUT/DELETE /v1/services/{id}/env-vars/{key}
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"owner":{"id","name","email","type"},
//	 "services":[{"id","name","type","customDomains":[...],"envVars":[...]}],
//	 "postgres":[{"id","name","plan","ipAllowList":[...],"highAvailabilityEnabled"}],
//	 "keyValue":[{"id","name","plan","ipAllowList":[...]}]}
package render

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the render service.
func New() *mock.Service {
	s := mock.NewService("render")
	s.Tables = []mock.Table{
		{Name: "account", DDL: "id TEXT PRIMARY KEY, data TEXT NOT NULL"},
		{Name: "services", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
		{Name: "postgres", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
		{Name: "keyvalue", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, data TEXT NOT NULL"},
	}
	s.Seed = seed

	s.GET("/v1/owners", listOwners)
	s.GET("/v1/services", listServices)
	s.GET("/v1/services/{serviceId}/custom-domains", listCustomDomains)
	s.POST("/v1/services/{serviceId}/custom-domains/{domainId}/verify", verifyCustomDomain)
	s.DELETE("/v1/services/{serviceId}/custom-domains/{domainId}", deleteCustomDomain)
	s.GET("/v1/services/{serviceId}/env-vars", listEnvVars)
	s.PUT("/v1/services/{serviceId}/env-vars/{key}", putEnvVar)
	s.DELETE("/v1/services/{serviceId}/env-vars/{key}", deleteEnvVar)
	s.GET("/v1/postgres", listPostgres)
	s.PATCH("/v1/postgres/{postgresId}", patchPostgres)
	s.GET("/v1/key-value", listKeyValue)
	s.PATCH("/v1/key-value/{keyValueId}", patchKeyValue)

	return s
}

// ---------- error helper ----------

func apiError(c *mock.Ctx, status int, message string) error {
	return c.JSON(status, map[string]any{"id": "mock", "message": message})
}

// ---------- fixture / documents ----------

type ownerDoc struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Type  string `json:"type,omitempty"`
}

type customDomainDoc struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	VerificationStatus string `json:"verificationStatus"`
}

type envVarDoc struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type serviceDoc struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Type          string            `json:"type,omitempty"`
	CustomDomains []customDomainDoc `json:"customDomains"`
	EnvVars       []envVarDoc       `json:"envVars"`
}

type ipAllowEntryDoc struct {
	CidrBlock   string `json:"cidrBlock"`
	Description string `json:"description,omitempty"`
}

type postgresDoc struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Plan                    string            `json:"plan,omitempty"`
	IPAllowList             []ipAllowEntryDoc `json:"ipAllowList"`
	HighAvailabilityEnabled bool              `json:"highAvailabilityEnabled"`
}

type keyValueDoc struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Plan        string            `json:"plan,omitempty"`
	IPAllowList []ipAllowEntryDoc `json:"ipAllowList"`
}

type fixture struct {
	Owner    json.RawMessage `json:"owner"`
	Services []serviceDoc    `json:"services"`
	Postgres []postgresDoc   `json:"postgres"`
	KeyValue []keyValueDoc   `json:"keyValue"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("render seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(f.Owner) > 0 {
		if _, err := tx.Exec("INSERT OR REPLACE INTO account(id, data) VALUES('owner', ?)", string(f.Owner)); err != nil {
			return err
		}
	}
	for _, svc := range f.Services {
		data, err := json.Marshal(svc)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO services(id, name, data) VALUES(?,?,?)", svc.ID, svc.Name, string(data)); err != nil {
			return err
		}
	}
	for _, pg := range f.Postgres {
		data, err := json.Marshal(pg)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO postgres(id, name, data) VALUES(?,?,?)", pg.ID, pg.Name, string(data)); err != nil {
			return err
		}
	}
	for _, kv := range f.KeyValue {
		data, err := json.Marshal(kv)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO keyvalue(id, name, data) VALUES(?,?,?)", kv.ID, kv.Name, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// listWrapped returns Render's list shape: [{"<key>": entity, "cursor": "cur-N"}].
func listWrapped(c *mock.Ctx, table, key string) error {
	rows, err := c.DB.Query("SELECT data FROM " + table + " ORDER BY name")
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	defer rows.Close()
	items := []any{}
	index := 0
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, err.Error())
		}
		index++
		items = append(items, map[string]any{
			key:      json.RawMessage(data),
			"cursor": fmt.Sprintf("cur-%d", index),
		})
	}
	return c.JSON(200, items)
}

func loadService(c *mock.Ctx) (*serviceDoc, bool, error) {
	var data string
	err := c.DB.QueryRow("SELECT data FROM services WHERE id=?", c.Params["serviceId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, false, apiError(c, 404, "service not found")
	}
	if err != nil {
		return nil, false, apiError(c, 500, err.Error())
	}
	var svc serviceDoc
	if err := json.Unmarshal([]byte(data), &svc); err != nil {
		return nil, false, apiError(c, 500, err.Error())
	}
	return &svc, true, nil
}

func saveService(db *sql.DB, svc *serviceDoc) error {
	data, err := json.Marshal(svc)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE services SET data=? WHERE id=?", string(data), svc.ID)
	return err
}

// ---------- handlers ----------

func listOwners(c *mock.Ctx) error {
	var data string
	err := c.DB.QueryRow("SELECT data FROM account WHERE id='owner'").Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 401, "unauthorized")
	}
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, []any{map[string]any{"owner": json.RawMessage(data), "cursor": "cur-1"}})
}

func listServices(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT data FROM services ORDER BY name")
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	defer rows.Close()
	items := []any{}
	index := 0
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return apiError(c, 500, err.Error())
		}
		var svc serviceDoc
		if err := json.Unmarshal([]byte(data), &svc); err != nil {
			return apiError(c, 500, err.Error())
		}
		index++
		items = append(items, map[string]any{
			"service": map[string]any{"id": svc.ID, "name": svc.Name, "type": svc.Type},
			"cursor":  fmt.Sprintf("cur-%d", index),
		})
	}
	return c.JSON(200, items)
}

func listCustomDomains(c *mock.Ctx) error {
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	items := []any{}
	for index, domain := range svc.CustomDomains {
		items = append(items, map[string]any{
			"customDomain": domain,
			"cursor":       fmt.Sprintf("cur-%d", index+1),
		})
	}
	return c.JSON(200, items)
}

func verifyCustomDomain(c *mock.Ctx) error {
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	domainID := c.Params["domainId"]
	for i := range svc.CustomDomains {
		if svc.CustomDomains[i].ID == domainID || svc.CustomDomains[i].Name == domainID {
			svc.CustomDomains[i].VerificationStatus = "verified"
			if err := saveService(c.DB, svc); err != nil {
				return apiError(c, 500, err.Error())
			}
			return c.JSON(200, svc.CustomDomains[i])
		}
	}
	return apiError(c, 404, "custom domain not found")
}

func deleteCustomDomain(c *mock.Ctx) error {
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	domainID := c.Params["domainId"]
	for i := range svc.CustomDomains {
		if svc.CustomDomains[i].ID == domainID || svc.CustomDomains[i].Name == domainID {
			svc.CustomDomains = append(svc.CustomDomains[:i], svc.CustomDomains[i+1:]...)
			if err := saveService(c.DB, svc); err != nil {
				return apiError(c, 500, err.Error())
			}
			c.W.WriteHeader(204)
			return nil
		}
	}
	return apiError(c, 404, "custom domain not found")
}

func listEnvVars(c *mock.Ctx) error {
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	items := []any{}
	for index, variable := range svc.EnvVars {
		items = append(items, map[string]any{
			"envVar": variable,
			"cursor": fmt.Sprintf("cur-%d", index+1),
		})
	}
	return c.JSON(200, items)
}

func putEnvVar(c *mock.Ctx) error {
	var body struct {
		Value string `json:"value"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	key := c.Params["key"]
	for i := range svc.EnvVars {
		if svc.EnvVars[i].Key == key {
			svc.EnvVars[i].Value = body.Value
			if err := saveService(c.DB, svc); err != nil {
				return apiError(c, 500, err.Error())
			}
			return c.JSON(200, svc.EnvVars[i])
		}
	}
	svc.EnvVars = append(svc.EnvVars, envVarDoc{Key: key, Value: body.Value})
	if err := saveService(c.DB, svc); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(201, envVarDoc{Key: key, Value: body.Value})
}

func deleteEnvVar(c *mock.Ctx) error {
	svc, found, err := loadService(c)
	if !found {
		return err
	}
	key := c.Params["key"]
	for i := range svc.EnvVars {
		if svc.EnvVars[i].Key == key {
			svc.EnvVars = append(svc.EnvVars[:i], svc.EnvVars[i+1:]...)
			if err := saveService(c.DB, svc); err != nil {
				return apiError(c, 500, err.Error())
			}
			c.W.WriteHeader(204)
			return nil
		}
	}
	return apiError(c, 404, "env var not found")
}

func listPostgres(c *mock.Ctx) error {
	return listWrapped(c, "postgres", "postgres")
}

func patchPostgres(c *mock.Ctx) error {
	var body struct {
		IPAllowList             []ipAllowEntryDoc `json:"ipAllowList"`
		HighAvailabilityEnabled *bool             `json:"highAvailabilityEnabled"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	var data string
	err := c.DB.QueryRow("SELECT data FROM postgres WHERE id=?", c.Params["postgresId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 404, "postgres not found")
	}
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	var pg postgresDoc
	if err := json.Unmarshal([]byte(data), &pg); err != nil {
		return apiError(c, 500, err.Error())
	}
	if body.IPAllowList != nil {
		pg.IPAllowList = body.IPAllowList
	}
	if body.HighAvailabilityEnabled != nil {
		pg.HighAvailabilityEnabled = *body.HighAvailabilityEnabled
	}
	updated, err := json.Marshal(pg)
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	if _, err := c.DB.Exec("UPDATE postgres SET data=? WHERE id=?", string(updated), pg.ID); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, pg)
}

func listKeyValue(c *mock.Ctx) error {
	return listWrapped(c, "keyvalue", "keyValue")
}

func patchKeyValue(c *mock.Ctx) error {
	var body struct {
		IPAllowList []ipAllowEntryDoc `json:"ipAllowList"`
	}
	if err := c.Bind(&body); err != nil {
		return apiError(c, 400, "invalid request body")
	}
	var data string
	err := c.DB.QueryRow("SELECT data FROM keyvalue WHERE id=?", c.Params["keyValueId"]).Scan(&data)
	if err == sql.ErrNoRows {
		return apiError(c, 404, "key value instance not found")
	}
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	var kv keyValueDoc
	if err := json.Unmarshal([]byte(data), &kv); err != nil {
		return apiError(c, 500, err.Error())
	}
	if body.IPAllowList != nil {
		kv.IPAllowList = body.IPAllowList
	}
	updated, err := json.Marshal(kv)
	if err != nil {
		return apiError(c, 500, err.Error())
	}
	if _, err := c.DB.Exec("UPDATE keyvalue SET data=? WHERE id=?", string(updated), kv.ID); err != nil {
		return apiError(c, 500, err.Error())
	}
	return c.JSON(200, kv)
}
