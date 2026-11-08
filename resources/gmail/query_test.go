package gmail

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSearchQuerySemantics(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	boss := mailMessage{From: "Boss <boss@acme.com>", To: "me@acme.com", Subject: "Q3 numbers", Body: "Please review the forecast.", InternalDate: now.Add(-time.Hour).UnixMilli(), LabelIDs: []string{"INBOX", "UNREAD"}}
	vendor := mailMessage{From: "Billing <billing@vendor.example>", Subject: "Invoice ready", Body: "Your invoice is attached.", InternalDate: now.AddDate(0, 0, -10).UnixMilli(), LabelIDs: []string{"INBOX", "Label_billing"}}
	archived := mailMessage{From: "boss@acme.com", Subject: "Old thread", InternalDate: now.AddDate(-1, 0, 0).UnixMilli(), LabelIDs: []string{"STARRED"}}
	window := now.Add(-2 * time.Hour).Unix()
	for _, tc := range []struct {
		query string
		want  []bool // boss, vendor, archived
	}{
		{"", []bool{true, true, true}},
		{"in:inbox", []bool{true, true, false}},
		// The shape a poller sends: a grouped search plus a time window.
		{fmt.Sprintf("(in:inbox from:boss@acme.com) after:%d", window), []bool{true, false, false}},
		{fmt.Sprintf("(in:inbox OR is:starred) after:%d", window), []bool{true, false, false}},
		{"from:boss@acme.com OR from:@vendor.example", []bool{true, true, true}},
		{"{from:@vendor.example is:starred}", []bool{false, true, true}},
		{"in:inbox -from:boss@acme.com", []bool{false, true, false}},
		{"-(in:inbox is:unread)", []bool{false, true, true}},
		{`"review the forecast"`, []bool{true, false, false}},
		{`subject:"q3 numbers"`, []bool{true, false, false}},
		{`"re: q3"`, []bool{false, false, false}},
		{"before:2026/08/01", []bool{false, false, true}},
		{"after:2026-08-01 before:2026/08/24", []bool{false, true, false}},
		{"newer_than:2d", []bool{true, false, false}},
		{"older_than:1m", []bool{false, false, true}},
		{"label:billing", []bool{false, true, false}},
		{"is:read", []bool{false, true, true}},
		{"in:inbox has:attachment", []bool{true, true, false}}, // unmodelled operators match
		{"in:inbox )", []bool{true, true, false}},              // a stray closer is ignored
		{"(in:inbox from:boss", []bool{true, false, false}},    // an open group closes at the end
	} {
		query := parseQuery(tc.query)
		for i, message := range []mailMessage{boss, vendor, archived} {
			if got := query.matches(message, now); got != tc.want[i] {
				t.Errorf("%q on message %d = %v, want %v", tc.query, i, got, tc.want[i])
			}
		}
	}
}

func TestSearchQueryIncludesTrashOnlyWhenNamed(t *testing.T) {
	for query, want := range map[string]bool{"in:trash": true, "(in:spam OR in:inbox)": true, "in:anywhere x": true, "in:inbox": false, "-in:trash": false, "trash": false} {
		if got := parseQuery(query).wantsTrash(); got != want {
			t.Errorf("%q wantsTrash = %v, want %v", query, got, want)
		}
	}
}

// An inserted message arrives without being sent: it keeps its own sender,
// takes the requested labels and becomes visible to a windowed search.
func TestInsertDeliversMailThatAWindowedSearchFinds(t *testing.T) {
	db := openTestDB(t, loadScenario(t, "acme-corp.v1.json"))
	handler := newTestHandler(t, db, &testIDs{})
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) // newTestHandler's clock

	search := func(q string) []string {
		t.Helper()
		response := request(t, handler, http.MethodGet, "/gmail/v1/users/me/messages?q="+url.QueryEscape(q), "", testToken)
		if response.Code != http.StatusOK {
			t.Fatalf("list %q = %d %s", q, response.Code, response.Body.String())
		}
		var got struct {
			Messages []struct{ ID string } `json:"messages"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, m := range got.Messages {
			ids = append(ids, m.ID)
		}
		return ids
	}
	query := fmt.Sprintf("(in:inbox from:quinn@contoso.example) after:%d", now.Add(-5*time.Minute).Unix())
	if ids := search(query); len(ids) != 0 {
		t.Fatalf("no seeded mail is from this sender, got %v", ids)
	}

	raw := base64.RawURLEncoding.EncodeToString([]byte("From: Quinn Park <quinn@contoso.example>\r\nTo: support@acme.example\r\nSubject: Contract renewal\r\n\r\nCan we talk renewal terms?"))
	inserted := request(t, handler, http.MethodPost, "/gmail/v1/users/me/messages", `{"raw":"`+raw+`","labelIds":["INBOX","UNREAD"]}`, testToken)
	if inserted.Code != http.StatusOK || !strings.Contains(inserted.Body.String(), `"id":"msg-sent-0001"`) || !strings.Contains(inserted.Body.String(), "quinn@contoso.example") || strings.Contains(inserted.Body.String(), `"SENT"`) {
		t.Fatalf("insert = %d %s", inserted.Code, inserted.Body.String())
	}
	if ids := search(query); len(ids) != 1 || ids[0] != "msg-sent-0001" {
		t.Fatalf("windowed search after insert = %v", ids)
	}
	if ids := search(strings.Replace(query, "quinn@", "someone-else@", 1)); len(ids) != 0 {
		t.Fatalf("a different sender matched: %v", ids)
	}

	// dateHeader backdates the message; a malformed Date is rejected.
	old := base64.RawURLEncoding.EncodeToString([]byte("From: a@b.example\r\nDate: Mon, 05 Jan 2026 09:00:00 +0000\r\nSubject: Backdated\r\n\r\nx"))
	backdated := request(t, handler, http.MethodPost, "/gmail/v1/users/me/messages?internalDateSource=dateHeader", `{"raw":"`+old+`","labelIds":["INBOX"]}`, testToken)
	if backdated.Code != http.StatusOK || !strings.Contains(backdated.Body.String(), `"internalDate":"1767603600000"`) {
		t.Fatalf("dateHeader insert = %d %s", backdated.Code, backdated.Body.String())
	}
	noDate := base64.RawURLEncoding.EncodeToString([]byte("From: a@b.example\r\nSubject: x\r\n\r\nx"))
	for name, body := range map[string]string{
		"missing raw":       `{"labelIds":["INBOX"]}`,
		"bad base64":        `{"raw":"!!"}`,
		"unknown field":     `{"raw":"` + raw + `","sizeEstimate":3}`,
		"dateHeader absent": `{"raw":"` + noDate + `"}`,
	} {
		path := "/gmail/v1/users/me/messages"
		if name == "dateHeader absent" {
			path += "?internalDateSource=dateHeader"
		}
		if response := request(t, handler, http.MethodPost, path, body, testToken); response.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
}
