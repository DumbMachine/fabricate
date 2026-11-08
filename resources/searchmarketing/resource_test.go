package searchmarketing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	_ "modernc.org/sqlite"
)

type secrets struct{}

func (secrets) Get(context.Context, string) (string, error) { return "synthetic", nil }

type ids struct{ next int }

func (i *ids) Next(context.Context, string) (string, error) { i.next++; return fmt.Sprint(i.next), nil }
func fixture(t *testing.T, id string) (*Resource, *sql.DB, httpresource.Server) {
	t.Helper()
	r := New(id)
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	c := r.Scenarios()
	if e = c.Initialize(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	d, e := r.Scenario(id + ".fitfoodie.v1")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Load(context.Background(), db, d); e != nil {
		t.Fatal(e)
	}
	s, e := r.NewServer(context.Background(), httpresource.ServerDependencies{DB: db, Clock: httpresource.FixedClock{Time: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)}, IDs: &ids{}, Secrets: secrets{}})
	if e != nil {
		t.Fatal(e)
	}
	return r, db, s
}
func request(s httpresource.Server, method, path, body string) *httptest.ResponseRecorder {
	q := httptest.NewRequest(method, path, strings.NewReader(body))
	q.Header.Set("Authorization", "Bearer synthetic")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, q)
	return w
}
func TestScenarioRoundtripAndIsolation(t *testing.T) {
	for _, id := range []string{"search-console", "serp", "wordpress"} {
		t.Run(id, func(t *testing.T) {
			r, db, s := fixture(t, id)
			doc, _ := r.Scenario(id + ".fitfoodie.v1")
			dump, e := r.Scenarios().Dump(context.Background(), db, doc.Metadata())
			if e != nil {
				t.Fatal(e)
			}
			a, _ := json.Marshal(json.RawMessage(doc.State))
			var x, y any
			json.Unmarshal(a, &x)
			json.Unmarshal(dump.State, &y)
			aa, _ := json.Marshal(x)
			bb, _ := json.Marshal(y)
			if string(aa) != string(bb) {
				t.Fatal("roundtrip changed state")
			}
			doc.State = json.RawMessage(`{"unsupported":[]}`)
			if r.Scenarios().Validate(context.Background(), doc) == nil {
				t.Fatal("accepted unsupported state")
			}
			if id == "wordpress" {
				for _, post := range []string{"1", "2"} {
					w := request(s, "POST", "/wp-json/wp/v2/posts/"+post, `{"title":"Reviewed title"}`)
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
					w = request(s, "GET", "/wp-json/wp/v2/posts/"+post, "")
					if !strings.Contains(w.Body.String(), "Reviewed title") {
						t.Fatal(w.Body.String())
					}
				}
				_, _, other := fixture(t, id)
				w := request(other, "GET", "/wp-json/wp/v2/posts/1", "")
				if strings.Contains(w.Body.String(), "Reviewed title") {
					t.Fatal("mutation crossed instance")
				}
			}
		})
	}
}
func TestReadAPIsAndUnsupportedSurfaces(t *testing.T) {
	_, _, gsc := fixture(t, "search-console")
	url := "/webmasters/v3/sites/https%3A%2F%2Ffitfoodiefinds.com%2F/searchAnalytics/query"
	w := request(gsc, "POST", url, `{"startDate":"2026-09-11","endDate":"2026-10-08","dimensions":["page","query"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"clicks":1484`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(gsc, "POST", url, `{"startDate":"2026-09-11","endDate":"2026-10-08","dimensions":["device"]}`)
	if w.Code != 400 {
		t.Fatal("unsupported dimension", w.Code)
	}
	_, _, serp := fixture(t, "serp")
	for _, q := range []string{"best beef chili recipe", "overnight oats"} {
		w = request(serp, "POST", "/v3/serp/google/organic/live/advanced", `[{"keyword":"`+q+`","location_code":2840,"language_code":"en"}]`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ai_overview") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request(serp, "POST", "/v3/serp/google/organic/live/advanced", `[{"keyword":"unsupported","location_code":2840,"language_code":"en"}]`)
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
	w = request(serp, "DELETE", "/v3/serp/google/organic/live/advanced", "")
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}
