package console

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dumbmachine/fabricate/requestlog"
)

func TestReadSessionLogKeepsTheLatestEntries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FAB_LOG_DIR", root)
	path := filepath.Join(root, "gmail", "run", "requests.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := ""
	for i := 0; i < 5; i++ {
		body += `{"method":"GET","path":"/one","status":200}` + "\n"
	}
	body += "not json\n"
	body += `{"method":"POST","path":"/two","status":201}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, truncated, err := readSessionLog(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(entries) != 2 || entries[0].Path != "/one" || entries[1].Path != "/two" || entries[1].Status != 201 {
		t.Fatalf("entries %#v truncated %v", entries, truncated)
	}

	missing, truncated, err := readSessionLog(filepath.Join(root, "gmail", "absent", "requests.jsonl"), 2)
	if err != nil || truncated || len(missing) != 0 {
		t.Fatalf("missing %#v truncated %v err %v", missing, truncated, err)
	}
	if _, _, err := readSessionLog(filepath.Join(root, "gmail", "run", "other.txt"), 2); err == nil {
		t.Fatal("accepted a file that is not requests.jsonl")
	}
	outside := filepath.Join(root, "..", "secret", "requests.jsonl")
	if _, _, err := readSessionLog(outside, 2); err == nil {
		t.Fatal("accepted a log outside the log directory")
	}
	resolved, err := requestlog.Root()
	if err != nil || resolved != root {
		t.Fatalf("root %s err %v", resolved, err)
	}
}
