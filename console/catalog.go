package console

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/environments"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

type Catalog struct {
	Environments []CatalogEnvironment `json:"environments"`
	Scenarios    []CatalogScenario    `json:"scenarios"`
}

type CatalogEnvironment struct {
	Name     string           `json:"name"`
	Label    string           `json:"label"`
	Services []CatalogService `json:"services"`
}

type CatalogService struct {
	Name          string   `json:"name"`
	Resource      string   `json:"resource"`
	DisplayName   string   `json:"displayName"`
	Scenario      string   `json:"scenario"`
	ProviderHosts []string `json:"providerHosts"`
}

type CatalogScenario struct {
	ID              string   `json:"id"`
	Resource        string   `json:"resource"`
	DisplayName     string   `json:"displayName"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ResourceVersion string   `json:"resourceVersion"`
	ProviderHosts   []string `json:"providerHosts"`
}

func buildCatalog(registry *httpresource.Registry) (Catalog, error) {
	names, err := environments.Names()
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{
		Environments: make([]CatalogEnvironment, 0, len(names)),
		Scenarios:    []CatalogScenario{},
	}
	for _, name := range names {
		spec, err := environments.Load(name)
		if err != nil {
			return Catalog{}, err
		}
		services := make([]CatalogService, 0, len(spec.Services))
		for _, serviceName := range serviceNames(spec) {
			service := spec.Services[serviceName]
			services = append(services, CatalogService{
				Name:          serviceName,
				Resource:      service.Resource,
				DisplayName:   resourceDisplayName(registry, service.Resource),
				Scenario:      service.Scenario,
				ProviderHosts: providerHosts(registry, service.Resource),
			})
		}
		catalog.Environments = append(catalog.Environments, CatalogEnvironment{
			Name:     spec.Metadata.Name,
			Label:    humanize(spec.Metadata.Name),
			Services: services,
		})
	}

	descriptors := registry.Descriptors()
	for _, descriptor := range descriptors {
		resource, ok := registry.Get(descriptor.ID)
		if !ok {
			continue
		}
		docs, err := resource.ScenarioDocuments()
		if err != nil {
			return Catalog{}, err
		}
		hosts := append([]string(nil), descriptor.ProviderHosts...)
		sort.Strings(hosts)
		for _, doc := range docs {
			scalars, collections, err := summarizeState(doc.State)
			if err != nil {
				return Catalog{}, fmt.Errorf("catalog: describe scenario %s: %w", doc.ID, err)
			}
			catalog.Scenarios = append(catalog.Scenarios, CatalogScenario{
				ID:              doc.ID,
				Resource:        doc.Resource,
				DisplayName:     descriptor.DisplayName,
				Name:            scenarioName(doc.ID, doc.Resource),
				Description:     scenarioDescription(scalars, collections),
				ResourceVersion: doc.ResourceVersion,
				ProviderHosts:   hosts,
			})
		}
	}
	sort.Slice(catalog.Scenarios, func(i, j int) bool {
		if catalog.Scenarios[i].Resource == catalog.Scenarios[j].Resource {
			return catalog.Scenarios[i].ID < catalog.Scenarios[j].ID
		}
		return catalog.Scenarios[i].Resource < catalog.Scenarios[j].Resource
	})
	return catalog, nil
}

func officialEnvironment(name string) (environment.Spec, error) {
	names, err := environments.Names()
	if err != nil {
		return environment.Spec{}, err
	}
	for _, candidate := range names {
		if candidate == name {
			return environments.Load(name)
		}
	}
	return environment.Spec{}, errUnknownEnvironment(name)
}

func scenarioEnvironment(id string, registry *httpresource.Registry) (environment.Spec, error) {
	resourceID, _, ok := strings.Cut(id, ".")
	if !ok {
		return environment.Spec{}, errUnknownScenario(id)
	}
	resource, ok := registry.Get(resourceID)
	if !ok {
		return environment.Spec{}, errUnknownScenario(id)
	}
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		return environment.Spec{}, err
	}
	doc, ok := scenario.Lookup(docs, id)
	if !ok {
		return environment.Spec{}, errUnknownScenario(id)
	}
	return environment.Spec{
		APIVersion: environment.APIVersion,
		Kind:       "Environment",
		Metadata:   environment.Metadata{Name: resourceID},
		Services: map[string]environment.ServiceSpec{
			resourceID: {Resource: resourceID, Scenario: doc.ID},
		},
	}, nil
}

func serviceNames(spec environment.Spec) []string {
	names := make([]string, 0, len(spec.Services))
	for name := range spec.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resourceDisplayName(registry *httpresource.Registry, id string) string {
	resource, ok := registry.Get(id)
	if !ok {
		return id
	}
	if name := resource.Descriptor().DisplayName; name != "" {
		return name
	}
	return id
}

func providerHosts(registry *httpresource.Registry, id string) []string {
	resource, ok := registry.Get(id)
	if !ok {
		return []string{}
	}
	hosts := append([]string(nil), resource.Descriptor().ProviderHosts...)
	sort.Strings(hosts)
	if hosts == nil {
		return []string{}
	}
	return hosts
}

func humanize(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
