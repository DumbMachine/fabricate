package httpresource

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

type Registry struct{ byID map[string]Resource }

func NewRegistry(resources ...Resource) (*Registry, error) {
	r := &Registry{byID: make(map[string]Resource, len(resources))}
	for _, resource := range resources {
		if resource == nil {
			return nil, fmt.Errorf("httpresource registry: nil resource")
		}
		id := resource.Descriptor().ID
		if id == "" {
			return nil, fmt.Errorf("httpresource registry: resource ID is required")
		}
		if _, exists := r.byID[id]; exists {
			return nil, fmt.Errorf("httpresource registry: duplicate resource %q", id)
		}
		if err := validateHostPrefixes(resource.Descriptor()); err != nil {
			return nil, fmt.Errorf("httpresource registry: resource %q: %w", id, err)
		}
		if err := validateTokenRoutes(resource.Descriptor()); err != nil {
			return nil, fmt.Errorf("httpresource registry: resource %q: %w", id, err)
		}
		r.byID[id] = resource
	}
	return r, nil
}

// validateHostPrefixes keeps path-prefix routing fail closed. Every key must
// name one of the descriptor's ProviderHosts exactly (the proxy looks prefixes
// up by exact host), otherwise a typo or case mismatch would be ignored and the
// resource would silently claim the whole host. Values must be canonical
// "/segment/" prefixes so the descriptor matches the route that is installed.
func validateHostPrefixes(descriptor Descriptor) error {
	hosts := make([]string, 0, len(descriptor.HostPrefixes))
	for host := range descriptor.HostPrefixes {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		prefix := descriptor.HostPrefixes[host]
		if !slices.Contains(descriptor.ProviderHosts, host) {
			return fmt.Errorf("host prefix for %q does not match any provider host %q", host, descriptor.ProviderHosts)
		}
		switch {
		case strings.TrimSpace(prefix) == "":
			return fmt.Errorf("host prefix for %q is empty", host)
		case strings.TrimSpace(prefix) != prefix:
			return fmt.Errorf("host prefix %q for %q has surrounding whitespace", prefix, host)
		case !strings.HasPrefix(prefix, "/"):
			return fmt.Errorf("host prefix %q for %q must start with \"/\"", prefix, host)
		case !strings.HasSuffix(prefix, "/"):
			return fmt.Errorf("host prefix %q for %q must end with \"/\"", prefix, host)
		case prefix == "/":
			return fmt.Errorf("host prefix for %q is \"/\", which claims the whole host; omit the entry instead", host)
		}
	}
	return nil
}

// validateTokenRoutes keeps synthetic OAuth routes exact. The proxy matches
// hosts in canonical form and answers only the declared path, so a host that is
// not lowercase and bare, or a path that is not a canonical absolute path,
// would install a route no client reaches and let refreshes fall through.
func validateTokenRoutes(descriptor Descriptor) error {
	seen := make(map[TokenRoute]bool, len(descriptor.TokenRoutes))
	for _, route := range descriptor.TokenRoutes {
		host, path := route.Host, route.Path
		switch {
		case host == "":
			return fmt.Errorf("token route host is empty")
		case host != strings.ToLower(host) || strings.ContainsAny(host, " \t\r\n/:") || strings.HasSuffix(host, "."):
			return fmt.Errorf("token route host %q must be a lowercase bare hostname", host)
		case path == "" || path == "/":
			return fmt.Errorf("token route path for %q must name an endpoint, not %q", host, path)
		case strings.ContainsAny(path, " \t\r\n?#"):
			return fmt.Errorf("token route path %q for %q must not contain whitespace, a query, or a fragment", path, host)
		case !strings.HasPrefix(path, "/"):
			return fmt.Errorf("token route path %q for %q must start with \"/\"", path, host)
		case strings.HasSuffix(path, "/"):
			return fmt.Errorf("token route path %q for %q must not end with \"/\"", path, host)
		case seen[route]:
			return fmt.Errorf("token route %s%s is declared twice", host, path)
		}
		seen[route] = true
	}
	return nil
}

func (r *Registry) Get(id string) (Resource, bool) {
	resource, ok := r.byID[id]
	return resource, ok
}

func (r *Registry) Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(r.byID))
	for _, resource := range r.byID {
		out = append(out, resource.Descriptor())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
