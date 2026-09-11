package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CheckFile is an ITSMBench-style verifier: named objects in a dump must match
// field assertions. Use this when full-digest compare is too strict (generated
// IDs, updatedAt clocks) or too loose (any mutation of the right record).
type CheckFile struct {
	Objects []ObjectCheck `json:"objects"`
}

// ObjectCheck finds one record in a service dump by collection + id.
type ObjectCheck struct {
	Service       string              `json:"service"`
	Collection    string              `json:"collection"`
	ID            string              `json:"id"`
	Equals        map[string]any      `json:"equals,omitempty"`
	Contains      map[string]string   `json:"contains,omitempty"`
	ArrayContains map[string][]string `json:"arrayContains,omitempty"`
	ArrayForbids  map[string][]string `json:"arrayForbids,omitempty"`
}

type CheckResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

func LoadChecks(path string) (CheckFile, error) {
	if strings.TrimSpace(path) == "" {
		return CheckFile{}, fmt.Errorf("environment: checks path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return CheckFile{}, fmt.Errorf("environment: read checks: %w", err)
	}
	var checks CheckFile
	if err := json.Unmarshal(raw, &checks); err != nil {
		return CheckFile{}, fmt.Errorf("environment: parse checks: %w", err)
	}
	if len(checks.Objects) == 0 {
		return CheckFile{}, fmt.Errorf("environment: checks file has no objects")
	}
	for i, object := range checks.Objects {
		if object.Service == "" || object.Collection == "" || object.ID == "" {
			return CheckFile{}, fmt.Errorf("environment: checks.objects[%d] requires service, collection, and id", i)
		}
	}
	return checks, nil
}

func EvaluateChecks(snap Snapshot, checks CheckFile) CheckResult {
	result := CheckResult{Passed: true}
	for _, object := range checks.Objects {
		failures := evaluateObjectCheck(snap, object)
		result.Failures = append(result.Failures, failures...)
	}
	if len(result.Failures) > 0 {
		result.Passed = false
	}
	return result
}

func evaluateObjectCheck(snap Snapshot, check ObjectCheck) []string {
	prefix := fmt.Sprintf("%s %s %s", check.Service, check.Collection, check.ID)
	service, ok := snap.Services[check.Service]
	if !ok {
		return []string{prefix + ": service missing from dump"}
	}
	record, err := findDumpObject(service.Document.State, check.Collection, check.ID)
	if err != nil {
		return []string{prefix + ": " + err.Error()}
	}
	var failures []string
	for path, want := range check.Equals {
		got, err := lookupPath(record, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", prefix, path, err))
			continue
		}
		if !valuesEqual(want, got) {
			failures = append(failures, fmt.Sprintf("%s %s: want %s, got %s", prefix, path, formatCheckValue(want), formatCheckValue(got)))
		}
	}
	for path, needle := range check.Contains {
		got, err := lookupPath(record, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", prefix, path, err))
			continue
		}
		text, ok := stringify(got)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s %s: not a string (%s)", prefix, path, formatCheckValue(got)))
			continue
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(needle)) {
			failures = append(failures, fmt.Sprintf("%s %s: %q does not contain %q", prefix, path, text, needle))
		}
	}
	for path, needles := range check.ArrayContains {
		got, err := lookupPath(record, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", prefix, path, err))
			continue
		}
		values, ok := stringList(got)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s %s: not a string array", prefix, path))
			continue
		}
		set := stringSet(values)
		for _, needle := range needles {
			if _, exists := set[needle]; !exists {
				failures = append(failures, fmt.Sprintf("%s %s: missing %q", prefix, path, needle))
			}
		}
	}
	for path, forbidden := range check.ArrayForbids {
		got, err := lookupPath(record, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", prefix, path, err))
			continue
		}
		values, ok := stringList(got)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s %s: not a string array", prefix, path))
			continue
		}
		set := stringSet(values)
		for _, label := range forbidden {
			if _, exists := set[label]; exists {
				failures = append(failures, fmt.Sprintf("%s %s: must not contain %q", prefix, path, label))
			}
		}
	}
	return failures
}

func findDumpObject(state json.RawMessage, collection, id string) (map[string]any, error) {
	var root map[string]any
	if err := json.Unmarshal(state, &root); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	raw, ok := root[collection]
	if !ok {
		return nil, fmt.Errorf("collection %q missing", collection)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("collection %q is not an array", collection)
	}
	for _, item := range list {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if dumpObjectID(record) == id {
			return record, nil
		}
	}
	return nil, fmt.Errorf("id %q not found", id)
}

func dumpObjectID(record map[string]any) string {
	for _, key := range []string{"id", "gid"} {
		raw, exists := record[key]
		if !exists || raw == nil {
			continue
		}
		if value, ok := stringify(raw); ok && value != "" {
			return value
		}
	}
	return ""
}

func lookupPath(record map[string]any, path string) (any, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty path")
	}
	current := any(record)
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", path)
		}
		next, exists := object[part]
		if !exists {
			return nil, fmt.Errorf("missing")
		}
		current = next
	}
	return current, nil
}

func valuesEqual(want, got any) bool {
	if boolEqual(want, got) {
		return true
	}
	left, okLeft := stringify(want)
	right, okRight := stringify(got)
	if okLeft && okRight {
		return left == right
	}
	return jsonEqual(want, got)
}

func boolEqual(want, got any) bool {
	left, okLeft := asBool(want)
	right, okRight := asBool(got)
	return okLeft && okRight && left == right
}

func asBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	default:
		return false, false
	}
}

func stringify(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", true
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10), true
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func stringList(value any) ([]string, bool) {
	list, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := stringify(item)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func formatCheckValue(value any) string {
	if text, ok := stringify(value); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}
