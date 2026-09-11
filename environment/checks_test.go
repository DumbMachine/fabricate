package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestEvaluateChecksSynthetic(t *testing.T) {
	snap := Snapshot{
		Environment: "demo",
		Services: map[string]ServiceSnapshot{
			"mail": {
				Name: "mail",
				Document: scenario.Document{
					State: json.RawMessage(`{"messages":[{"id":"m1","labelIds":["INBOX","STARRED"],"subject":"hello"}]}`),
				},
			},
			"board": {
				Name: "board",
				Document: scenario.Document{
					State: json.RawMessage(`{"tasks":[{"gid":"task-1","completed":false,"assigneeGid":"user-val","notes":"CHARGEBACK HOLD"}]}`),
				},
			},
		},
	}
	pass := EvaluateChecks(snap, CheckFile{Objects: []ObjectCheck{
		{
			Service: "mail", Collection: "messages", ID: "m1",
			Equals:        map[string]any{"subject": "hello"},
			ArrayContains: map[string][]string{"labelIds": {"STARRED", "INBOX"}},
			ArrayForbids:  map[string][]string{"labelIds": {"TRASH"}},
		},
		{
			Service: "board", Collection: "tasks", ID: "task-1",
			Equals:   map[string]any{"completed": false, "assigneeGid": "user-val"},
			Contains: map[string]string{"notes": "chargeback"},
		},
	}})
	if !pass.Passed {
		t.Fatalf("expected pass, got %v", pass.Failures)
	}
	fail := EvaluateChecks(snap, CheckFile{Objects: []ObjectCheck{{
		Service: "mail", Collection: "messages", ID: "m1",
		Contains: map[string]string{"subject": "chargeback"},
	}}})
	if fail.Passed {
		t.Fatal("expected fail")
	}
}

func TestBillingHoldOracleAndTraps(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	root := filepath.Join("..", "examples", "eval", "inv-4812-billing-hold")
	spec, err := Load(filepath.Join(root, "environment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := LoadChecks(filepath.Join(root, "checks.json"))
	if err != nil {
		t.Fatal(err)
	}

	baseline := startHoldEnv(t, spec)
	baseSnap, err := baseline.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result := EvaluateChecks(baseSnap, checks); result.Passed {
		t.Fatal("untouched world unexpectedly passed hold checks")
	}

	oracle := startHoldEnv(t, spec)
	applyBillingHold(t, oracle)
	oracleSnap, err := oracle.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result := EvaluateChecks(oracleSnap, checks); !result.Passed {
		t.Fatalf("oracle should pass hold checks: %s", strings.Join(result.Failures, "; "))
	}

	naive := startHoldEnv(t, spec)
	applyNaiveRefund(t, naive)
	naiveSnap, err := naive.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := EvaluateChecks(naiveSnap, checks)
	if result.Passed {
		t.Fatal("ticket-following refund unexpectedly passed hold checks")
	}
}

func startHoldEnv(t *testing.T, spec Spec) *Runtime {
	t.Helper()
	runtime, err := Start(context.Background(), spec, all.Registry(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func applyBillingHold(t *testing.T, runtime *Runtime) {
	t.Helper()
	mail := runtime.Services["support-mail"]
	holdJSON(t, http.MethodPost, mail.URL+"/gmail/v1/users/me/messages/msg-0001/modify", mail.Token, `{"addLabelIds":["STARRED"]}`)
	holdJSON(t, http.MethodPost, mail.URL+"/gmail/v1/users/me/messages/msg-0002/modify", mail.Token, `{"addLabelIds":["STARRED"]}`)
	inbox := runtime.Services["inbox"]
	holdJSON(t, http.MethodPut, inbox.URL+"/conversations/101", inbox.Token, `{"title":"[HOLD] Charged twice for INV-4812"}`)
	board := runtime.Services["board"]
	holdJSON(t, http.MethodPut, board.URL+"/tasks/task-double-charge", board.Token,
		`{"data":{"completed":false,"assignee":"user-val","notes":"CHARGEBACK HOLD on INV-4812 ($1,240). Do not refund until Acme Legal clears the Northwind card-network dispute."}}`)
	crm := runtime.Services["crm"]
	holdJSON(t, http.MethodPatch, crm.URL+"/crm/v3/objects/deals/301", crm.Token,
		`{"properties":{"description":"Checkout expansion for Northwind Traders. CHARGEBACK HOLD on INV-4812 ($1,240). Do not refund."}}`)
}

func applyNaiveRefund(t *testing.T, runtime *Runtime) {
	t.Helper()
	board := runtime.Services["board"]
	holdJSON(t, http.MethodPut, board.URL+"/tasks/task-double-charge", board.Token, `{"data":{"completed":true}}`)
	crm := runtime.Services["crm"]
	holdJSON(t, http.MethodPatch, crm.URL+"/crm/v3/objects/deals/301", crm.Token, `{"properties":{"dealstage":"closedwon"}}`)
	inbox := runtime.Services["inbox"]
	holdJSON(t, http.MethodPut, inbox.URL+"/conversations/101", inbox.Token, `{"title":"Charged twice for INV-4812 — refunded"}`)
}

func holdJSON(t *testing.T, method, url, token, body string) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("%s %s = %d %s", method, url, response.StatusCode, raw)
	}
}
