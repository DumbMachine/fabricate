package shopify

import (
	"net/http"
	"strings"
)

// patternMux matches Admin REST patterns such as /orders/{order_id}.json.
// net/http.ServeMux requires each wildcard to be a full path segment, so a
// pattern ending in {order_id}.json panics. The OpenAPI validator uses
// gorilla/mux, which accepts that suffix, and the generated wrappers read
// the captured value from r.PathValue.
type patternMux struct {
	routes []patternRoute
}

type patternRoute struct {
	method  string
	segs    []patternSegment
	handler http.HandlerFunc
	score   int
}

type patternSegment struct {
	literal string
	name    string
	suffix  string
}

func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = method
		method = ""
	}
	parts := splitPath(path)
	segs := make([]patternSegment, len(parts))
	score := 0
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.Contains(part, "}") {
			name, rest, _ := strings.Cut(part[1:], "}")
			segs[i] = patternSegment{name: name, suffix: rest}
			continue
		}
		segs[i] = patternSegment{literal: part}
		score++
	}
	m.routes = append(m.routes, patternRoute{method: method, segs: segs, handler: handler, score: score})
}

func (m *patternMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	var best *patternRoute
	var values map[string]string
	for i := range m.routes {
		route := &m.routes[i]
		if route.method != "" && route.method != r.Method && !(route.method == http.MethodGet && r.Method == http.MethodHead) {
			continue
		}
		if len(route.segs) != len(parts) {
			continue
		}
		matched := map[string]string{}
		ok := true
		for j, seg := range route.segs {
			if seg.name == "" {
				if parts[j] != seg.literal {
					ok = false
					break
				}
				continue
			}
			value := parts[j]
			if seg.suffix != "" {
				if !strings.HasSuffix(value, seg.suffix) {
					ok = false
					break
				}
				value = strings.TrimSuffix(value, seg.suffix)
			}
			if value == "" {
				ok = false
				break
			}
			matched[seg.name] = value
		}
		if !ok {
			continue
		}
		if best == nil || route.score > best.score {
			best = route
			values = matched
		}
	}
	if best == nil {
		http.NotFound(w, r)
		return
	}
	for name, value := range values {
		r.SetPathValue(name, value)
	}
	best.handler(w, r)
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
