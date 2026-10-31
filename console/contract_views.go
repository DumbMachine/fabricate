package console

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Contract views are projections of the compiled OpenAPI, never a second
// operation registry. Agents can discover names before requesting exact schemas.
func operationView(spec map[string]any, query url.Values) (map[string]any, error) {
	offset, limit := 0, 50
	var err error
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return nil, fmt.Errorf("invalid operation offset")
		}
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return nil, fmt.Errorf("operation limit must be 1 to 100")
		}
	}
	operations := []map[string]any{}
	paths, _ := spec["paths"].(map[string]any)
	for path, raw := range paths {
		item, _ := raw.(map[string]any)
		for method, value := range item {
			op, _ := value.(map[string]any)
			id, _ := op["operationId"].(string)
			if id == "" {
				continue
			}
			operations = append(operations, map[string]any{"id": id, "method": strings.ToUpper(method), "path": path, "summary": op["summary"]})
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i]["id"].(string) < operations[j]["id"].(string) })
	total := len(operations)
	start := min(offset, total)
	end := min(start+limit, total)
	return map[string]any{"operations": operations[start:end], "offset": offset, "limit": limit, "total": total, "hasMore": end < total}, nil
}
func compactOpenAPI(source map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"openapi", "info", "servers", "paths", "security"} {
		if value, ok := source[key]; ok {
			out[key] = value
		}
	}
	if components, ok := source["components"].(map[string]any); ok {
		if schemes, ok := components["securitySchemes"]; ok {
			out["components"] = map[string]any{"securitySchemes": schemes}
		}
	}
	var refs []string
	var collect func(any)
	collect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if key == "$ref" {
					if ref, ok := item.(string); ok && strings.HasPrefix(ref, "#/") {
						refs = append(refs, ref)
					}
				}
				collect(item)
			}
		case []any:
			for _, item := range v {
				collect(item)
			}
		}
	}
	collect(out)
	seen := map[string]bool{}
	for i := 0; i < len(refs); i++ {
		ref := refs[i]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		var value any = source
		valid := true
		for j, part := range parts {
			parts[j] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			obj, ok := value.(map[string]any)
			if !ok {
				valid = false
				break
			}
			value, ok = obj[parts[j]]
			if !ok {
				valid = false
				break
			}
		}
		if !valid {
			continue
		} // Preserve unresolved refs so existing validation fails explicitly.
		current := out
		for _, part := range parts[:len(parts)-1] {
			next, ok := current[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				current[part] = next
			}
			current = next
		}
		current[parts[len(parts)-1]] = value
		collect(value)
	}
	return out
}
