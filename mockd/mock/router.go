package mock

import "strings"

// router matches (method, path) to a handler. Patterns use {param} segments and
// AIP-136 custom-method suffixes (":search", ":reply"). A route declares whether
// it has a verb suffix, so colon-bearing ids (Google review ids like "gp:AOq…")
// are unambiguous: a verb route matches only when the path ends in ":<verb>";
// a plain route keeps the colon as part of the id.
type router struct{ routes []route }

type route struct {
	method  string
	segs    []segment
	verb    string // custom-method suffix on the final segment ("" if none)
	handler Handler
}

type segment struct {
	literal string
	param   string // non-empty → capture this segment
}

func (rt *router) handle(method, pattern string, h Handler) {
	segs, verb := compile(pattern)
	rt.routes = append(rt.routes, route{method: strings.ToUpper(method), segs: segs, verb: verb, handler: h})
}

// compile splits "/a/{id}/b:reply" into segments [a {id} b] with verb "reply".
func compile(pattern string) ([]segment, string) {
	p := strings.TrimPrefix(pattern, "/")
	parts := strings.Split(p, "/")
	verb := ""
	if n := len(parts); n > 0 {
		if i := strings.LastIndex(parts[n-1], ":"); i >= 0 {
			verb = parts[n-1][i+1:]
			parts[n-1] = parts[n-1][:i]
		}
	}
	segs := make([]segment, len(parts))
	for i, s := range parts {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			segs[i] = segment{param: s[1 : len(s)-1]}
		} else {
			segs[i] = segment{literal: s}
		}
	}
	return segs, verb
}

// match returns the handler + captured params for a request. A verb route
// requires the path to end in ":<verb>" (stripped before segment matching); a
// plain route matches the path verbatim, so an id's internal colons are kept.
func (rt *router) match(method, path string) (Handler, map[string]string, bool) {
	method = strings.ToUpper(method)
	rp := strings.TrimPrefix(path, "/")
	for _, r := range rt.routes {
		if r.method != method {
			continue
		}
		p := rp
		if r.verb != "" {
			suffix := ":" + r.verb
			if !strings.HasSuffix(p, suffix) {
				continue
			}
			p = strings.TrimSuffix(p, suffix)
		}
		parts := strings.Split(p, "/")
		if len(parts) != len(r.segs) {
			continue
		}
		params := make(map[string]string, len(r.segs))
		ok := true
		for i, seg := range r.segs {
			if seg.param != "" {
				params[seg.param] = parts[i]
			} else if seg.literal != parts[i] {
				ok = false
				break
			}
		}
		if ok {
			return r.handler, params, true
		}
	}
	return nil, nil, false
}
