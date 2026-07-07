// Package gmail is a stateful mock of the Gmail API v1 subset an
// email-triage agent uses: users.getProfile, labels.list/get,
// messages.list/get/modify/trash, messages.send, and threads.list/get.
// It is backed by SQLite, so writes actually mutate state — sending a
// reply appends a SENT message to the thread, modify flips UNREAD off,
// and the next messages.list reflects both.
//
// Search (the q parameter) supports the subset agents lean on:
// `is:unread`, `is:starred`, `in:<label>`, `label:<label>`,
// `from:<substring>`, `to:<substring>`, `subject:<substring>`, and bare
// words (matched against subject + body, case-insensitive). Unknown
// operators are ignored rather than erroring.
//
// Fixture (mounted JSON, loaded at boot):
//
//	{"emailAddress": "me@example.com",
//	 "labels":[{"id","name","type"}],                       // system labels auto-added
//	 "messages":[{"id","threadId","from","to","subject",
//	              "body","labelIds":[...],"internalDate"}]}  // internalDate: epoch millis
package gmail

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

// New builds the gmail service.
func New() *mock.Service {
	s := mock.NewService("gmail")
	s.Tables = []mock.Table{
		{Name: "meta", DDL: "id TEXT PRIMARY KEY, value TEXT NOT NULL"},
		{Name: "labels", DDL: "id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL"},
		{Name: "messages", DDL: `
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			from_addr TEXT, to_addr TEXT, subject TEXT, body TEXT,
			internal_date INTEGER NOT NULL,
			label_ids TEXT NOT NULL DEFAULT ''`},
	}
	s.Seed = seed

	s.GET("/gmail/v1/users/{userId}/profile", getProfile)
	s.GET("/gmail/v1/users/{userId}/labels", listLabels)
	s.GET("/gmail/v1/users/{userId}/labels/{id}", getLabel)
	s.GET("/gmail/v1/users/{userId}/messages", listMessages)
	s.GET("/gmail/v1/users/{userId}/messages/{id}", getMessage)
	s.POST("/gmail/v1/users/{userId}/messages/{id}/modify", modifyMessage)
	s.POST("/gmail/v1/users/{userId}/messages/{id}/trash", trashMessage)
	s.POST("/gmail/v1/users/{userId}/messages/send", sendMessage)
	s.GET("/gmail/v1/users/{userId}/threads", listThreads)
	s.GET("/gmail/v1/users/{userId}/threads/{id}", getThread)

	return s
}

// systemLabels are always present, fixture or not, mirroring a real
// mailbox. A fixture can add user labels on top.
var systemLabels = []struct{ id, name string }{
	{"INBOX", "INBOX"}, {"UNREAD", "UNREAD"}, {"SENT", "SENT"},
	{"TRASH", "TRASH"}, {"STARRED", "STARRED"}, {"IMPORTANT", "IMPORTANT"},
}

// ---------- seed ----------

type fixture struct {
	EmailAddress string `json:"emailAddress"`
	Labels       []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"labels"`
	Messages []struct {
		ID           string   `json:"id"`
		ThreadID     string   `json:"threadId"`
		From         string   `json:"from"`
		To           string   `json:"to"`
		Subject      string   `json:"subject"`
		Body         string   `json:"body"`
		LabelIDs     []string `json:"labelIds"`
		InternalDate int64    `json:"internalDate"`
	} `json:"messages"`
}

func seed(db *sql.DB, raw []byte) error {
	var f fixture
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("gmail seed: parse fixture: %w", err)
		}
	}
	if f.EmailAddress == "" {
		f.EmailAddress = "me@example.com"
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR REPLACE INTO meta(id, value) VALUES('emailAddress', ?)", f.EmailAddress); err != nil {
		return err
	}
	for _, l := range systemLabels {
		if _, err := tx.Exec("INSERT OR IGNORE INTO labels(id, name, type) VALUES(?,?,?)", l.id, l.name, "system"); err != nil {
			return err
		}
	}
	for _, l := range f.Labels {
		typ := l.Type
		if typ == "" {
			typ = "user"
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO labels(id, name, type) VALUES(?,?,?)", l.ID, l.Name, typ); err != nil {
			return err
		}
	}
	for _, m := range f.Messages {
		threadID := m.ThreadID
		if threadID == "" {
			threadID = m.ID
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO messages
			(id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids)
			VALUES(?,?,?,?,?,?,?,?)`,
			m.ID, threadID, m.From, m.To, m.Subject, m.Body, m.InternalDate, strings.Join(m.LabelIDs, " ")); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- message row ----------

type message struct {
	id, threadID, from, to, subject, body string
	internalDate                          int64
	labelIDs                              []string
}

func scanMessage(sc interface{ Scan(...any) error }) (*message, error) {
	var m message
	var labels string
	if err := sc.Scan(&m.id, &m.threadID, &m.from, &m.to, &m.subject, &m.body, &m.internalDate, &labels); err != nil {
		return nil, err
	}
	m.labelIDs = splitLabels(labels)
	return &m, nil
}

const messageCols = "id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids"

func splitLabels(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	return strings.Fields(s)
}

func (m *message) hasLabel(id string) bool {
	for _, l := range m.labelIDs {
		if l == id {
			return true
		}
	}
	return false
}

func (m *message) snippet() string {
	s := strings.Join(strings.Fields(m.body), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// full renders the messages.get envelope: metadata headers + the plain
// body as base64url payload data, the way real clients decode it.
func (m *message) full() map[string]any {
	headers := []any{
		map[string]any{"name": "From", "value": m.from},
		map[string]any{"name": "To", "value": m.to},
		map[string]any{"name": "Subject", "value": m.subject},
		map[string]any{"name": "Date", "value": time.UnixMilli(m.internalDate).UTC().Format(time.RFC1123Z)},
	}
	return map[string]any{
		"id":           m.id,
		"threadId":     m.threadID,
		"labelIds":     m.labelIDs,
		"snippet":      m.snippet(),
		"internalDate": strconv.FormatInt(m.internalDate, 10),
		"sizeEstimate": len(m.body),
		"payload": map[string]any{
			"mimeType": "text/plain",
			"headers":  headers,
			"body": map[string]any{
				"size": len(m.body),
				"data": base64.URLEncoding.EncodeToString([]byte(m.body)),
			},
		},
	}
}

func loadMessages(db *sql.DB) ([]*message, error) {
	rows, err := db.Query("SELECT " + messageCols + " FROM messages ORDER BY internal_date DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- handlers ----------

func getProfile(c *mock.Ctx) error {
	var email string
	if err := c.DB.QueryRow("SELECT value FROM meta WHERE id='emailAddress'").Scan(&email); err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	var msgs, threads int
	_ = c.DB.QueryRow("SELECT COUNT(*), COUNT(DISTINCT thread_id) FROM messages").Scan(&msgs, &threads)
	return c.JSON(200, map[string]any{
		"emailAddress": email, "messagesTotal": msgs, "threadsTotal": threads, "historyId": "1",
	})
}

func listLabels(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT id, name, type FROM labels ORDER BY type DESC, id")
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	defer rows.Close()
	labels := []any{}
	for rows.Next() {
		var id, name, typ string
		if err := rows.Scan(&id, &name, &typ); err != nil {
			return c.GErr(500, "INTERNAL", err.Error())
		}
		labels = append(labels, map[string]any{"id": id, "name": name, "type": typ})
	}
	return c.JSON(200, map[string]any{"labels": labels})
}

func getLabel(c *mock.Ctx) error {
	var id, name, typ string
	err := c.DB.QueryRow("SELECT id, name, type FROM labels WHERE id=?", c.Params["id"]).Scan(&id, &name, &typ)
	if err == sql.ErrNoRows {
		return c.GErr(404, "NOT_FOUND", "label not found")
	}
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	msgs, err := loadMessages(c.DB)
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	total, unread := 0, 0
	for _, m := range msgs {
		if !m.hasLabel(id) {
			continue
		}
		total++
		if m.hasLabel("UNREAD") {
			unread++
		}
	}
	return c.JSON(200, map[string]any{
		"id": id, "name": name, "type": typ,
		"messagesTotal": total, "messagesUnread": unread,
	})
}

func listMessages(c *mock.Ctx) error {
	msgs, err := loadMessages(c.DB)
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	var filtered []*message
	labelFilter := c.Query["labelIds"]
	q := parseQuery(c.Query.Get("q"))
	for _, m := range msgs {
		// TRASH is excluded unless explicitly asked for, like the real API.
		if m.hasLabel("TRASH") && !contains(labelFilter, "TRASH") && !q.wantsTrash() {
			continue
		}
		if !matchesLabels(m, labelFilter) || !q.matches(m) {
			continue
		}
		filtered = append(filtered, m)
	}

	limit := clamp(atoiOr(c.Query.Get("maxResults"), 100), 1, 500)
	offset := atoiOr(c.Query.Get("pageToken"), 0)
	end := offset + limit
	if offset > len(filtered) {
		offset = len(filtered)
	}
	if end > len(filtered) {
		end = len(filtered)
	}
	page := []any{}
	for _, m := range filtered[offset:end] {
		page = append(page, map[string]any{"id": m.id, "threadId": m.threadID})
	}
	resp := map[string]any{"messages": page, "resultSizeEstimate": len(filtered)}
	if end < len(filtered) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	return c.JSON(200, resp)
}

func getMessage(c *mock.Ctx) error {
	row := c.DB.QueryRow("SELECT "+messageCols+" FROM messages WHERE id=?", c.Params["id"])
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return c.GErr(404, "NOT_FOUND", "message not found")
	}
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	return c.JSON(200, m.full())
}

// modifyMessage is the stateful label flip: {"removeLabelIds":["UNREAD"]}
// marks a message read; {"addLabelIds":["STARRED"]} stars it.
func modifyMessage(c *mock.Ctx) error {
	var body struct {
		AddLabelIDs    []string `json:"addLabelIds"`
		RemoveLabelIDs []string `json:"removeLabelIds"`
	}
	if err := c.Bind(&body); err != nil {
		return c.GErr(400, "INVALID_ARGUMENT", "invalid body")
	}
	return applyLabelChange(c, c.Params["id"], body.AddLabelIDs, body.RemoveLabelIDs)
}

func trashMessage(c *mock.Ctx) error {
	return applyLabelChange(c, c.Params["id"], []string{"TRASH"}, []string{"INBOX", "UNREAD"})
}

func applyLabelChange(c *mock.Ctx, id string, add, remove []string) error {
	row := c.DB.QueryRow("SELECT "+messageCols+" FROM messages WHERE id=?", id)
	m, err := scanMessage(row)
	if err == sql.ErrNoRows {
		return c.GErr(404, "NOT_FOUND", "message not found")
	}
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	set := map[string]bool{}
	for _, l := range m.labelIDs {
		set[l] = true
	}
	for _, l := range add {
		set[l] = true
	}
	for _, l := range remove {
		delete(set, l)
	}
	next := make([]string, 0, len(set))
	for l := range set {
		next = append(next, l)
	}
	sort.Strings(next)
	if _, err := c.DB.Exec("UPDATE messages SET label_ids=? WHERE id=?", strings.Join(next, " "), id); err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	m.labelIDs = next
	return c.JSON(200, map[string]any{"id": m.id, "threadId": m.threadID, "labelIds": m.labelIDs})
}

// sendMessage accepts the real API's shape: {"raw": "<base64url RFC 822>",
// "threadId": "..."}. The parsed message lands in SENT (from = the
// profile address), appended to the given thread — so a reply shows up
// on the next threads.get.
func sendMessage(c *mock.Ctx) error {
	var body struct {
		Raw      string `json:"raw"`
		ThreadID string `json:"threadId"`
	}
	if err := c.Bind(&body); err != nil || body.Raw == "" {
		return c.GErr(400, "INVALID_ARGUMENT", "raw (base64url RFC 822) is required")
	}
	raw, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(body.Raw, "="))
	if err != nil {
		return c.GErr(400, "INVALID_ARGUMENT", "raw is not valid base64url")
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return c.GErr(400, "INVALID_ARGUMENT", "raw is not a valid RFC 822 message")
	}
	bodyText, err := io.ReadAll(parsed.Body)
	if err != nil {
		return c.GErr(400, "INVALID_ARGUMENT", "unreadable message body")
	}

	var from string
	_ = c.DB.QueryRow("SELECT value FROM meta WHERE id='emailAddress'").Scan(&from)

	var n int
	if err := c.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	id := fmt.Sprintf("msg-sent-%04d", n+1)
	threadID := body.ThreadID
	if threadID == "" {
		threadID = id
	}
	now := time.Now().UnixMilli()
	if _, err := c.DB.Exec(`INSERT INTO messages
		(id, thread_id, from_addr, to_addr, subject, body, internal_date, label_ids)
		VALUES(?,?,?,?,?,?,?,?)`,
		id, threadID, from, parsed.Header.Get("To"), parsed.Header.Get("Subject"),
		string(bodyText), now, "SENT"); err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	return c.JSON(200, map[string]any{"id": id, "threadId": threadID, "labelIds": []string{"SENT"}})
}

func listThreads(c *mock.Ctx) error {
	msgs, err := loadMessages(c.DB)
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	seen := map[string]bool{}
	threads := []any{}
	for _, m := range msgs {
		if seen[m.threadID] || m.hasLabel("TRASH") {
			continue
		}
		seen[m.threadID] = true
		threads = append(threads, map[string]any{"id": m.threadID, "snippet": m.snippet(), "historyId": "1"})
	}
	return c.JSON(200, map[string]any{"threads": threads, "resultSizeEstimate": len(threads)})
}

func getThread(c *mock.Ctx) error {
	rows, err := c.DB.Query("SELECT "+messageCols+" FROM messages WHERE thread_id=? ORDER BY internal_date, id", c.Params["id"])
	if err != nil {
		return c.GErr(500, "INTERNAL", err.Error())
	}
	defer rows.Close()
	msgs := []any{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return c.GErr(500, "INTERNAL", err.Error())
		}
		msgs = append(msgs, m.full())
	}
	if len(msgs) == 0 {
		return c.GErr(404, "NOT_FOUND", "thread not found")
	}
	return c.JSON(200, map[string]any{"id": c.Params["id"], "messages": msgs})
}

// ---------- q parsing ----------

// query is the parsed form of Gmail's q parameter: a list of
// predicates ANDed together.
type query struct {
	unread, starred bool
	labels          []string
	from, to        []string
	subject         []string
	words           []string
}

func parseQuery(q string) *query {
	out := &query{}
	for _, tok := range strings.Fields(q) {
		lower := strings.ToLower(tok)
		switch {
		case lower == "is:unread":
			out.unread = true
		case lower == "is:starred":
			out.starred = true
		case strings.HasPrefix(lower, "in:"), strings.HasPrefix(lower, "label:"):
			out.labels = append(out.labels, strings.SplitN(tok, ":", 2)[1])
		case strings.HasPrefix(lower, "from:"):
			out.from = append(out.from, strings.ToLower(tok[len("from:"):]))
		case strings.HasPrefix(lower, "to:"):
			out.to = append(out.to, strings.ToLower(tok[len("to:"):]))
		case strings.HasPrefix(lower, "subject:"):
			out.subject = append(out.subject, strings.ToLower(tok[len("subject:"):]))
		case strings.Contains(lower, ":"):
			// Unknown operator — ignore rather than mis-match as a bare word.
		default:
			out.words = append(out.words, lower)
		}
	}
	return out
}

func (q *query) wantsTrash() bool {
	for _, l := range q.labels {
		if strings.EqualFold(l, "trash") {
			return true
		}
	}
	return false
}

func (q *query) matches(m *message) bool {
	if q.unread && !m.hasLabel("UNREAD") {
		return false
	}
	if q.starred && !m.hasLabel("STARRED") {
		return false
	}
	for _, l := range q.labels {
		if !hasLabelFold(m, l) {
			return false
		}
	}
	for _, f := range q.from {
		if !strings.Contains(strings.ToLower(m.from), f) {
			return false
		}
	}
	for _, t := range q.to {
		if !strings.Contains(strings.ToLower(m.to), t) {
			return false
		}
	}
	for _, s := range q.subject {
		if !strings.Contains(strings.ToLower(m.subject), s) {
			return false
		}
	}
	for _, w := range q.words {
		hay := strings.ToLower(m.subject + " " + m.body)
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// hasLabelFold matches a label by id or name, case-insensitively —
// users write `in:inbox`, the label id is INBOX.
func hasLabelFold(m *message, label string) bool {
	for _, l := range m.labelIDs {
		if strings.EqualFold(l, label) || strings.EqualFold(strings.TrimPrefix(l, "Label_"), label) {
			return true
		}
	}
	return false
}

func matchesLabels(m *message, labelIDs []string) bool {
	for _, want := range labelIDs {
		if !m.hasLabel(want) {
			return false
		}
	}
	return true
}

// ---------- small helpers ----------

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

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
