package mock

import (
	"database/sql"
	"io"
	"log"
	"net/http"
)

// Service is one mockable API. It declares its SQLite schema, an optional seed
// (loads fixture rows), and registers handlers Express-style. It implements
// http.Handler.
type Service struct {
	Name   string
	Tables []Table
	// Seed loads fixture rows into the freshly-created DB. fixture is the raw
	// bytes of the mounted seed file (may be empty).
	Seed func(db *sql.DB, fixture []byte) error

	router router
	db     *sql.DB
}

// NewService returns an empty service with the given name.
func NewService(name string) *Service { return &Service{Name: name} }

func (s *Service) GET(p string, h Handler)    { s.router.handle("GET", p, h) }
func (s *Service) POST(p string, h Handler)   { s.router.handle("POST", p, h) }
func (s *Service) PUT(p string, h Handler)    { s.router.handle("PUT", p, h) }
func (s *Service) PATCH(p string, h Handler)  { s.router.handle("PATCH", p, h) }
func (s *Service) DELETE(p string, h Handler) { s.router.handle("DELETE", p, h) }

// DB exposes the database (handlers reach it via Ctx; this is for tests/seed).
func (s *Service) DB() *sql.DB { return s.db }

// Init opens the DB at dbPath, creates tables, and runs the seed.
func (s *Service) Init(dbPath string, fixture []byte) error {
	db, err := OpenDB(dbPath, s.Tables)
	if err != nil {
		return err
	}
	s.db = db
	if s.Seed != nil {
		if err := s.Seed(db, fixture); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		return
	}
	h, params, ok := s.router.match(r.Method, r.URL.Path)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":404,"status":"NOT_FOUND","message":"no mock route for this method+path"}}`)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	c := &Ctx{W: w, R: r, DB: s.db, Params: params, Query: r.URL.Query(), Body: body}
	if err := h(c); err != nil {
		log.Printf("mock %s: %s %s: %v", s.Name, r.Method, r.URL.Path, err)
	}
}
