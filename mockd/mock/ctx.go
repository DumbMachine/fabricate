package mock

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
)

// Handler is a service route handler. Returning an error after the response is
// already written is best-effort logged by the framework; handlers signal real
// API errors via c.GErr and return nil.
type Handler func(c *Ctx) error

// Ctx is the per-request handle a handler receives: the live DB, captured path
// params, query, and decoded body, plus response helpers.
type Ctx struct {
	W      http.ResponseWriter
	R      *http.Request
	DB     *sql.DB
	Params map[string]string
	Query  url.Values
	Body   []byte
}

// JSON writes a JSON response with the given status.
func (c *Ctx) JSON(status int, v any) error {
	c.W.Header().Set("Content-Type", "application/json")
	c.W.WriteHeader(status)
	return json.NewEncoder(c.W).Encode(v)
}

// Bind unmarshals the request body into v (no-op on empty body).
func (c *Ctx) Bind(v any) error {
	if len(c.Body) == 0 {
		return nil
	}
	return json.Unmarshal(c.Body, v)
}

// GErr writes a Google-style error envelope: {"error":{code,status,message}}.
func (c *Ctx) GErr(code int, status, message string) error {
	return c.JSON(code, map[string]any{"error": map[string]any{
		"code": code, "status": status, "message": message,
	}})
}
