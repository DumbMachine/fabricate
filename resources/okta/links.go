package okta

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/dumbmachine/fabricate/resources/okta/generated"
)

const providerHost = "acme.okta.com"

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

// pageLinks returns Okta's Link header values: rel="self" for the request as
// sent, and rel="next" with after=<last id on this page> when more results
// exist. Every other query parameter is carried into the next link.
func pageLinks(ctx context.Context, next string) []string {
	self, _ := ctx.Value(requestURLKey{}).(*url.URL)
	if self == nil {
		return nil
	}
	links := []string{"<" + self.String() + `>; rel="self"`}
	if next != "" {
		query := self.Query()
		query.Set("after", next)
		nextURL := *self
		nextURL.RawQuery = query.Encode()
		links = append(links, "<"+nextURL.String()+`>; rel="next"`)
	}
	return links
}

// Okta sends rel="self" and rel="next" as separate Link header fields, and
// okta-sdk-golang reads them one field at a time. The generated strict
// writers can only Set a single Link value, so these wrappers Add each link
// before delegating and leave the generated Headers.Link field nil.

func addLinks(w http.ResponseWriter, links []string) {
	for _, link := range links {
		w.Header().Add("Link", link)
	}
}

type listUsersPage struct {
	generated.ListUsers200JSONResponse
	links []string
}

func (p listUsersPage) VisitListUsersResponse(w http.ResponseWriter) error {
	addLinks(w, p.links)
	return p.ListUsers200JSONResponse.VisitListUsersResponse(w)
}

type listGroupsPage struct {
	generated.ListGroups200JSONResponse
	links []string
}

func (p listGroupsPage) VisitListGroupsResponse(w http.ResponseWriter) error {
	addLinks(w, p.links)
	return p.ListGroups200JSONResponse.VisitListGroupsResponse(w)
}

type listGroupUsersPage struct {
	generated.ListGroupUsers200JSONResponse
	links []string
}

func (p listGroupUsersPage) VisitListGroupUsersResponse(w http.ResponseWriter) error {
	addLinks(w, p.links)
	return p.ListGroupUsers200JSONResponse.VisitListGroupUsersResponse(w)
}

type listApplicationsPage struct {
	generated.ListApplications200JSONResponse
	links []string
}

func (p listApplicationsPage) VisitListApplicationsResponse(w http.ResponseWriter) error {
	addLinks(w, p.links)
	return p.ListApplications200JSONResponse.VisitListApplicationsResponse(w)
}

type listApplicationUsersPage struct {
	generated.ListApplicationUsers200JSONResponse
	links []string
}

func (p listApplicationUsersPage) VisitListApplicationUsersResponse(w http.ResponseWriter) error {
	addLinks(w, p.links)
	return p.ListApplicationUsers200JSONResponse.VisitListApplicationUsersResponse(w)
}
