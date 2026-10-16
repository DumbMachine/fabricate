package console

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const scenarioDescriptionLimit = 160

func scenarioName(id, resource string) string {
	slug := id
	prefix := resource + "."
	if strings.HasPrefix(slug, prefix) {
		slug = strings.TrimPrefix(slug, prefix)
	}
	if index := strings.LastIndex(slug, ".v"); index > 0 && digits(slug[index+2:]) {
		slug = slug[:index]
	}
	if slug == "" {
		return id
	}
	return humanize(slug)
}

func scenarioDescription(raw json.RawMessage) (string, error) {
	scalars, collections, err := summarizeState(raw)
	if err != nil {
		return "", err
	}
	counts := make([]countedCollection, 0, len(collections))
	for _, collection := range collections {
		if collection.Total == 0 {
			continue
		}
		counts = append(counts, countedCollection{key: collection.Key, total: collection.Total})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].total == counts[j].total {
			return counts[i].key < counts[j].key
		}
		return counts[i].total > counts[j].total
	})
	if len(counts) > 3 {
		counts = counts[:3]
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count.total, words(count.key)))
	}
	phrase := strings.Join(parts, ", ")
	identity := identityValue(scalars)
	switch {
	case phrase == "" && identity == "":
		phrase = "Empty seed"
	case phrase == "" && identity != "":
		phrase = "Empty seed · " + identity
	case identity != "":
		phrase = phrase + " · " + identity
	}
	return truncate(phrase, scenarioDescriptionLimit), nil
}

type countedCollection struct {
	key   string
	total int
}

func identityValue(scalars []ScenarioScalar) string {
	byKey := map[string]string{}
	for _, scalar := range scalars {
		byKey[scalar.Key] = strings.TrimSpace(scalar.Value)
	}
	for _, key := range []string{
		"emailAddress", "email", "name", "companyName", "company", "organization",
		"workspace", "domain", "accountName", "clusterIdentifier", "database",
	} {
		if value := usableIdentity(byKey[key]); value != "" {
			return truncate(value, 72)
		}
	}
	for _, scalar := range scalars {
		if strings.Contains(scalar.Value, "@") {
			if value := usableIdentity(scalar.Value); value != "" {
				return truncate(value, 72)
			}
		}
	}
	return ""
}

func usableIdentity(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "true", "false":
		return ""
	default:
		return value
	}
}

func words(key string) string {
	var builder strings.Builder
	for index, r := range key {
		switch {
		case r == '_' || r == '-':
			builder.WriteByte(' ')
		case index > 0 && unicode.IsUpper(r):
			builder.WriteByte(' ')
			builder.WriteRune(unicode.ToLower(r))
		default:
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
