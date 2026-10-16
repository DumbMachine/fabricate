package greenhouse

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const providerHost = "harvest.greenhouse.io"

type requestURLKey struct{}

// withRequestURL records the public URL of the incoming request so strict
// handlers can build absolute pagination links. Through the transparent proxy
// the Host is the production hostname and the hop to this server is plain
// HTTP, so the provider host is treated as HTTPS. Direct requests keep the
// listener's scheme and host:port.
func withRequestURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") || canonicalHost(r.Host) == providerHost {
			scheme = "https"
		}
		public := &url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestURLKey{}, public)))
	})
}

func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if split, _, err := net.SplitHostPort(host); err == nil {
		host = split
	}
	return strings.TrimSuffix(host, ".")
}

// pageLinks builds Harvest's single comma-separated Link header:
// rel="prev" after page 1, rel="next" while more pages remain, and rel="last"
// always. Each link carries the request's other query parameters plus page
// and per_page.
func pageLinks(ctx context.Context, page, perPage, total int) *string {
	self, _ := ctx.Value(requestURLKey{}).(*url.URL)
	if self == nil || page < 1 || perPage < 1 {
		return nil
	}
	last := (total + perPage - 1) / perPage
	if last < 1 {
		last = 1
	}
	link := func(target int, rel string) string {
		query := self.Query()
		query.Set("page", strconv.Itoa(target))
		query.Set("per_page", strconv.Itoa(perPage))
		u := *self
		u.RawQuery = query.Encode()
		return "<" + u.String() + `>; rel="` + rel + `"`
	}
	links := []string{}
	if page > 1 {
		links = append(links, link(min(page-1, last), "prev"))
	}
	if page < last {
		links = append(links, link(page+1, "next"))
	}
	links = append(links, link(last, "last"))
	value := strings.Join(links, ", ")
	return &value
}
