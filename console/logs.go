package console

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dumbmachine/fabricate/requestlog"
)

const logEntryLimit = 200

// RequestLogView is the redacted request trace for one running sandbox.
type RequestLogView struct {
	ID        string             `json:"id"`
	Entries   []requestlog.Entry `json:"entries"`
	Truncated bool               `json:"truncated"`
}

func readSessionLog(path string, limit int) ([]requestlog.Entry, bool, error) {
	if limit < 1 {
		limit = logEntryLimit
	}
	if path == "" {
		return []requestlog.Entry{}, false, nil
	}
	root, err := requestlog.Root()
	if err != nil {
		return nil, false, err
	}
	cleaned, err := logPath(root, path)
	if err != nil {
		return nil, false, err
	}
	file, err := os.Open(cleaned)
	if os.IsNotExist(err) {
		return []requestlog.Entry{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("request log: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	entries := make([]requestlog.Entry, 0)
	truncated := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry requestlog.Entry
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		entries = append(entries, entry)
		if len(entries) > limit {
			next := make([]requestlog.Entry, limit)
			copy(next, entries[1:])
			entries = next
			truncated = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("request log: %w", err)
	}
	return entries, truncated, nil
}

func logPath(root, path string) (string, error) {
	root = filepath.Clean(root)
	cleaned := filepath.Clean(path)
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("request log is unavailable")
	}
	if filepath.Base(cleaned) != "requests.jsonl" {
		return "", fmt.Errorf("request log is unavailable")
	}
	return cleaned, nil
}
