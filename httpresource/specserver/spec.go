package specserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PathDrop reports whether a vendor path template should be omitted from the
// running server. GitHub git-object routes are the intended use.
type PathDrop func(path string) bool

// GitHubGitPath reports repository routes that require a real git
// implementation (git objects, file contents, archives, source import, and
// template-repo generate).
func GitHubGitPath(path string) bool {
	switch {
	case strings.Contains(path, "/git/"):
		return true
	case strings.Contains(path, "/contents/") || strings.HasSuffix(path, "/contents"):
		return true
	case strings.Contains(path, "/tarball/") || strings.HasSuffix(path, "/tarball"):
		return true
	case strings.Contains(path, "/zipball/") || strings.HasSuffix(path, "/zipball"):
		return true
	case strings.Contains(path, "/import/") || strings.HasSuffix(path, "/import"):
		return true
	case strings.HasSuffix(path, "/generate"):
		return true
	default:
		return false
	}
}

type CompiledSpec struct {
	Version string
	Title   string
	JSON    []byte
	Index   *Index
}

type Index struct {
	routes []route
}

type route struct {
	method      string
	template    string
	operationID string
	collection  string
	item        bool
	wrap        wrapStyle
	created     bool
	regex       *regexp.Regexp
	paramNames  []string
}

type wrapStyle struct {
	kind string // "", "result", "results", "data", "named"
	name string
}

func CompileSpec(raw []byte, drop PathDrop) (*CompiledSpec, error) {
	doc, err := decodeSpec(raw)
	if err != nil {
		return nil, err
	}
	if drop != nil {
		paths, _ := doc["paths"].(map[string]any)
		for path := range paths {
			if drop(path) {
				delete(paths, path)
			}
		}
	}
	assignMissingOperationIDs(doc)
	idx, err := buildIndex(doc)
	if err != nil {
		return nil, err
	}
	contract, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("specserver: marshal contract: %w", err)
	}
	version, _ := doc["openapi"].(string)
	if version == "" {
		version, _ = doc["swagger"].(string)
	}
	title := ""
	if info, _ := doc["info"].(map[string]any); info != nil {
		title, _ = info["title"].(string)
	}
	return &CompiledSpec{Version: version, Title: title, JSON: contract, Index: idx}, nil
}

func decodeSpec(raw []byte) (map[string]any, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 {
		return nil, fmt.Errorf("specserver: OpenAPI document is empty")
	}
	var doc map[string]any
	if trim[0] == '{' || trim[0] == '[' {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("specserver: parse JSON OpenAPI: %w", err)
		}
		return doc, nil
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("specserver: parse YAML OpenAPI: %w", err)
	}
	typed, ok := jsonable(doc).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("specserver: OpenAPI document must be an object")
	}
	return typed, nil
}

func jsonable(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = jsonable(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = jsonable(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = jsonable(child)
		}
		return out
	default:
		return value
	}
}

func assignMissingOperationIDs(doc map[string]any) {
	paths, _ := doc["paths"].(map[string]any)
	for path, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, op := range ops {
			if !isHTTPMethod(method) {
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := opMap["operationId"].(string); strings.TrimSpace(id) == "" {
				opMap["operationId"] = operationIDFrom(method, path)
			}
		}
	}
}

func isHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
}

func operationIDFrom(method, path string) string {
	trimmed := strings.Trim(path, "/")
	trimmed = strings.ReplaceAll(trimmed, "{", "")
	trimmed = strings.ReplaceAll(trimmed, "}", "")
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '/' || r == '-' || r == '.' || r == '?'
	})
	return strings.ToLower(method) + "_" + strings.Join(parts, "_")
}

func buildIndex(doc map[string]any) (*Index, error) {
	prefixes := serverPrefixes(doc)
	components, _ := doc["components"].(map[string]any)
	paths, _ := doc["paths"].(map[string]any)
	var routes []route
	for path, item := range paths {
		ops, ok := resolvePathItem(item, doc)
		if !ok {
			continue
		}
		cleanPath := stripPathQuery(path)
		for method, op := range ops {
			if !isHTTPMethod(method) {
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				continue
			}
			id, _ := opMap["operationId"].(string)
			wrap := inferWrap(opMap, components)
			created := hasStatus(opMap, "201")
			for _, prefix := range prefixes {
				template := joinPrefix(prefix, cleanPath)
				re, names := compileTemplate(template)
				routes = append(routes, route{
					method:      strings.ToUpper(method),
					template:    template,
					operationID: id,
					collection:  collectionName(template),
					item:        isItemPath(template),
					wrap:        wrap,
					created:     created,
					regex:       re,
					paramNames:  names,
				})
			}
		}
	}
	sort.SliceStable(routes, func(i, j int) bool {
		si, sj := staticLen(routes[i].template), staticLen(routes[j].template)
		if si != sj {
			return si > sj
		}
		if routes[i].template != routes[j].template {
			return routes[i].template < routes[j].template
		}
		return routes[i].method < routes[j].method
	})
	return &Index{routes: routes}, nil
}

func (idx *Index) Len() int { return len(idx.routes) }

func (idx *Index) Operations() []struct{ Method, Path, OperationID string } {
	out := make([]struct{ Method, Path, OperationID string }, 0, len(idx.routes))
	seen := map[string]struct{}{}
	for _, rt := range idx.routes {
		key := rt.method + " " + rt.template
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, struct{ Method, Path, OperationID string }{rt.method, rt.template, rt.operationID})
	}
	return out
}

func (idx *Index) find(method, path string) (route, map[string]string, bool) {
	method = strings.ToUpper(method)
	for _, rt := range idx.routes {
		if rt.method != method {
			continue
		}
		match := rt.regex.FindStringSubmatch(path)
		if match == nil {
			continue
		}
		params := map[string]string{}
		for i, name := range rt.paramNames {
			if i+1 < len(match) {
				params[name] = match[i+1]
			}
		}
		return rt, params, true
	}
	return route{}, nil, false
}

func serverPrefixes(doc map[string]any) []string {
	servers, _ := doc["servers"].([]any)
	seen := map[string]struct{}{}
	var prefixes []string
	for _, server := range servers {
		item, _ := server.(map[string]any)
		raw, _ := item["url"].(string)
		prefix := pathPrefix(raw)
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 0 {
		return []string{""}
	}
	return prefixes
}

func pathPrefix(raw string) string {
	if raw == "" {
		return ""
	}
	withoutScheme := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		withoutScheme = raw[i+3:]
	}
	slash := strings.Index(withoutScheme, "/")
	path := ""
	if slash >= 0 {
		path = withoutScheme[slash:]
	}
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimRight(path, "/")
	if path == "" || path == "/" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func joinPrefix(prefix, path string) string {
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	if prefix == "" {
		return path
	}
	if path == prefix || strings.HasPrefix(path, prefix+"/") {
		return path
	}
	return prefix + path
}

func stripPathQuery(path string) string {
	if i := strings.Index(path, "?"); i >= 0 {
		return path[:i]
	}
	return path
}

func resolvePathItem(item any, doc map[string]any) (map[string]any, bool) {
	ops, ok := item.(map[string]any)
	if !ok {
		return nil, false
	}
	if ref, _ := ops["$ref"].(string); ref != "" {
		resolved, ok := resolveRef(ref, doc).(map[string]any)
		if !ok {
			return nil, false
		}
		return resolved, true
	}
	return ops, true
}

func resolveRef(ref string, doc map[string]any) any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	cur := any(doc)
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return cur
}

var paramPattern = regexp.MustCompile(`\{[^{}]+\}`)

func compileTemplate(template string) (*regexp.Regexp, []string) {
	trimmed := strings.TrimRight(template, "/")
	var names []string
	var b strings.Builder
	b.WriteString("^")
	last := 0
	for _, match := range paramPattern.FindAllStringIndex(trimmed, -1) {
		b.WriteString(regexp.QuoteMeta(trimmed[last:match[0]]))
		raw := trimmed[match[0]+1 : match[1]-1]
		name := strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "*")
		if i := strings.IndexAny(name, ":="); i >= 0 {
			name = name[:i]
		}
		names = append(names, name)
		if strings.Contains(raw, "*") || strings.HasPrefix(raw, "+") {
			b.WriteString("(.+)")
		} else {
			b.WriteString("([^/]+)")
		}
		last = match[1]
	}
	b.WriteString(regexp.QuoteMeta(trimmed[last:]))
	b.WriteString("/?$")
	return regexp.MustCompile(b.String()), names
}

func staticLen(template string) int {
	return len(paramPattern.ReplaceAllString(template, ""))
}

func collectionName(template string) string {
	parts := strings.Split(strings.Trim(template, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" || strings.HasPrefix(part, "{") {
			continue
		}
		return strings.TrimSuffix(part, ".json")
	}
	return "items"
}

func isItemPath(template string) bool {
	parts := strings.Split(strings.Trim(template, "/"), "/")
	if len(parts) == 0 {
		return false
	}
	last := parts[len(parts)-1]
	return strings.HasPrefix(last, "{") && strings.HasSuffix(last, "}")
}

func itemIDFromParams(template string, params map[string]string) string {
	parts := strings.Split(strings.Trim(template, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	last := strings.Trim(parts[len(parts)-1], "{}")
	last = strings.TrimPrefix(strings.TrimPrefix(last, "+"), "*")
	if i := strings.IndexAny(last, ":="); i >= 0 {
		last = last[:i]
	}
	if value := params[last]; value != "" {
		return value
	}
	for _, key := range []string{"id", "Id", "ID", "slug"} {
		if value := params[key]; value != "" {
			return value
		}
	}
	for _, value := range params {
		if value != "" {
			return value
		}
	}
	return ""
}

func InstantiatePath(template string) string {
	return paramPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.Trim(match, "{}")
		if strings.Contains(name, "*") || strings.HasPrefix(name, "+") {
			return "item/nested"
		}
		return "item"
	})
}
