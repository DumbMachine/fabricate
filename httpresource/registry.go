package httpresource

import (
	"fmt"
	"sort"
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
		r.byID[id] = resource
	}
	return r, nil
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
