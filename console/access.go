package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type principal struct {
	Admin   bool
	Project string
	TokenID string
}
type principalKey struct{}

func actor(ctx context.Context) principal {
	if p, ok := ctx.Value(principalKey{}).(principal); ok {
		return p
	}
	return principal{Admin: true, Project: "default"} // trusted in-process callers
}
func withActor(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

type apiError struct {
	Status  int
	Message string
}

func (e apiError) Error() string             { return e.Message }
func problem(code int, message string) error { return apiError{code, message} }
func errorStatus(err error) int {
	var e apiError
	if errors.As(err, &e) {
		return e.Status
	}
	return 500
}

type Project struct {
	ID                string `json:"id" jsonschema:"Project namespace; lowercase letters, digits and hyphens"`
	Name              string `json:"name,omitempty"`
	MaxSandboxes      int    `json:"maxSandboxes,omitempty"`
	DefaultTTLSeconds int64  `json:"defaultTTLSeconds,omitempty"`
	MaxTTLSeconds     int64  `json:"maxTTLSeconds,omitempty"`
}
type projectTokenRequest struct {
	Name       string `json:"name"`
	TTLSeconds int64  `json:"ttlSeconds,omitempty"`
}
type ProjectToken struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expiresAt"`
	Revoked   bool      `json:"revoked"`
}

func tokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomToken() (string, error) {
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	return "fab_p_" + hex.EncodeToString(raw), err
}
func (m *Manager) project(ctx context.Context, requested string) (Project, error) {
	p := actor(ctx)
	if requested == "" {
		requested = p.Project
	}
	if requested == "" {
		requested = "default"
	}
	if !p.Admin && requested != p.Project {
		return Project{}, problem(403, "project is outside this credential's scope")
	}
	if requested == "default" {
		return Project{ID: "default", Name: "Default", MaxSandboxes: m.capacity, MaxTTLSeconds: 31 * 86400}, nil
	}
	if m.storage == nil {
		return Project{}, problem(400, "projects require mounted storage")
	}
	var raw []byte
	if err := m.storage.db.QueryRow("SELECT record FROM projects WHERE id=?", requested).Scan(&raw); err != nil {
		return Project{}, problem(404, "project not found")
	}
	var out Project
	if json.Unmarshal(raw, &out) != nil {
		return Project{}, fmt.Errorf("invalid stored project")
	}
	return out, nil
}
func (m *Manager) authenticateProject(token string) (principal, bool) {
	if m.storage == nil || m.storage.db == nil || len(token) > 256 || !strings.HasPrefix(token, "fab_p_") {
		return principal{}, false
	}
	var p principal
	var expiry int64
	err := m.storage.db.QueryRow("SELECT id,project,expires FROM project_tokens WHERE digest=? AND revoked=0", tokenDigest(token)).Scan(&p.TokenID, &p.Project, &expiry)
	return p, err == nil && expiry > time.Now().Unix()
}
func (m *Manager) ListFor(ctx context.Context) []Session {
	p := actor(ctx)
	out := []Session{}
	for _, s := range m.List() {
		if (p.Admin || s.Project == p.Project) && !expired(s.ExpiresAt, time.Now()) {
			out = append(out, s)
		}
	}
	return out
}
func (m *Manager) GetFor(ctx context.Context, id string) (Session, bool) {
	s, ok := m.Get(id)
	p := actor(ctx)
	return s, ok && (p.Admin || s.Project == p.Project) && !expired(s.ExpiresAt, time.Now())
}
func (m *Manager) scopeControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := actor(r.Context())
		if strings.HasPrefix(r.URL.Path, "/api/projects") && !p.Admin {
			writeError(w, 403, fmt.Errorf("operator access required"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/sessions/") {
			id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
			_, visible := m.GetFor(r.Context(), id)
			if !visible && r.Method == "DELETE" && m.storage != nil {
				record, status, err := m.storage.session(id)
				if err != nil {
					writeError(w, 500, fmt.Errorf("sandbox lifecycle unavailable"))
					return
				}
				visible = status != "" && (p.Admin || record.Project == p.Project)
			}
			if !visible {
				// Repeated delete is idempotent, including expired/deleted resources; it
				// never operates on an existing sandbox in another project.
				if r.Method == "DELETE" {
					w.WriteHeader(204)
				} else {
					writeError(w, 404, fmt.Errorf("sandbox not found"))
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func decodeControl(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		writeError(w, 400, fmt.Errorf("invalid request"))
		return false
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, 400, fmt.Errorf("one JSON request required"))
		return false
	}
	return true
}
func (m *Manager) handleProjects(w http.ResponseWriter, r *http.Request) {
	if m.storage == nil {
		writeError(w, 400, fmt.Errorf("projects require mounted storage"))
		return
	}
	if r.Method == "GET" {
		rows, err := m.storage.db.Query("SELECT record FROM projects ORDER BY id")
		if err != nil {
			writeError(w, 500, err)
			return
		}
		defer rows.Close()
		out := []Project{}
		for rows.Next() {
			var raw []byte
			var p Project
			if rows.Scan(&raw) != nil || json.Unmarshal(raw, &p) != nil {
				writeError(w, 500, fmt.Errorf("invalid project record"))
				return
			}
			out = append(out, p)
		}
		if err = rows.Err(); err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	var p Project
	if !decodeControl(w, r, &p) {
		return
	}
	if !projectPattern.MatchString(p.ID) || p.ID == "default" {
		writeError(w, 400, fmt.Errorf("invalid or reserved project id"))
		return
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.MaxSandboxes == 0 {
		p.MaxSandboxes = 8
	}
	if p.DefaultTTLSeconds == 0 {
		p.DefaultTTLSeconds = 86400
	}
	if p.MaxTTLSeconds == 0 {
		p.MaxTTLSeconds = 7 * 86400
	}
	if p.MaxSandboxes < 1 || p.MaxSandboxes > m.capacity || p.DefaultTTLSeconds < 1 || p.MaxTTLSeconds < p.DefaultTTLSeconds || p.MaxTTLSeconds > 31*86400 {
		writeError(w, 400, fmt.Errorf("invalid project capacity or TTL policy"))
		return
	}
	raw, _ := json.Marshal(p)
	if _, err := m.storage.db.Exec("INSERT INTO projects(id,record) VALUES(?,?) ON CONFLICT DO NOTHING", p.ID, raw); err != nil {
		writeError(w, 500, err)
		return
	}
	var saved []byte
	if err := m.storage.db.QueryRow("SELECT record FROM projects WHERE id=?", p.ID).Scan(&saved); err != nil {
		writeError(w, 500, err)
		return
	}
	if string(saved) != string(raw) {
		writeError(w, 409, fmt.Errorf("project already exists with different policy"))
		return
	}
	writeJSON(w, 200, p)
}
func (m *Manager) handleProjectTokens(w http.ResponseWriter, r *http.Request) {
	if m.storage == nil {
		writeError(w, 400, fmt.Errorf("project credentials require mounted storage"))
		return
	}
	p, err := m.project(r.Context(), r.PathValue("project"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	if r.Method == "GET" {
		rows, e := m.storage.db.Query("SELECT id,name,expires,revoked FROM project_tokens WHERE project=? ORDER BY id", p.ID)
		if e != nil {
			writeError(w, 500, e)
			return
		}
		defer rows.Close()
		out := []ProjectToken{}
		for rows.Next() {
			item := ProjectToken{Project: p.ID}
			var expiry int64
			if e = rows.Scan(&item.ID, &item.Name, &expiry, &item.Revoked); e != nil {
				writeError(w, 500, e)
				return
			}
			item.ExpiresAt = time.Unix(expiry, 0).UTC()
			out = append(out, item)
		}
		if e = rows.Err(); e != nil {
			writeError(w, 500, e)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	if r.Method == "DELETE" {
		_, err = m.storage.db.Exec("UPDATE project_tokens SET revoked=1 WHERE project=? AND id=?", p.ID, r.PathValue("token"))
		if err != nil {
			writeError(w, 500, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	operator, err := ControlTokenFromEnvironment()
	if err != nil || operator == "" {
		writeError(w, 400, fmt.Errorf("configure operator authentication before issuing project credentials"))
		return
	}
	var input projectTokenRequest
	if !decodeControl(w, r, &input) {
		return
	}
	if input.Name == "" {
		writeError(w, 400, fmt.Errorf("credential name required"))
		return
	}
	if input.TTLSeconds == 0 {
		input.TTLSeconds = 86400
	}
	if input.TTLSeconds < 1 || input.TTLSeconds > 31*86400 {
		writeError(w, 400, fmt.Errorf("credential TTL must be 1 second to 31 days"))
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id := tokenDigest(token)[:24]
	expiry := time.Now().Add(time.Duration(input.TTLSeconds) * time.Second).UTC()
	_, err = m.storage.db.Exec("INSERT INTO project_tokens(id,project,name,digest,expires,revoked) VALUES(?,?,?,?,?,0)", id, p.ID, input.Name, tokenDigest(token), expiry.Unix())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"credential": ProjectToken{ID: id, Project: p.ID, Name: input.Name, ExpiresAt: expiry}, "token": token})
}

// Policy changes affect new admissions and renewals; they never silently delete
// existing sandboxes or change a project's identity or credential scope.
type projectPolicyUpdate struct {
	Name              *string `json:"name,omitempty"`
	MaxSandboxes      *int    `json:"maxSandboxes,omitempty"`
	DefaultTTLSeconds *int64  `json:"defaultTTLSeconds,omitempty"`
	MaxTTLSeconds     *int64  `json:"maxTTLSeconds,omitempty"`
}

func (m *Manager) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("project") == "default" {
		writeError(w, 400, fmt.Errorf("configure default-project capacity with the deployment limit"))
		return
	}
	project, err := m.project(r.Context(), r.PathValue("project"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	var update projectPolicyUpdate
	if !decodeControl(w, r, &update) {
		return
	}
	if update.Name != nil {
		project.Name = *update.Name
	}
	if update.MaxSandboxes != nil {
		project.MaxSandboxes = *update.MaxSandboxes
	}
	if update.DefaultTTLSeconds != nil {
		project.DefaultTTLSeconds = *update.DefaultTTLSeconds
	}
	if update.MaxTTLSeconds != nil {
		project.MaxTTLSeconds = *update.MaxTTLSeconds
	}
	if strings.TrimSpace(project.Name) == "" || project.MaxSandboxes < 1 || project.MaxSandboxes > m.capacity || project.DefaultTTLSeconds < 1 || project.MaxTTLSeconds < project.DefaultTTLSeconds || project.MaxTTLSeconds > 31*86400 {
		writeError(w, 400, fmt.Errorf("invalid project capacity or TTL policy"))
		return
	}
	raw, _ := json.Marshal(project)
	if _, err = m.storage.db.Exec("UPDATE projects SET record=? WHERE id=?", raw, project.ID); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, project)
}
