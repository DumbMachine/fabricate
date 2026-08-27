package specserver

func inferWrap(op map[string]any, components map[string]any) wrapStyle {
	schema := successSchema(op)
	return inferWrapSchema(schema, components, 0)
}

func successSchema(op map[string]any) any {
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return nil
	}
	for _, code := range []string{"200", "201", "202"} {
		resp, _ := responses[code].(map[string]any)
		if resp == nil {
			continue
		}
		if ref, _ := resp["$ref"].(string); ref != "" {
			continue
		}
		content, _ := resp["content"].(map[string]any)
		if jsonContent, _ := content["application/json"].(map[string]any); jsonContent != nil {
			if schema := jsonContent["schema"]; schema != nil {
				return schema
			}
		}
		for _, typed := range content {
			item, _ := typed.(map[string]any)
			if item == nil {
				continue
			}
			if schema := item["schema"]; schema != nil {
				return schema
			}
		}
	}
	return nil
}

func hasStatus(op map[string]any, code string) bool {
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return false
	}
	_, ok := responses[code]
	return ok
}

func inferWrapSchema(schema any, components map[string]any, depth int) wrapStyle {
	if schema == nil || depth > 8 {
		return wrapStyle{}
	}
	m, ok := schema.(map[string]any)
	if !ok {
		return wrapStyle{}
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		return inferWrapSchema(resolveRef(ref, map[string]any{"components": components}), components, depth+1)
	}
	if allOf, _ := m["allOf"].([]any); len(allOf) > 0 {
		style := wrapStyle{}
		for _, part := range allOf {
			next := inferWrapSchema(part, components, depth+1)
			if next.kind != "" {
				style = next
			}
		}
		if style.kind != "" {
			return style
		}
	}
	if oneOf, _ := m["oneOf"].([]any); len(oneOf) > 0 {
		return inferWrapSchema(oneOf[0], components, depth+1)
	}
	if anyOf, _ := m["anyOf"].([]any); len(anyOf) > 0 {
		return inferWrapSchema(anyOf[0], components, depth+1)
	}
	if typeIs(m, "array") {
		return wrapStyle{}
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		return wrapStyle{}
	}
	if _, ok := props["result"]; ok {
		return wrapStyle{kind: "result"}
	}
	if _, ok := props["results"]; ok {
		return wrapStyle{kind: "results"}
	}
	if _, ok := props["data"]; ok {
		return wrapStyle{kind: "data"}
	}
	for _, name := range []string{"projects", "zones", "droplets", "customers", "vals", "webhooks", "issues", "tasks", "items", "project", "droplet", "customer", "zone", "val", "webhook", "issue", "task"} {
		if _, ok := props[name]; ok {
			return wrapStyle{kind: "named", name: name}
		}
	}
	return wrapStyle{}
}

func typeIs(schema map[string]any, want string) bool {
	switch typed := schema["type"].(type) {
	case string:
		return typed == want
	case []any:
		for _, item := range typed {
			if item == want {
				return true
			}
		}
	}
	return false
}

func wrapValue(style wrapStyle, payload any, list bool) any {
	switch style.kind {
	case "result":
		out := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": payload}
		if list {
			out["result_info"] = map[string]any{"count": lenAny(payload), "page": 1, "per_page": 50, "total_count": lenAny(payload)}
		}
		return out
	case "results":
		return map[string]any{"results": payload, "count": lenAny(payload), "next": nil, "previous": nil}
	case "data":
		out := map[string]any{"data": payload}
		if list {
			out["object"] = "list"
			out["has_more"] = false
			out["url"] = ""
		}
		return out
	case "named":
		return map[string]any{style.name: payload}
	default:
		return payload
	}
}

func wrapError(style wrapStyle, message string) any {
	if style.kind == "result" {
		return map[string]any{"success": false, "errors": []any{map[string]any{"message": message}}, "result": nil}
	}
	return map[string]any{"message": message}
}

func unwrapBody(body map[string]any, style wrapStyle) map[string]any {
	if style.kind == "named" && style.name != "" {
		if inner, ok := body[style.name].(map[string]any); ok {
			return inner
		}
	}
	if style.kind == "data" {
		if inner, ok := body["data"].(map[string]any); ok {
			return inner
		}
	}
	if style.kind == "result" {
		if inner, ok := body["result"].(map[string]any); ok {
			return inner
		}
	}
	return body
}

func looksLikeList(style wrapStyle, item bool) bool {
	if item {
		return false
	}
	return style.kind == "result" || style.kind == "results" || style.kind == "data" || style.kind == "named" || style.kind == ""
}

func lenAny(value any) int {
	switch typed := value.(type) {
	case []map[string]any:
		return len(typed)
	case []any:
		return len(typed)
	default:
		return 0
	}
}
