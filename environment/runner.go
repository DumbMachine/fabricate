package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	httpengine "github.com/dumbmachine/fabricate/engine/http"
	proxyengine "github.com/dumbmachine/fabricate/engine/http/proxy"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/requestlog"
)

type Runtime struct {
	Spec     Spec
	StateDir string
	Services map[string]*httpengine.Service
	Proxy    *proxyengine.Proxy
	Requests *requestlog.Log
}

func Start(ctx context.Context, spec Spec, registry *httpresource.Registry, enableProxy bool) (_ *Runtime, err error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("environment: resource registry is required")
	}
	stateDir, err := os.MkdirTemp("", "fab-"+spec.Metadata.Name+"-")
	if err != nil {
		return nil, fmt.Errorf("environment: create state directory: %w", err)
	}
	runtime := &Runtime{Spec: spec, StateDir: stateDir, Services: make(map[string]*httpengine.Service)}
	committed := false
	defer func() {
		if !committed {
			_ = runtime.Close(context.Background())
		}
	}()
	runtime.Requests, err = requestlog.New(spec.Metadata.Name)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(spec.Services))
	for name := range spec.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		serviceSpec := spec.Services[name]
		resource, ok := registry.Get(serviceSpec.Resource)
		if !ok {
			return nil, fmt.Errorf("environment: service %q uses unknown resource %q", name, serviceSpec.Resource)
		}
		doc, err := resource.Scenario(serviceSpec.Scenario)
		if err != nil {
			return nil, fmt.Errorf("environment: service %q: %w", name, err)
		}
		if doc.Resource != resource.Descriptor().ID {
			return nil, fmt.Errorf("environment: service %q scenario %q belongs to resource %q", name, doc.ID, doc.Resource)
		}
		service, err := httpengine.StartService(ctx, name, filepath.Join(stateDir, "services", name), resource, doc, runtime.Requests)
		if err != nil {
			return nil, err
		}
		runtime.Services[name] = service
	}

	if enableProxy || spec.Proxy.Enabled {
		routes, err := runtime.proxyRoutes()
		if err != nil {
			return nil, err
		}
		runtime.Proxy, err = proxyengine.Start(filepath.Join(stateDir, "proxy"), routes, runtime.Requests, spec.Proxy.Passthrough...)
		if err != nil {
			return nil, err
		}
	}
	committed = true
	return runtime, nil
}

func (r *Runtime) Environment() map[string]string {
	environment := map[string]string{"FAB_ENVIRONMENT": r.Spec.Metadata.Name}
	for name, service := range r.Services {
		prefix := "FAB_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		environment[prefix+"_URL"] = service.URL
		environment[prefix+"_TOKEN"] = service.Token
	}
	if r.Proxy != nil {
		for key, value := range r.Proxy.Environment() {
			environment[key] = value
		}
	}
	return environment
}

func (r *Runtime) Close(ctx context.Context) error {
	var first error
	if r.Proxy != nil {
		if err := r.Proxy.Close(ctx); err != nil {
			first = err
		}
	}
	names := make([]string, 0, len(r.Services))
	for name := range r.Services {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if err := r.Services[name].Close(ctx); first == nil && err != nil {
			first = err
		}
	}
	if err := os.RemoveAll(r.StateDir); first == nil && err != nil {
		first = err
	}
	if r.Requests != nil {
		if err := r.Requests.Close(); first == nil && err != nil {
			first = err
		}
	}
	return first
}

func (r *Runtime) proxyRoutes() ([]proxyengine.Route, error) {
	explicit := make(map[string]string, len(r.Spec.Proxy.Hosts))
	for route, service := range r.Spec.Proxy.Hosts {
		explicit[normalizeRouteKey(route)] = service
	}
	claimed := map[string]string{}
	var routes []proxyengine.Route
	for name, service := range r.Services {
		descriptor := service.Resource.Descriptor()
		for _, host := range descriptor.ProviderHosts {
			prefix := "/"
			if descriptor.ID == "gmail" && host == "www.googleapis.com" {
				prefix = "/gmail/"
			}
			key := normalizeRouteKey(host + prefix)
			selected := name
			if override, ok := explicit[key]; ok {
				selected = override
			}
			if selected != name {
				continue
			}
			if previous, exists := claimed[key]; exists && previous != name {
				return nil, fmt.Errorf("environment: proxy route %s is claimed by %q and %q; select one with proxy.hosts", key, previous, name)
			}
			claimed[key] = name
			routes = append(routes, proxyengine.Route{Host: host, PathPrefix: prefix, Target: service.URL, Token: service.Token, Service: name})
		}
		if descriptor.ID == "gmail" {
			key := normalizeRouteKey("oauth2.googleapis.com/token")
			selected := name
			if override, ok := explicit[key]; ok {
				selected = override
			}
			if selected == name {
				if previous, exists := claimed[key]; exists && previous != name {
					return nil, fmt.Errorf("environment: OAuth proxy route is ambiguous between %q and %q", previous, name)
				}
				claimed[key] = name
				routes = append(routes, proxyengine.Route{Host: "oauth2.googleapis.com", PathPrefix: "/token", Token: service.Token, OAuthToken: true, Service: name})
			}
		}
	}
	for route, name := range explicit {
		if _, ok := claimed[route]; !ok {
			return nil, fmt.Errorf("environment: explicit proxy route %q for service %q does not match a provider route", route, name)
		}
	}
	return routes, nil
}

func normalizeRouteKey(route string) string {
	route = strings.TrimSpace(strings.ToLower(route))
	if !strings.Contains(route, "/") {
		return route + "/"
	}
	parts := strings.SplitN(route, "/", 2)
	return strings.TrimSuffix(parts[0], ".") + "/" + strings.TrimPrefix(parts[1], "/")
}

func CloseTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
