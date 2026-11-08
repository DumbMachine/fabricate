// Package searchmarketing provides bounded, stateful Search Console, Google
// organic SERP, and WordPress API sandboxes. Scenario data never changes routing.
package searchmarketing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

//go:embed search-console/*.json serp/*.json wordpress/*.json
var files embed.FS

type Resource struct{ id string }

func New(id string) *Resource {
	switch id {
	case "search-console", "serp", "wordpress":
	default:
		panic("unsupported search marketing resource: " + id)
	}
	return &Resource{id: id}
}
func (r *Resource) Descriptor() httpresource.Descriptor {
	hosts := map[string][]string{"search-console": {"www.googleapis.com"}, "serp": {"api.dataforseo.com"}, "wordpress": {"wordpress.example.test"}}
	// Claim only the Search Console path so Drive and Gmail can share www.googleapis.com.
	prefixes := map[string]map[string]string{"search-console": {"www.googleapis.com": "/webmasters/"}}
	spec, _ := files.ReadFile(r.id + "/openapi.json")
	return httpresource.Descriptor{ID: r.id, DisplayName: r.id, Version: "v1", OpenAPIVersion: "3.0.3", OpenAPIDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(spec)), ScenarioVersion: 1, ProviderHosts: hosts[r.id], HostPrefixes: prefixes[r.id], SDK: httpresource.SDKDescriptor{Package: "HTTP", Language: "http", Exception: "Bounded API subset; official SDK conformance has not been established."}}
}
func (r *Resource) Contract() httpresource.Contract {
	spec, _ := files.ReadFile(r.id + "/openapi.json")
	key := map[string]string{"search-console": "rows", "serp": "results", "wordpress": "posts"}[r.id]
	schema := fmt.Sprintf(`{"type":"object","additionalProperties":false,"required":[%q],"properties":{%q:{"type":"array","items":{"type":"object"}}}}`, key, key)
	return httpresource.Contract{OpenAPIJSON: spec, ScenarioJSON: []byte(schema)}
}
func (r *Resource) Scenarios() httpresource.ScenarioCodec { return codec{r.id} }
func (r *Resource) ScenarioDocuments() ([]scenario.Document, error) {
	docs := []scenario.Document{}
	for _, n := range []string{"minimal.v1", "fitfoodie.v1"} {
		b, e := files.ReadFile(r.id + "/" + n + ".json")
		if e != nil {
			return nil, e
		}
		d, e := scenario.Parse(b)
		if e != nil {
			return nil, e
		}
		docs = append(docs, d)
	}
	return docs, nil
}
func (r *Resource) Scenario(id string) (scenario.Document, error) {
	docs, e := r.ScenarioDocuments()
	if e != nil {
		return scenario.Document{}, e
	}
	d, ok := scenario.Lookup(docs, id)
	if !ok {
		return d, fmt.Errorf("unknown scenario %q", id)
	}
	return d, nil
}

type codec struct{ id string }

func (c codec) key() string {
	return map[string]string{"search-console": "rows", "serp": "results", "wordpress": "posts"}[c.id]
}
func (c codec) Validate(_ context.Context, d scenario.Document) error {
	if e := d.ValidateEnvelope(); e != nil {
		return e
	}
	if d.Resource != c.id || d.ResourceVersion != "v1" {
		return fmt.Errorf("expected %s v1", c.id)
	}
	var state map[string]json.RawMessage
	if e := json.Unmarshal(d.State, &state); e != nil {
		return e
	}
	if len(state) != 1 {
		return fmt.Errorf("unsupported state fields")
	}
	raw, ok := state[c.key()]
	if !ok {
		return fmt.Errorf("missing %s", c.key())
	}
	var items []map[string]any
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("state collection must be an array")
	}
	if e := json.Unmarshal(raw, &items); e != nil {
		return e
	}
	for _, item := range items {
		switch c.id {
		case "wordpress":
			if _, ok := item["id"].(float64); !ok {
				return fmt.Errorf("post id required")
			}
			if _, ok := item["title"].(map[string]any); !ok {
				return fmt.Errorf("post title required")
			}
		case "serp":
			if item["keyword"] == nil || item["items"] == nil {
				return fmt.Errorf("keyword and items required")
			}
		case "search-console":
			for _, key := range []string{"date", "page", "query", "clicks", "impressions", "position"} {
				if item[key] == nil {
					return fmt.Errorf("missing %s", key)
				}
			}
		}
	}
	return nil
}
func (c codec) Initialize(ctx context.Context, db *sql.DB) error {
	_, e := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS marketing_state (id INTEGER PRIMARY KEY CHECK(id=1), body TEXT NOT NULL)`)
	return e
}
func (c codec) Load(ctx context.Context, db *sql.DB, d scenario.Document) error {
	if e := c.Validate(ctx, d); e != nil {
		return e
	}
	_, e := db.ExecContext(ctx, `INSERT INTO marketing_state(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, string(d.State))
	return e
}
func (c codec) Dump(ctx context.Context, db *sql.DB, m scenario.Metadata) (scenario.Document, error) {
	var raw string
	e := db.QueryRowContext(ctx, `SELECT body FROM marketing_state WHERE id=1`).Scan(&raw)
	return scenario.Document{Contract: scenario.Contract, ContractVersion: 1, ID: m.ID, Resource: m.Resource, ResourceVersion: m.ResourceVersion, State: json.RawMessage(raw)}, e
}

type server struct {
	r       *Resource
	deps    httpresource.ServerDependencies
	handler http.Handler
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }
func (r *Resource) NewServer(ctx context.Context, d httpresource.ServerDependencies) (httpresource.Server, error) {
	token, e := d.Secrets.Get(ctx, "token")
	if e != nil {
		return nil, e
	}
	s := &server{r: r, deps: d}
	mux := http.NewServeMux()
	switch r.id {
	case "search-console":
		mux.HandleFunc("POST /webmasters/v3/sites/{siteUrl}/searchAnalytics/query", s.query)
	case "serp":
		mux.HandleFunc("POST /v3/serp/google/organic/live/advanced", s.serp)
	case "wordpress":
		mux.HandleFunc("GET /wp-json/wp/v2/posts", s.posts)
		mux.HandleFunc("GET /wp-json/wp/v2/posts/{id}", s.post)
		mux.HandleFunc("POST /wp-json/wp/v2/posts/{id}", s.post)
	}
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("Authorization") != "Bearer "+token {
			write(w, 401, map[string]any{"error": "invalid synthetic bearer token"})
			return
		}
		mux.ServeHTTP(w, q)
	})
	return s, nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		write(w, 400, map[string]any{"error": e.Error()})
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		write(w, 400, map[string]any{"error": "expected one JSON value"})
		return false
	}
	return true
}
func (s *server) state(ctx context.Context) (map[string][]map[string]any, error) {
	var raw string
	e := s.deps.DB.QueryRowContext(ctx, `SELECT body FROM marketing_state WHERE id=1`).Scan(&raw)
	if e != nil {
		return nil, e
	}
	var state map[string][]map[string]any
	e = json.Unmarshal([]byte(raw), &state)
	return state, e
}
func (s *server) read(w http.ResponseWriter, r *http.Request) (map[string][]map[string]any, bool) {
	v, e := s.state(r.Context())
	if e != nil {
		write(w, 500, map[string]any{"error": e.Error()})
		return nil, false
	}
	return v, true
}
func num(v any) float64 { n, _ := v.(float64); return n }
func (s *server) query(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Start      string   `json:"startDate"`
		End        string   `json:"endDate"`
		Dimensions []string `json:"dimensions"`
		Limit      int      `json:"rowLimit"`
		Groups     []struct {
			GroupType string `json:"groupType"`
			Filters   []struct {
				Dimension  string `json:"dimension"`
				Operator   string `json:"operator"`
				Expression string `json:"expression"`
			} `json:"filters"`
		} `json:"dimensionFilterGroups"`
	}
	if !decode(w, r, &in) {
		return
	}
	for _, d := range []string{in.Start, in.End} {
		if _, e := time.Parse("2006-01-02", d); e != nil {
			write(w, 400, map[string]any{"error": "valid startDate and endDate required"})
			return
		}
	}
	if in.Start > in.End || len(in.Dimensions) == 0 || in.Limit < 0 || in.Limit > 25000 {
		write(w, 400, map[string]any{"error": "invalid range, dimensions, or rowLimit"})
		return
	}
	for _, dim := range in.Dimensions {
		if dim != "page" && dim != "query" {
			write(w, 400, map[string]any{"error": "unsupported dimension"})
			return
		}
	}
	for _, g := range in.Groups {
		if g.GroupType != "" && g.GroupType != "and" {
			write(w, 400, map[string]any{"error": "only AND filters supported"})
			return
		}
		for _, f := range g.Filters {
			if (f.Dimension != "page" && f.Dimension != "query") || f.Operator != "equals" {
				write(w, 400, map[string]any{"error": "only page/query equals filters supported"})
				return
			}
		}
	}
	state, ok := s.read(w, r)
	if !ok {
		return
	}
	type agg struct {
		Keys        []string `json:"keys"`
		Clicks      float64  `json:"clicks"`
		Impressions float64  `json:"impressions"`
		CTR         float64  `json:"ctr"`
		Position    float64  `json:"position"`
	}
	groups := map[string]*agg{}
	site := r.PathValue("siteUrl")
	if !strings.HasPrefix(site, "https://") {
		write(w, 400, map[string]any{"error": "sandbox supports URL-prefix properties"})
		return
	}
	for _, row := range state["rows"] {
		date, _ := row["date"].(string)
		page, _ := row["page"].(string)
		if date < in.Start || date > in.End || !strings.HasPrefix(page, site) {
			continue
		}
		matched := true
		for _, g := range in.Groups {
			for _, f := range g.Filters {
				if row[f.Dimension] != f.Expression {
					matched = false
				}
			}
		}
		if !matched {
			continue
		}
		keys := []string{}
		for _, d := range in.Dimensions {
			keys = append(keys, fmt.Sprint(row[d]))
		}
		b, _ := json.Marshal(keys)
		k := string(b)
		a := groups[k]
		if a == nil {
			a = &agg{Keys: keys}
			groups[k] = a
		}
		a.Clicks += num(row["clicks"])
		a.Impressions += num(row["impressions"])
		a.Position += num(row["position"]) * num(row["impressions"])
	}
	out := []*agg{}
	for _, a := range groups {
		if a.Impressions > 0 {
			a.Position /= a.Impressions
			a.CTR = a.Clicks / a.Impressions
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Clicks > out[j].Clicks })
	limit := in.Limit
	if limit == 0 {
		limit = 1000
	}
	if len(out) > limit {
		out = out[:limit]
	}
	write(w, 200, map[string]any{"rows": out, "responseAggregationType": "byPage"})
}
func (s *server) serp(w http.ResponseWriter, r *http.Request) {
	var in []struct {
		Keyword  string `json:"keyword"`
		Location int    `json:"location_code"`
		Language string `json:"language_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in) < 1 || len(in) > 10 {
		write(w, 400, map[string]any{"error": "one to ten tasks required"})
		return
	}
	state, ok := s.read(w, r)
	if !ok {
		return
	}
	tasks := []any{}
	for _, task := range in {
		var result any
		for _, row := range state["results"] {
			if row["keyword"] == task.Keyword && int(num(row["location_code"])) == task.Location && row["language_code"] == task.Language {
				result = row
				break
			}
		}
		if result == nil {
			write(w, 422, map[string]any{"error": "keyword, location, language combination not present in sandbox"})
			return
		}
		id, e := s.deps.IDs.Next(r.Context(), "serp-task")
		if e != nil {
			write(w, 500, map[string]any{"error": e.Error()})
			return
		}
		tasks = append(tasks, map[string]any{"id": id, "status_code": 20000, "status_message": "Ok.", "cost": 0, "result_count": 1, "result": []any{result}})
	}
	write(w, 200, map[string]any{"status_code": 20000, "status_message": "Ok.", "tasks_count": len(tasks), "tasks_error": 0, "cost": 0, "tasks": tasks})
}
func (s *server) posts(w http.ResponseWriter, r *http.Request) {
	state, ok := s.read(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) > 0 {
		write(w, 400, map[string]any{"error": "list query options are outside this bounded sandbox"})
		return
	}
	write(w, 200, state["posts"])
}
func (s *server) post(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.Atoi(r.PathValue("id"))
	if e != nil || id < 1 {
		write(w, 400, map[string]any{"error": "positive post id required"})
		return
	}
	if r.Method == "GET" {
		state, ok := s.read(w, r)
		if !ok {
			return
		}
		for _, post := range state["posts"] {
			if int(num(post["id"])) == id {
				write(w, 200, post)
				return
			}
		}
		write(w, 404, map[string]any{"code": "rest_post_invalid_id"})
		return
	}
	var patch struct {
		Title   *string `json:"title"`
		Content *string `json:"content"`
	}
	if !decode(w, r, &patch) {
		return
	}
	if patch.Title == nil && patch.Content == nil {
		write(w, 400, map[string]any{"error": "title or content required"})
		return
	}
	if patch.Title != nil && (len(*patch.Title) == 0 || len(*patch.Title) > 300) || patch.Content != nil && len(*patch.Content) > 100000 {
		write(w, 400, map[string]any{"error": "invalid title or content length"})
		return
	}
	tx, e := s.deps.DB.BeginTx(r.Context(), nil)
	if e != nil {
		write(w, 500, map[string]any{"error": e.Error()})
		return
	}
	defer tx.Rollback()
	var raw string
	if e = tx.QueryRowContext(r.Context(), `SELECT body FROM marketing_state WHERE id=1`).Scan(&raw); e != nil {
		write(w, 500, map[string]any{"error": e.Error()})
		return
	}
	var state map[string][]map[string]any
	if e = json.Unmarshal([]byte(raw), &state); e != nil {
		write(w, 500, map[string]any{"error": e.Error()})
		return
	}
	for _, post := range state["posts"] {
		if int(num(post["id"])) != id {
			continue
		}
		if patch.Title != nil {
			post["title"] = map[string]any{"raw": *patch.Title, "rendered": *patch.Title}
		}
		if patch.Content != nil {
			post["content"] = map[string]any{"raw": *patch.Content, "rendered": *patch.Content}
		}
		post["modified_gmt"] = s.deps.Clock.Now().UTC().Format("2006-01-02T15:04:05")
		b, e := json.Marshal(state)
		if e == nil {
			_, e = tx.ExecContext(r.Context(), `UPDATE marketing_state SET body=? WHERE id=1`, string(b))
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			write(w, 500, map[string]any{"error": e.Error()})
			return
		}
		write(w, 200, post)
		return
	}
	write(w, 404, map[string]any{"code": "rest_post_invalid_id"})
}
