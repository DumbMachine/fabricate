package httpresource

import (
	"fmt"
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
		if err := ValidateDescriptor(resource.Descriptor()); err != nil {
			return nil, err
		}
		r.byID[id] = resource
	}
	return r, nil
}

func ValidateDescriptor(descriptor Descriptor) error {
	hosts := make(map[string]struct{}, len(descriptor.ProviderHosts))
	for _, host := range descriptor.ProviderHosts {
		hosts[host] = struct{}{}
	}
	for host, prefix := range descriptor.HostPrefixes {
		if _, ok := hosts[host]; !ok {
			return fmt.Errorf("httpresource %s: host prefix %q is not a provider host", descriptor.ID, host)
		}
		if !canonicalPathPrefix(prefix) {
			return fmt.Errorf("httpresource %s: host prefix %q for %q must be \"/\" or start and end with /", descriptor.ID, prefix, host)
		}
	}
	for _, route := range descriptor.ProxyRoutes {
		if strings.TrimSpace(route.Host) == "" || strings.Contains(route.Host, "/") {
			return fmt.Errorf("httpresource %s: proxy route host %q is invalid", descriptor.ID, route.Host)
		}
		if !strings.HasPrefix(route.PathPrefix, "/") || strings.Contains(route.PathPrefix, " ") {
			return fmt.Errorf("httpresource %s: proxy route prefix %q for %q must start with /", descriptor.ID, route.PathPrefix, route.Host)
		}
	}
	return nil
}

func canonicalPathPrefix(prefix string) bool {
	if prefix == "/" {
		return true
	}
	return len(prefix) > 2 && strings.HasPrefix(prefix, "/") && strings.HasSuffix(prefix, "/") && !strings.Contains(prefix, " ")
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
