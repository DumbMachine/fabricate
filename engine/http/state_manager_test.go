package httpengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dumbmachine/fabricate/resources/gmail"
	"github.com/dumbmachine/fabricate/scenario"
)

func TestPrepareAndResetRestoreBaseline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "resources", "gmail", "scenarios", "acme-corp.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	resource := gmail.NewResource()
	state, err := (StateManager{}).Prepare(context.Background(), filepath.Join(t.TempDir(), "support-mail"), resource.Scenarios(), doc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })

	if _, err := state.DB().Exec("DELETE FROM messages WHERE id='msg-0001'"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := state.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 27 {
		t.Fatalf("mutated count = %d, want 27", count)
	}
	db, err := state.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 28 {
		t.Fatalf("reset count = %d, want 28", count)
	}
	dumped, err := resource.Scenarios().Dump(context.Background(), db, doc.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var stateDoc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(dumped.State, &stateDoc); err != nil {
		t.Fatal(err)
	}
	if len(stateDoc.Messages) != 28 {
		t.Fatalf("dumped reset state has %d messages", len(stateDoc.Messages))
	}
}

func TestPrepareFailureCleansServiceDirectory(t *testing.T) {
	doc := scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: "gmail.bad.v1",
		Resource: "gmail", ResourceVersion: "v1",
		State: json.RawMessage(`{"emailAddress":"support@acme.example","labels":[],"messages":[{"id":"x"}]}`),
	}
	dir := filepath.Join(t.TempDir(), "bad-service")
	_, err := (StateManager{}).Prepare(context.Background(), dir, gmail.NewResource().Scenarios(), doc)
	if err == nil {
		t.Fatal("expected invalid scenario failure")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("failed prepare left service directory behind: %v", statErr)
	}
}
