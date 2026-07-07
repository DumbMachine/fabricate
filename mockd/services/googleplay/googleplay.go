// Package googleplay is a stateful mock of the subset of the Google Play APIs
// a support-review agent uses: the Play Developer Reporting
// apps:search (app discovery) and Android Publisher reviews.list / reviews.get /
// reviews.reply. It is backed by SQLite, so replying to a review actually
// mutates state — the next reviews.list shows the developer comment and the
// unanswered backlog shrinks. That makes the support-agent demo live, not
// canned.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"apps":[{"packageName","displayName"}],
//	 "reviews":[{"reviewId","packageName","author","text","starRating","language",
//	             "device","appVersion","lastModified","developerReply","replyModified"}]}
package googleplay

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the google-play service.
func New() *mock.Service {
	s := mock.NewService("google-play")
	s.Tables = []mock.Table{
		{Name: "apps", DDL: "package_name TEXT PRIMARY KEY, display_name TEXT"},
		{Name: "reviews", DDL: `
			review_id TEXT PRIMARY KEY,
			package_name TEXT NOT NULL,
			author TEXT, text TEXT, star_rating INTEGER,
			language TEXT, device TEXT, app_version TEXT,
			last_modified INTEGER,
			developer_reply TEXT, reply_modified INTEGER`},
	}
	s.Seed = seed

	// Reporting API: enumerate the apps the service account can see.
	s.GET("/v1beta1/apps:search", listApps)

	// Android Publisher reviews: list (paginated), get one, and the reply action.
	s.GET("/androidpublisher/v3/applications/{packageName}/reviews", listReviews)
	s.GET("/androidpublisher/v3/applications/{packageName}/reviews/{reviewId}", getReview)
	s.POST("/androidpublisher/v3/applications/{packageName}/reviews/{reviewId}:reply", replyReview)

	return s
}

// ---------- seed ----------

type fixture struct {
	Apps []struct {
		PackageName string `json:"packageName"`
		DisplayName string `json:"displayName"`
	} `json:"apps"`
	Reviews []struct {
		ReviewID       string `json:"reviewId"`
		PackageName    string `json:"packageName"`
		Author         string `json:"author"`
		Text           string `json:"text"`
		StarRating     int    `json:"starRating"`
		Language       string `json:"language"`
		Device         string `json:"device"`
		AppVersion     string `json:"appVersion"`
		LastModified   int64  `json:"lastModified"`
		DeveloperReply string `json:"developerReply"`
		ReplyModified  int64  `json:"replyModified"`
	} `json:"reviews"`
}

func seed(db *sql.DB, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("google-play seed: parse fixture: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, a := range f.Apps {
		if _, err := tx.Exec("INSERT OR REPLACE INTO apps(package_name, display_name) VALUES(?,?)", a.PackageName, a.DisplayName); err != nil {
			return err
		}
	}
	for _, r := range f.Reviews {
		var reply any
		if r.DeveloperReply != "" {
			reply = r.DeveloperReply
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO reviews
			(review_id, package_name, author, text, star_rating, language, device, app_version, last_modified, developer_reply, reply_modified)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			r.ReviewID, r.PackageName, r.Author, r.Text, r.StarRating, r.Language, r.Device, r.AppVersion, r.LastModified, reply, r.ReplyModified); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- handlers ----------

func listApps(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT package_name, display_name FROM apps ORDER BY package_name")
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	defer rows.Close()
	apps := []any{}
	for rows.Next() {
		var pkg, name string
		if err := rows.Scan(&pkg, &name); err != nil {
			return c.GErr(500, "INTERNAL", err.Error())
		}
		apps = append(apps, map[string]any{"name": "apps/" + pkg, "displayName": name})
	}
	return c.JSON(200, map[string]any{"apps": apps})
}

func listReviews(c *mock.Ctx) error {
	pkg := c.Params["packageName"]
	limit := clamp(atoiOr(c.Query.Get("maxResults"), 50), 1, 100)
	offset := atoiOr(c.Query.Get("token"), 0)

	// Fetch one extra row to know whether another page exists.
	rows, err := c.DB.Query(`SELECT review_id, author, text, star_rating, language, device, app_version,
		last_modified, developer_reply, reply_modified
		FROM reviews WHERE package_name=? ORDER BY last_modified DESC, review_id LIMIT ? OFFSET ?`,
		pkg, limit+1, offset)
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	defer rows.Close()

	var items []any
	more := false
	for rows.Next() {
		if len(items) == limit {
			more = true
			break
		}
		r, err := scanReview(rows)
		if err != nil {
			return c.GErr(500, "INTERNAL", err.Error())
		}
		items = append(items, r.envelope())
	}
	if items == nil {
		items = []any{}
	}
	resp := map[string]any{"reviews": items}
	if more {
		resp["tokenPagination"] = map[string]any{"nextPageToken": strconv.Itoa(offset + limit)}
	}
	return c.JSON(200, resp)
}

func getReview(c *mock.Ctx) error {
	row := c.DB.QueryRow(`SELECT review_id, author, text, star_rating, language, device, app_version,
		last_modified, developer_reply, reply_modified
		FROM reviews WHERE package_name=? AND review_id=?`, c.Params["packageName"], c.Params["reviewId"])
	r, err := scanReviewRow(row)
	if err == sql.ErrNoRows {
		return c.GErr(404, "NOT_FOUND", "review not found")
	}
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	return c.JSON(200, r.envelope())
}

// replyReview is the stateful action: it records the developer reply, so the
// review now reads as answered everywhere.
func replyReview(c *mock.Ctx) error {
	var body struct {
		ReplyText string `json:"replyText"`
	}
	if err := c.Bind(&body); err != nil {
		return c.GErr(400, "INVALID_ARGUMENT", "invalid body")
	}
	if body.ReplyText == "" {
		return c.GErr(400, "INVALID_ARGUMENT", "replyText is required")
	}
	now := time.Now().Unix()
	res, err := c.DB.Exec(`UPDATE reviews SET developer_reply=?, reply_modified=? WHERE package_name=? AND review_id=?`,
		body.ReplyText, now, c.Params["packageName"], c.Params["reviewId"])
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return c.GErr(404, "NOT_FOUND", "review not found")
	}
	return c.JSON(200, map[string]any{"result": map[string]any{
		"replyText":  body.ReplyText,
		"lastEdited": map[string]any{"seconds": strconv.FormatInt(now, 10)},
	}})
}

// ---------- review row → Android Publisher envelope ----------

type review struct {
	id, author, text, lang, device, appVer string
	stars                                  int
	lastModified                           int64
	reply                                  sql.NullString
	replyModified                          int64
}

func scanReview(rows *sql.Rows) (*review, error) {
	var r review
	err := rows.Scan(&r.id, &r.author, &r.text, &r.stars, &r.lang, &r.device, &r.appVer, &r.lastModified, &r.reply, &r.replyModified)
	return &r, err
}

func scanReviewRow(row *sql.Row) (*review, error) {
	var r review
	err := row.Scan(&r.id, &r.author, &r.text, &r.stars, &r.lang, &r.device, &r.appVer, &r.lastModified, &r.reply, &r.replyModified)
	return &r, err
}

func (r *review) envelope() map[string]any {
	comments := []any{map[string]any{"userComment": map[string]any{
		"text":             r.text,
		"starRating":       r.stars,
		"reviewerLanguage": r.lang,
		"device":           r.device,
		"appVersionName":   r.appVer,
		"lastModified":     map[string]any{"seconds": strconv.FormatInt(r.lastModified, 10)},
	}}}
	if r.reply.Valid && r.reply.String != "" {
		comments = append(comments, map[string]any{"developerComment": map[string]any{
			"text":         r.reply.String,
			"lastModified": map[string]any{"seconds": strconv.FormatInt(r.replyModified, 10)},
		}})
	}
	return map[string]any{"reviewId": r.id, "authorName": r.author, "comments": comments}
}

// ---------- small helpers ----------

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
