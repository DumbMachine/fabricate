package socialstate

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/dumbmachine/fabricate/httpresource"
	"net/http"
	"strings"
)

type Server struct {
	D      httpresource.ServerDependencies
	Routes http.Handler
	Token  string
}

func New(d httpresource.ServerDependencies) (*Server, error) {
	token, err := d.Secrets.Get(context.Background(), "token")
	return &Server{D: d, Token: token}, err
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, b := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))), sha256.Sum256([]byte(s.Token))
		if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
			http.Error(w, `{"message":"invalid_token"}`, 401)
			return
		}
		s.Routes.ServeHTTP(w, r)
	})
}
func (s *Server) Close(context.Context) error { return nil }
func (s *Server) Account(ctx context.Context) (map[string]string, error) {
	var raw string
	err := s.D.DB.QueryRowContext(ctx, "SELECT payload FROM social_account").Scan(&raw)
	var a map[string]string
	if err == nil {
		err = json.Unmarshal([]byte(raw), &a)
	}
	return a, err
}
func (s *Server) ID(ctx context.Context) (string, error) {
	v, err := s.D.IDs.Next(ctx, "post")
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%d", 1000000000000000000+binary.BigEndian.Uint64(sum[:8])%8000000000000000000), err
}
func (s *Server) Put(ctx context.Context, kind, id string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.D.DB.ExecContext(ctx, "INSERT INTO social_objects(kind,id,payload) VALUES(?,?,?)", kind, id, string(b))
	return err
}
func (s *Server) Get(ctx context.Context, kind, id string) (map[string]any, error) {
	var raw string
	err := s.D.DB.QueryRowContext(ctx, "SELECT payload FROM social_objects WHERE kind=? AND id=?", kind, id).Scan(&raw)
	var value map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(raw), &value)
	}
	return value, err
}
func (s *Server) List(ctx context.Context, kind string) ([]map[string]any, error) {
	rows, err := s.D.DB.QueryContext(ctx, "SELECT payload FROM social_objects WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v map[string]any
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func Convert[T any](v any) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var t T
	if err = json.Unmarshal(b, &t); err != nil {
		panic(err)
	}
	return t
}

var _ = sql.ErrNoRows
