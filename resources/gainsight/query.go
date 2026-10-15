package gainsight

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dumbmachine/fabricate/resources/gainsight/generated"
)

type condition struct {
	alias  string
	field  string
	op     string
	values []string
}

func filterProject(records []generated.Record, where *generated.Where, orderBy *map[string]string, offset, limit int, fields []string) ([]generated.Record, string, string) {
	filtered := make([]generated.Record, 0, len(records))
	for _, record := range records {
		ok, err := matchWhere(record, where)
		if err != nil {
			return nil, "GSOBJ_1001", err.Error()
		}
		if ok {
			filtered = append(filtered, record)
		}
	}
	var ordering map[string]string
	if orderBy != nil {
		ordering = *orderBy
	}
	sortRecords(filtered, ordering)
	if offset < 0 {
		offset = 0
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := len(filtered)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	page := filtered[offset:end]
	if page == nil {
		page = []generated.Record{}
	}
	projected := make([]generated.Record, 0, len(page))
	for _, record := range page {
		item, err := project(record, fields)
		if err != nil {
			return nil, "GSOBJ_1001", err.Error()
		}
		projected = append(projected, item)
	}
	return projected, "", ""
}

func matchWhere(record generated.Record, where *generated.Where) (bool, error) {
	if where == nil || where.Conditions == nil || len(*where.Conditions) == 0 {
		return true, nil
	}
	results := map[string]bool{}
	for i, raw := range *where.Conditions {
		field := sval(raw.Name)
		if field == "" {
			field = sval(raw.FieldName)
		}
		if field == "" {
			return false, errors.New("Invalid fields or operator in where clause")
		}
		alias := sval(raw.Alias)
		if alias == "" {
			alias = string(rune('A' + i))
		}
		var values []string
		if raw.Value != nil {
			values = *raw.Value
		}
		ok, err := matchCondition(record, condition{alias: alias, field: field, op: strings.ToUpper(raw.Operator), values: values})
		if err != nil {
			return false, err
		}
		results[alias] = ok
	}
	expression := sval(where.Expression)
	if expression == "" {
		return false, errors.New("Please pass the expression for the respective conditions")
	}
	return evalExpression(expression, results)
}

func matchCondition(record generated.Record, item condition) (bool, error) {
	value, ok := lookup(record, item.field)
	if !ok {
		return false, fmt.Errorf("Invalid fields or operator in where clause - %s", item.field)
	}
	text := ""
	if value != nil {
		text = fmt.Sprint(value)
	}
	null := value == nil || text == ""
	need := func(n int) error {
		if len(item.values) < n {
			return fmt.Errorf("Invalid fields or operator in where clause - %s", item.field)
		}
		return nil
	}
	switch item.op {
	case "EQ":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && text == item.values[0], nil
	case "NE":
		if err := need(1); err != nil {
			return false, err
		}
		return null || text != item.values[0], nil
	case "IN", "INCLUDES":
		for _, wanted := range item.values {
			if !null && text == wanted {
				return true, nil
			}
		}
		return false, nil
	case "CONTAINS":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && strings.Contains(text, item.values[0]), nil
	case "STARTS_WITH", "STARTSWITH":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && strings.HasPrefix(text, item.values[0]), nil
	case "BTW":
		if err := need(2); err != nil {
			return false, err
		}
		return !null && text >= item.values[0] && text <= item.values[1], nil
	case "GT":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && text > item.values[0], nil
	case "GTE":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && text >= item.values[0], nil
	case "LT":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && text < item.values[0], nil
	case "LTE":
		if err := need(1); err != nil {
			return false, err
		}
		return !null && text <= item.values[0], nil
	case "IS_NULL":
		return null, nil
	case "IS_NOT_NULL":
		return !null, nil
	default:
		return false, fmt.Errorf("Invalid fields or operator in where clause - %s", item.op)
	}
}

func evalExpression(expression string, results map[string]bool) (bool, error) {
	parts := strings.Fields(expression)
	if len(parts) == 0 {
		return false, errors.New("Please pass the expression for the respective conditions")
	}
	conjunction := ""
	var acc bool
	started := false
	for i, part := range parts {
		if i%2 == 1 {
			op := strings.ToUpper(part)
			if op != "AND" && op != "OR" {
				return false, errors.New("Please pass the correct conditions")
			}
			if conjunction == "" {
				conjunction = op
			} else if conjunction != op {
				return false, errors.New("Please pass the correct conditions")
			}
			continue
		}
		value, ok := results[part]
		if !ok {
			return false, errors.New("Please pass the correct conditions")
		}
		if !started {
			acc = value
			started = true
			continue
		}
		if conjunction == "AND" {
			acc = acc && value
		} else {
			acc = acc || value
		}
	}
	if !started {
		return false, errors.New("Please pass the correct conditions")
	}
	return acc, nil
}

func sortRecords(records []generated.Record, orderBy map[string]string) {
	type key struct {
		field string
		desc  bool
	}
	keys := []key{}
	if len(orderBy) == 0 {
		keys = append(keys, key{"Name", false}, key{"Gsid", false})
	} else {
		names := make([]string, 0, len(orderBy))
		for name := range orderBy {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			keys = append(keys, key{name, strings.EqualFold(orderBy[name], "desc")})
		}
		keys = append(keys, key{"Gsid", false})
	}
	sort.SliceStable(records, func(i, j int) bool {
		for _, key := range keys {
			left, _ := lookup(records[i], key.field)
			right, _ := lookup(records[j], key.field)
			lt, rt := fmt.Sprint(left), fmt.Sprint(right)
			if lt == rt {
				continue
			}
			if key.desc {
				return lt > rt
			}
			return lt < rt
		}
		return false
	})
}

func project(record generated.Record, fields []string) (generated.Record, error) {
	out := generated.Record{}
	var unknown []string
	for _, field := range fields {
		if value, ok := record[field]; ok {
			out[field] = value
			continue
		}
		found := false
		for key, value := range record {
			if strings.EqualFold(key, field) {
				out[key] = value
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("Invalid fields in select clause - %s", strings.Join(unknown, ", "))
	}
	return out, nil
}

func lookup(record generated.Record, field string) (any, bool) {
	if value, ok := record[field]; ok {
		return value, true
	}
	for key, value := range record {
		if strings.EqualFold(key, field) {
			return value, true
		}
	}
	return nil, false
}
