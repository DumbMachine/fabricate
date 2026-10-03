package ga4

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// methodMux registers Analytics Data custom methods such as
// /v1beta/properties/{property}:runReport. Go's ServeMux only accepts a
// wildcard as an entire path segment, so the suffix is matched by this mux
// and the property id is written back with SetPathValue before the generated
// handler reads it.
type methodMux struct {
	once   sync.Once
	std    *http.ServeMux
	groups map[string][]customRoute
}

type customRoute struct {
	captures []capture
	handler  func(http.ResponseWriter, *http.Request)
}

type capture struct {
	name   string
	suffix string
}

var customSegment = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\}:([^/]+)$`)

func newMethodMux() *methodMux {
	return &methodMux{std: http.NewServeMux(), groups: map[string][]customRoute{}}
}

func (m *methodMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || !strings.Contains(path, ":") {
		m.std.HandleFunc(pattern, handler)
		return
	}
	rewritten, captures := rewriteCustomMethod(path)
	key := method + " " + rewritten
	m.groups[key] = append(m.groups[key], customRoute{captures: captures, handler: handler})
}

func (m *methodMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.once.Do(m.install)
	m.std.ServeHTTP(w, r)
}

func (m *methodMux) install() {
	for key, routes := range m.groups {
		routes := routes
		m.std.HandleFunc(key, func(w http.ResponseWriter, r *http.Request) {
			for _, route := range routes {
				if applyCaptures(r, route.captures) {
					route.handler(w, r)
					return
				}
			}
			http.NotFound(w, r)
		})
	}
}

func rewriteCustomMethod(path string) (string, []capture) {
	parts := strings.Split(path, "/")
	captures := make([]capture, 0, 2)
	for i, part := range parts {
		if match := customSegment.FindStringSubmatch(part); match != nil {
			parts[i] = "{" + match[1] + "}"
			captures = append(captures, capture{name: match[1], suffix: match[2]})
			continue
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			captures = append(captures, capture{name: part[1 : len(part)-1]})
		}
	}
	return strings.Join(parts, "/"), captures
}

func applyCaptures(r *http.Request, captures []capture) bool {
	updates := make([][2]string, 0, len(captures))
	for _, capture := range captures {
		if capture.suffix == "" {
			continue
		}
		id, suffix, ok := strings.Cut(r.PathValue(capture.name), ":")
		if !ok || suffix != capture.suffix || id == "" {
			return false
		}
		updates = append(updates, [2]string{capture.name, id})
	}
	for _, update := range updates {
		r.SetPathValue(update[0], update[1])
	}
	return true
}
