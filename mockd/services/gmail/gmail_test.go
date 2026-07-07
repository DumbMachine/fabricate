package gmail

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "emailAddress": "support@acme.dev",
  "labels": [{"id":"Label_billing","name":"billing","type":"user"}],
  "messages": [
    {"id":"msg-001","threadId":"thr-001","from":"dana@northwind.io","to":"support@acme.dev",
     "subject":"Charged twice for March","body":"Hi, invoice INV-2103 was charged twice.",
     "labelIds":["INBOX","UNREAD","Label_billing"],"internalDate":1767900000000},
    {"id":"msg-002","threadId":"thr-002","from":"news@vendor.com","to":"support@acme.dev",
     "subject":"Weekly digest","body":"Product updates you missed.",
     "labelIds":["INBOX"],"internalDate":1767910000000},
    {"id":"msg-003","threadId":"thr-003","from":"lee@oldcorp.com","to":"support@acme.dev",
     "subject":"Spam offer","body":"Buy now.",
     "labelIds":["TRASH"],"internalDate":1767920000000}
  ]
}`

func setup(t *testing.T) *mock.Service {
	t.Helper()
	svc := New()
	if err := svc.Init(":memory:", []byte(fixtureJSON)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return svc
}

func do(t *testing.T, svc *mock.Service, method, path, body string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.Bytes()
}

func TestProfileAndLabels(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/gmail/v1/users/me/profile", "")
	if code != 200 || !strings.Contains(string(body), `"support@acme.dev"`) {
		t.Fatalf("profile = %d %s", code, body)
	}

	_, body = do(t, svc, "GET", "/gmail/v1/users/me/labels", "")
	for _, want := range []string{`"INBOX"`, `"UNREAD"`, `"Label_billing"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("labels missing %s: %s", want, body)
		}
	}

	_, body = do(t, svc, "GET", "/gmail/v1/users/me/labels/INBOX", "")
	var label struct{ MessagesTotal, MessagesUnread int }
	if err := json.Unmarshal(body, &label); err != nil {
		t.Fatal(err)
	}
	if label.MessagesTotal != 2 || label.MessagesUnread != 1 {
		t.Fatalf("INBOX counts = %+v", label)
	}
}

func TestListExcludesTrashAndFiltersUnread(t *testing.T) {
	svc := setup(t)

	_, body := do(t, svc, "GET", "/gmail/v1/users/me/messages", "")
	if strings.Contains(string(body), "msg-003") {
		t.Fatalf("trash leaked into default list: %s", body)
	}

	_, body = do(t, svc, "GET", "/gmail/v1/users/me/messages?q=is:unread", "")
	if !strings.Contains(string(body), "msg-001") || strings.Contains(string(body), "msg-002") {
		t.Fatalf("is:unread = %s", body)
	}

	_, body = do(t, svc, "GET", "/gmail/v1/users/me/messages?q=from:northwind+invoice", "")
	if !strings.Contains(string(body), "msg-001") {
		t.Fatalf("from+word search = %s", body)
	}

	_, body = do(t, svc, "GET", "/gmail/v1/users/me/messages?labelIds=Label_billing", "")
	if !strings.Contains(string(body), "msg-001") || strings.Contains(string(body), "msg-002") {
		t.Fatalf("labelIds filter = %s", body)
	}
}

func TestGetMessagePayload(t *testing.T) {
	svc := setup(t)

	_, body := do(t, svc, "GET", "/gmail/v1/users/me/messages/msg-001", "")
	var msg struct {
		Payload struct {
			Body struct{ Data string }
		}
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.URLEncoding.DecodeString(msg.Payload.Body.Data)
	if err != nil {
		t.Fatalf("payload not base64url: %v", err)
	}
	if !strings.Contains(string(decoded), "INV-2103") {
		t.Fatalf("decoded body = %s", decoded)
	}

	code, _ := do(t, svc, "GET", "/gmail/v1/users/me/messages/nope", "")
	if code != 404 {
		t.Fatalf("missing message = %d", code)
	}
}

func TestModifyMarksRead(t *testing.T) {
	svc := setup(t)

	_, body := do(t, svc, "POST", "/gmail/v1/users/me/messages/msg-001/modify",
		`{"removeLabelIds":["UNREAD"],"addLabelIds":["STARRED"]}`)
	if strings.Contains(string(body), `"UNREAD"`) || !strings.Contains(string(body), `"STARRED"`) {
		t.Fatalf("modify = %s", body)
	}

	// The flip is visible on the next unread search.
	_, body = do(t, svc, "GET", "/gmail/v1/users/me/messages?q=is:unread", "")
	if strings.Contains(string(body), "msg-001") {
		t.Fatalf("msg-001 still unread after modify: %s", body)
	}
}

func TestSendAppendsToThread(t *testing.T) {
	svc := setup(t)

	raw := "To: dana@northwind.io\r\nSubject: Re: Charged twice for March\r\n\r\nRefund issued for the duplicate charge."
	payload, _ := json.Marshal(map[string]string{
		"raw":      base64.URLEncoding.EncodeToString([]byte(raw)),
		"threadId": "thr-001",
	})
	code, body := do(t, svc, "POST", "/gmail/v1/users/me/messages/send", string(payload))
	if code != 200 || !strings.Contains(string(body), `"threadId":"thr-001"`) {
		t.Fatalf("send = %d %s", code, body)
	}

	// The reply is now the second message in the thread.
	_, body = do(t, svc, "GET", "/gmail/v1/users/me/threads/thr-001", "")
	var thread struct{ Messages []json.RawMessage }
	if err := json.Unmarshal(body, &thread); err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("thread has %d messages, want 2: %s", len(thread.Messages), body)
	}
	if !strings.Contains(string(body), "Refund issued") {
		t.Fatalf("thread missing reply body: %s", body)
	}
}

func TestTrash(t *testing.T) {
	svc := setup(t)

	_, body := do(t, svc, "POST", "/gmail/v1/users/me/messages/msg-002/trash", "")
	if !strings.Contains(string(body), `"TRASH"`) {
		t.Fatalf("trash = %s", body)
	}
	_, body = do(t, svc, "GET", "/gmail/v1/users/me/messages", "")
	if strings.Contains(string(body), "msg-002") {
		t.Fatalf("trashed message still listed: %s", body)
	}
}
