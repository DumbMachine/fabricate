package environment

import (
	"context"
	"fmt"
	"github.com/dumbmachine/fabricate/scenario"
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
	Spec       Spec
	StateDir   string
	Services   map[string]*httpengine.Service
	Proxy      *proxyengine.Proxy
	Requests   *requestlog.Log
	Persistent bool
	Metadata   map[string]scenario.Metadata
}

func Start(ctx context.Context, spec Spec, registry *httpresource.Registry, enableProxy bool) (*Runtime, error) {
	return StartWithDocuments(ctx, spec, registry, enableProxy, nil)
}

// StartWithDocuments loads caller-supplied baselines through the same codec and
// supervisor as catalog scenarios. It never evaluates code or reads caller paths.
func StartWithDocuments(ctx context.Context, spec Spec, registry *httpresource.Registry, enableProxy bool, documents map[string]scenario.Document) (_ *Runtime, err error) {
	return startWithStorage(ctx, spec, registry, enableProxy, documents, StorageOptions{})
}

// StorageOptions is owned by the supervisor, never accepted from an API caller.
type StorageOptions struct {
	Directory string
	Reopen    bool
	Tokens    map[string]string
	Metadata  map[string]scenario.Metadata
	LogRunID  string
}

func StartWithStorage(ctx context.Context, spec Spec, registry *httpresource.Registry, documents map[string]scenario.Document, storage StorageOptions) (*Runtime, error) {
	return startWithStorage(ctx, spec, registry, false, documents, storage)
}
func startWithStorage(ctx context.Context, spec Spec, registry *httpresource.Registry, enableProxy bool, documents map[string]scenario.Document, storage StorageOptions) (_ *Runtime, err error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("environment: resource registry is required")
	}
	resolved := map[string]scenario.Document{}
	for name := range documents {
		if _, ok := spec.Services[name]; !ok {
			return nil, fmt.Errorf("environment: unknown baseline service %q", name)
		}
	}
	for name, service := range spec.Services {
		resource, ok := registry.Get(service.Resource)
		if !ok {
			return nil, fmt.Errorf("environment: unknown resource %q", service.Resource)
		}
		if storage.Reopen {
			continue
		}
		doc, supplied := documents[name]
		if !supplied {
			doc, err = ResolveScenario(resource, service)
			if err != nil {
				return nil, err
			}
		}
		if doc.ID != service.Scenario || doc.Resource != service.Resource {
			return nil, fmt.Errorf("environment: baseline identity mismatch for %q", name)
		}
		if err := resource.Scenarios().Validate(ctx, doc); err != nil {
			return nil, fmt.Errorf("environment: service %q: %w", name, err)
		}
		resolved[name] = doc
	}
	stateDir := storage.Directory
	if stateDir == "" {
		stateDir, err = os.MkdirTemp("", "fab-"+spec.Metadata.Name+"-")
	} else if !storage.Reopen {
		err = os.Mkdir(stateDir, 0o700)
	}
	if err != nil {
		return nil, fmt.Errorf("environment: create state directory: %w", err)
	}
	runtime := &Runtime{Spec: spec, StateDir: stateDir, Services: make(map[string]*httpengine.Service), Persistent: storage.Directory != "", Metadata: map[string]scenario.Metadata{}}
	committed := false
	defer func() {
		if !committed {
			_ = runtime.Close(context.Background())
		}
	}()
	if storage.Reopen {
		runtime.Requests, err = requestlog.Resume(spec.Metadata.Name, storage.LogRunID)
	} else {
		runtime.Requests, err = requestlog.New(spec.Metadata.Name)
	}
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
		doc := resolved[name]
		var service *httpengine.Service
		if storage.Reopen {
			runtime.Metadata[name] = storage.Metadata[name]
			service, err = httpengine.OpenService(ctx, name, filepath.Join(stateDir, "services", name), resource, storage.Tokens[name], runtime.Requests)
		} else {
			runtime.Metadata[name] = doc.Metadata()
			service, err = httpengine.StartService(ctx, name, filepath.Join(stateDir, "services", name), resource, doc, runtime.Requests)
		}
		if err != nil {
			return nil, err
		}
		runtime.Services[name] = service
	}

	if enableProxy {
		routes, err := runtime.proxyRoutes()
		if err != nil {
			return nil, err
		}
		runtime.Proxy, err = proxyengine.Start(filepath.Join(stateDir, "proxy"), routes, runtime.Requests, proxyengine.Options{
			Passthrough:   spec.Proxy.Passthrough,
			RejectUnknown: spec.Proxy.RejectUnknownHosts(),
		})
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
	if !r.Persistent {
		if err := os.RemoveAll(r.StateDir); first == nil && err != nil {
			first = err
		}
	}
	if r.Requests != nil {
		if err := r.Requests.Close(); first == nil && err != nil {
			first = err
		}
	}
	return first
}

func hostPrefix(descriptor httpresource.Descriptor, host string) string {
	if value, ok := descriptor.HostPrefixes[host]; ok && value != "" {
		return value
	}
	return "/"
}

func descriptorClaims(descriptor httpresource.Descriptor) []httpresource.ProxyRoute {
	claims := make([]httpresource.ProxyRoute, 0, len(descriptor.ProviderHosts)+len(descriptor.ProxyRoutes))
	for _, host := range descriptor.ProviderHosts {
		claims = append(claims, httpresource.ProxyRoute{Host: host, PathPrefix: hostPrefix(descriptor, host)})
	}
	return append(claims, descriptor.ProxyRoutes...)
}

func (r *Runtime) proxyRoutes() ([]proxyengine.Route, error) {
	explicit := make(map[string]string, len(r.Spec.Proxy.Hosts))
	for route, service := range r.Spec.Proxy.Hosts {
		explicit[normalizeRouteKey(route)] = service
	}
	claimed := map[string]string{}
	var routes []proxyengine.Route
	for name, service := range r.Services {
		for _, claim := range descriptorClaims(service.Resource.Descriptor()) {
			key := normalizeRouteKey(claim.Host + claim.PathPrefix)
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
			route := proxyengine.Route{
				Host: claim.Host, PathPrefix: claim.PathPrefix, Token: service.Token,
				OAuthToken: claim.OAuthToken, Service: name,
			}
			if !claim.OAuthToken {
				route.Target = service.URL
			}
			routes = append(routes, route)
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

// ResolveScenario supports an exported immutable baseline without changing a provider.
func ResolveScenario(resource httpresource.Resource, s ServiceSpec) (scenario.Document, error) {
	if s.ScenarioFile == "" {
		return resource.Scenario(s.Scenario)
	}
	raw, err := os.ReadFile(s.ScenarioFile)
	if err != nil {
		return scenario.Document{}, err
	}
	doc, err := scenario.Parse(raw)
	if err != nil {
		return doc, err
	}
	if doc.ID != s.Scenario || doc.Resource != s.Resource {
		return doc, fmt.Errorf("scenario file identity does not match service")
	}
	if err = resource.Scenarios().Validate(context.Background(), doc); err != nil {
		return doc, err
	}
	return doc, nil
}
