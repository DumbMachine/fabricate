package googledrive

import (
	"fmt"
	"strconv"
	"strings"
)

type orderKey struct {
	field string
	desc  bool
}

func matchQuery(file storedFile, raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true, nil
	}
	clauses, err := splitAnd(raw)
	if err != nil {
		return false, err
	}
	for _, clause := range clauses {
		ok, err := matchClause(file, strings.TrimSpace(clause))
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func splitAnd(raw string) ([]string, error) {
	var clauses []string
	start := 0
	inQuote := false
	for i := 0; i < len(raw); i++ {
		if inQuote && raw[i] == '\\' {
			i++
			continue
		}
		if raw[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && i+5 <= len(raw) && strings.EqualFold(raw[i:i+5], " and ") {
			clauses = append(clauses, raw[start:i])
			start = i + 5
			i += 4
		}
	}
	if inQuote {
		return nil, fmt.Errorf("invalid q")
	}
	clauses = append(clauses, raw[start:])
	if len(clauses) == 0 {
		return nil, fmt.Errorf("invalid q")
	}
	return clauses, nil
}

func matchClause(file storedFile, clause string) (bool, error) {
	if clause == "" {
		return false, fmt.Errorf("invalid q")
	}
	if strings.HasSuffix(strings.ToLower(clause), " in parents") {
		quoted := strings.TrimSpace(clause[:len(clause)-len(" in parents")])
		value, err := parseQuoted(quoted)
		if err != nil {
			return false, err
		}
		return containsString(file.Parents, value), nil
	}
	if idx := indexFoldOutsideQuotes(clause, " contains "); idx >= 0 {
		field := strings.TrimSpace(clause[:idx])
		value, err := parseQuoted(strings.TrimSpace(clause[idx+len(" contains "):]))
		if err != nil {
			return false, err
		}
		needle := strings.ToLower(value)
		switch field {
		case "name":
			return strings.Contains(strings.ToLower(file.Name), needle), nil
		case "mimeType":
			return strings.Contains(strings.ToLower(file.MimeType), needle), nil
		case "description":
			return strings.Contains(strings.ToLower(file.Description), needle), nil
		case "fullText":
			haystack := strings.ToLower(file.Name + "\n" + file.Description + "\n" + file.Body)
			return strings.Contains(haystack, needle), nil
		default:
			return false, fmt.Errorf("invalid q")
		}
	}
	op := ""
	opAt := -1
	if idx := indexOutsideQuotes(clause, "!="); idx >= 0 {
		op, opAt = "!=", idx
	} else if idx := indexOutsideQuotes(clause, "="); idx >= 0 {
		op, opAt = "=", idx
	} else {
		return false, fmt.Errorf("invalid q")
	}
	field := strings.TrimSpace(clause[:opAt])
	rest := strings.TrimSpace(clause[opAt+len(op):])
	switch field {
	case "trashed", "starred":
		if op != "=" {
			return false, fmt.Errorf("invalid q")
		}
		value, err := parseBoolLiteral(rest)
		if err != nil {
			return false, err
		}
		if field == "trashed" {
			return file.Trashed == value, nil
		}
		return file.Starred == value, nil
	case "name", "mimeType", "description":
		value, err := parseQuoted(rest)
		if err != nil {
			return false, err
		}
		actual := file.Name
		if field == "mimeType" {
			actual = file.MimeType
		} else if field == "description" {
			actual = file.Description
		}
		if op == "!=" {
			return actual != value, nil
		}
		return actual == value, nil
	default:
		return false, fmt.Errorf("invalid q")
	}
}

func parseQuoted(raw string) (string, error) {
	if len(raw) < 2 || raw[0] != '\'' || raw[len(raw)-1] != '\'' {
		return "", fmt.Errorf("invalid q")
	}
	var b strings.Builder
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] == '\\' && i+1 < len(raw)-1 {
			b.WriteByte(raw[i+1])
			i++
			continue
		}
		if raw[i] == '\'' {
			return "", fmt.Errorf("invalid q")
		}
		b.WriteByte(raw[i])
	}
	return b.String(), nil
}

func parseBoolLiteral(raw string) (bool, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid q")
	}
}

func indexOutsideQuotes(raw, sub string) int {
	inQuote := false
	for i := 0; i+len(sub) <= len(raw); i++ {
		if inQuote && raw[i] == '\\' {
			i++
			continue
		}
		if raw[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && raw[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func indexFoldOutsideQuotes(raw, sub string) int {
	inQuote := false
	for i := 0; i+len(sub) <= len(raw); i++ {
		if inQuote && raw[i] == '\\' {
			i++
			continue
		}
		if raw[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && strings.EqualFold(raw[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func parseOrderBy(raw string) ([]orderKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []orderKey{{field: "name"}}, nil
	}
	parts := strings.Split(raw, ",")
	keys := make([]orderKey, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid orderBy")
		}
		field := part
		desc := false
		lower := strings.ToLower(part)
		switch {
		case strings.HasSuffix(lower, " desc"):
			desc = true
			field = strings.TrimSpace(part[:len(part)-len(" desc")])
		case strings.HasSuffix(lower, " asc"):
			field = strings.TrimSpace(part[:len(part)-len(" asc")])
		}
		switch field {
		case "name", "createdTime", "modifiedTime", "modifiedByMeTime", "folder", "quotaBytesUsed", "starred":
		default:
			return nil, fmt.Errorf("invalid orderBy")
		}
		keys = append(keys, orderKey{field: field, desc: desc})
	}
	return keys, nil
}

func compareFile(left, right storedFile, field string) int {
	switch field {
	case "name":
		return strings.Compare(left.Name, right.Name)
	case "createdTime":
		return strings.Compare(left.CreatedTime, right.CreatedTime)
	case "modifiedTime", "modifiedByMeTime":
		return strings.Compare(left.ModifiedTime, right.ModifiedTime)
	case "folder":
		return strings.Compare(firstParent(left), firstParent(right))
	case "quotaBytesUsed":
		return cmpInt(len(left.Body), len(right.Body))
	case "starred":
		return cmpBool(left.Starred, right.Starred)
	default:
		return 0
	}
}

func lessFile(left, right storedFile, keys []orderKey) bool {
	for _, key := range keys {
		cmp := compareFile(left, right, key.field)
		if cmp == 0 {
			continue
		}
		if key.desc {
			return cmp > 0
		}
		return cmp < 0
	}
	return left.ID < right.ID
}

func pageWindow(token *string, pageSize *int, n int) (start, end int, err error) {
	offset := 0
	if token != nil && strings.TrimSpace(*token) != "" {
		offset, err = strconv.Atoi(strings.TrimSpace(*token))
		if err != nil || offset < 0 || offset > n {
			return 0, 0, fmt.Errorf("invalid pageToken")
		}
	}
	limit := n - offset
	if pageSize != nil {
		limit = *pageSize
	}
	end = offset + limit
	if end > n {
		end = n
	}
	return offset, end, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func firstParent(file storedFile) string {
	if len(file.Parents) == 0 {
		return ""
	}
	return file.Parents[0]
}

func cmpInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func cmpBool(left, right bool) int {
	if left == right {
		return 0
	}
	if !left && right {
		return -1
	}
	return 1
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func spacesIncludeDrive(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, space := range strings.Split(raw, ",") {
		if strings.TrimSpace(space) == "drive" {
			return true
		}
	}
	return false
}
