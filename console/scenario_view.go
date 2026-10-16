package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dumbmachine/fabricate/scenario"
)

const (
	scenarioRowLimit    = 25
	scenarioColumnLimit = 10
	scenarioCellLimit   = 180
)

var preferredColumns = []string{
	"gid", "id", "name", "title", "subject", "email", "from", "status", "type",
}

type ScenarioView struct {
	ID              string               `json:"id"`
	Resource        string               `json:"resource"`
	DisplayName     string               `json:"displayName"`
	Name            string               `json:"name"`
	Description     string               `json:"description"`
	ResourceVersion string               `json:"resourceVersion"`
	ProviderHosts   []string             `json:"providerHosts"`
	Scalars         []ScenarioScalar     `json:"scalars"`
	Collections     []ScenarioCollection `json:"collections"`
}

type ScenarioScalar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ScenarioCollection struct {
	Key     string              `json:"key"`
	Total   int                 `json:"total"`
	Shown   int                 `json:"shown"`
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

func (m *Manager) Scenario(id string) (ScenarioView, error) {
	id = strings.TrimSpace(id)
	resourceID, _, ok := strings.Cut(id, ".")
	if !ok || resourceID == "" {
		return ScenarioView{}, errUnknownScenario(id)
	}
	resource, ok := m.registry.Get(resourceID)
	if !ok {
		return ScenarioView{}, errUnknownScenario(id)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		return ScenarioView{}, err
	}
	doc, ok := scenario.Lookup(docs, id)
	if !ok {
		return ScenarioView{}, errUnknownScenario(id)
	}
	scalars, collections, err := summarizeState(doc.State)
	if err != nil {
		return ScenarioView{}, err
	}
	description, err := scenarioDescription(doc.State)
	if err != nil {
		return ScenarioView{}, err
	}
	return ScenarioView{
		ID:              doc.ID,
		Resource:        doc.Resource,
		DisplayName:     resourceDisplayName(m.registry, doc.Resource),
		Name:            scenarioName(doc.ID, doc.Resource),
		Description:     description,
		ResourceVersion: doc.ResourceVersion,
		ProviderHosts:   providerHosts(m.registry, doc.Resource),
		Scalars:         scalars,
		Collections:     collections,
	}, nil
}

func summarizeState(raw json.RawMessage) ([]ScenarioScalar, []ScenarioCollection, error) {
	keys, values, err := decodeObject(raw)
	if err != nil {
		return nil, nil, err
	}
	scalars := make([]ScenarioScalar, 0)
	collections := make([]ScenarioCollection, 0)
	for _, key := range keys {
		switch typed := values[key].(type) {
		case []any:
			collections = append(collections, summarizeCollection(key, typed))
		default:
			formatted := formatCell(typed)
			if formatted != "" {
				scalars = append(scalars, ScenarioScalar{Key: key, Value: formatted})
			}
		}
	}
	return scalars, collections, nil
}

func decodeObject(raw json.RawMessage) ([]string, map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, nil, err
	}
	if token != json.Delim('{') {
		return nil, nil, fmt.Errorf("scenario: state must be an object")
	}
	keys := make([]string, 0)
	values := map[string]any{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, nil, fmt.Errorf("scenario: state key must be a string")
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, err
		}
		if _, exists := values[key]; exists {
			continue
		}
		keys = append(keys, key)
		values[key] = value
	}
	return keys, values, nil
}

func summarizeCollection(key string, items []any) ScenarioCollection {
	total := len(items)
	shownItems := items
	if len(shownItems) > scenarioRowLimit {
		shownItems = shownItems[:scenarioRowLimit]
	}
	rows := make([]map[string]string, 0, len(shownItems))
	seen := map[string]struct{}{}
	for _, item := range shownItems {
		row := map[string]string{}
		switch typed := item.(type) {
		case map[string]any:
			for field, value := range typed {
				row[field] = formatCell(value)
				seen[field] = struct{}{}
			}
		default:
			row["value"] = formatCell(typed)
			seen["value"] = struct{}{}
		}
		rows = append(rows, row)
	}
	columns := orderColumns(seen)
	selected := map[string]struct{}{}
	for _, column := range columns {
		selected[column] = struct{}{}
	}
	projected := make([]map[string]string, len(rows))
	for i, row := range rows {
		next := map[string]string{}
		for field, value := range row {
			if _, ok := selected[field]; ok {
				next[field] = value
			}
		}
		projected[i] = next
	}
	return ScenarioCollection{
		Key:     key,
		Total:   total,
		Shown:   len(projected),
		Columns: columns,
		Rows:    projected,
	}
}

func orderColumns(seen map[string]struct{}) []string {
	ordered := make([]string, 0, len(seen))
	for _, column := range preferredColumns {
		if _, ok := seen[column]; !ok {
			continue
		}
		ordered = append(ordered, column)
		delete(seen, column)
	}
	rest := make([]string, 0, len(seen))
	for column := range seen {
		rest = append(rest, column)
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	if len(ordered) > scenarioColumnLimit {
		ordered = ordered[:scenarioColumnLimit]
	}
	if ordered == nil {
		return []string{}
	}
	return ordered
}

func formatCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return truncate(collapseSpace(typed), scenarioCellLimit)
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return truncate(typed.String(), scenarioCellLimit)
	case []any:
		if !allPrimitive(typed) {
			return truncate(mustJSON(typed), scenarioCellLimit)
		}
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, primitiveText(item))
		}
		return truncate(strings.Join(parts, ", "), scenarioCellLimit)
	default:
		return truncate(mustJSON(typed), scenarioCellLimit)
	}
}

func allPrimitive(items []any) bool {
	for _, item := range items {
		switch item.(type) {
		case nil, string, bool, json.Number:
		default:
			return false
		}
	}
	return true
}

func primitiveText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return collapseSpace(typed)
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func collapseSpace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	if max <= 1 {
		return "…"
	}
	runes := []rune(value)
	return strings.TrimRight(string(runes[:max-1]), " ") + "…"
}
