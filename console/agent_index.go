package console

import "strings"

const agentIndexPath = "/.well-known/fabricate"

const agentIndexSummary = "Resources and scenarios this Fabricate console can start, plus the sandboxes that are running. " +
	"storeAs is the service name to keep in app config. urlName is the config key for the base URL, and secretName is the config key for the bearer token. " +
	"When a sandbox is running, services[].url and services[].secret are the values to store under those names. " +
	"scenarios[].running and environments[].services[].running are the sandbox ids using that scenario. " +
	"When a sandbox has proxy set, also store its proxy and ca secrets, keep the provider hostname, and do not send the bearer token on those hosts. The proxy replaces Authorization."

// AgentIndex is the document an agent fetches to onboard this console.
type AgentIndex struct {
	Kind         string             `json:"kind"`
	Summary      string             `json:"summary"`
	URL          string             `json:"url"`
	Proxy        bool               `json:"proxy"`
	PublicURL    string             `json:"publicUrl,omitempty"`
	Resources    []AgentResource    `json:"resources"`
	Environments []AgentEnvironment `json:"environments"`
	Sandboxes    []AgentSandbox     `json:"sandboxes"`
}

// AgentResource is one provider and the scenarios it can load.
type AgentResource struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ProviderHosts []string        `json:"providerHosts"`
	Scenarios     []AgentScenario `json:"scenarios"`
}

// AgentScenario is a scenario id and the config names a single-service sandbox would use.
type AgentScenario struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	StoreAs     string   `json:"storeAs"`
	URLName     string   `json:"urlName"`
	SecretName  string   `json:"secretName"`
	Running     []string `json:"running"`
}

// AgentEnvironment is an official environment and the names its services store under.
type AgentEnvironment struct {
	Name     string             `json:"name"`
	Label    string             `json:"label"`
	Services []AgentServiceName `json:"services"`
}

// AgentServiceName is the config identity of one service before it is running.
type AgentServiceName struct {
	Name          string   `json:"name"`
	Resource      string   `json:"resource"`
	DisplayName   string   `json:"displayName"`
	Scenario      string   `json:"scenario"`
	ProviderHosts []string `json:"providerHosts"`
	StoreAs       string   `json:"storeAs"`
	URLName       string   `json:"urlName"`
	SecretName    string   `json:"secretName"`
	Running       []string `json:"running"`
}

// AgentSandbox is a running sandbox and the secrets an app should store for it.
type AgentSandbox struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Label    string             `json:"label"`
	Kind     string             `json:"kind"`
	Status   string             `json:"status"`
	Proxy    bool               `json:"proxy"`
	Anchored bool               `json:"anchored"`
	Services []AgentLiveService `json:"services"`
	Secrets  []AgentSecret      `json:"secrets"`
}

// AgentLiveService is one running service with the values to store.
type AgentLiveService struct {
	Name          string   `json:"name"`
	Resource      string   `json:"resource"`
	DisplayName   string   `json:"displayName"`
	Scenario      string   `json:"scenario"`
	ProviderHosts []string `json:"providerHosts"`
	StoreAs       string   `json:"storeAs"`
	URLName       string   `json:"urlName"`
	URL           string   `json:"url"`
	SecretName    string   `json:"secretName"`
	Secret        string   `json:"secret"`
}

// AgentSecret is one value to write into the app's secret store.
type AgentSecret struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Purpose string `json:"purpose"`
}

func buildAgentIndex(deployment Deployment, catalog Catalog, sessions []Session, indexURL string) AgentIndex {
	resources := make([]AgentResource, 0)
	resourceIndex := map[string]int{}
	for _, scenario := range catalog.Scenarios {
		position, ok := resourceIndex[scenario.Resource]
		if !ok {
			position = len(resources)
			resourceIndex[scenario.Resource] = position
			hosts := scenario.ProviderHosts
			if hosts == nil {
				hosts = []string{}
			}
			resources = append(resources, AgentResource{
				ID:            scenario.Resource,
				Name:          scenario.DisplayName,
				ProviderHosts: hosts,
				Scenarios:     []AgentScenario{},
			})
		}
		prefix := variablePrefix(scenario.Resource)
		resources[position].Scenarios = append(resources[position].Scenarios, AgentScenario{
			ID:          scenario.ID,
			Name:        scenario.Name,
			Description: scenario.Description,
			Version:     scenario.ResourceVersion,
			StoreAs:     scenario.Resource,
			URLName:     prefix + "_URL",
			SecretName:  prefix + "_TOKEN",
			Running:     sandboxIDsForScenario(sessions, scenario.Resource, scenario.ID),
		})
	}

	environments := make([]AgentEnvironment, 0, len(catalog.Environments))
	for _, environment := range catalog.Environments {
		services := make([]AgentServiceName, 0, len(environment.Services))
		for _, service := range environment.Services {
			prefix := variablePrefix(service.Name)
			hosts := service.ProviderHosts
			if hosts == nil {
				hosts = []string{}
			}
			services = append(services, AgentServiceName{
				Name:          service.Name,
				Resource:      service.Resource,
				DisplayName:   service.DisplayName,
				Scenario:      service.Scenario,
				ProviderHosts: hosts,
				StoreAs:       service.Name,
				URLName:       prefix + "_URL",
				SecretName:    prefix + "_TOKEN",
				Running:       sandboxIDsForEnvironmentService(sessions, environment.Name, service.Name),
			})
		}
		environments = append(environments, AgentEnvironment{
			Name:     environment.Name,
			Label:    environment.Label,
			Services: services,
		})
	}

	sandboxes := make([]AgentSandbox, 0, len(sessions))
	for _, session := range sessions {
		sandboxes = append(sandboxes, sandboxIndex(session))
	}

	return AgentIndex{
		Kind:         "fabricate.agent-index",
		Summary:      agentIndexSummary,
		URL:          indexURL,
		Proxy:        deployment.Proxy,
		PublicURL:    deployment.PublicURL,
		Resources:    resources,
		Environments: environments,
		Sandboxes:    sandboxes,
	}
}

func sandboxIndex(session Session) AgentSandbox {
	services := make([]AgentLiveService, 0, len(session.Services))
	secrets := make([]AgentSecret, 0, len(session.Variables))
	seen := map[string]bool{}
	if value, ok := variableValue(session, "FAB_ENVIRONMENT"); ok {
		secrets = append(secrets, AgentSecret{Name: "FAB_ENVIRONMENT", Value: value, Purpose: "name"})
		seen["FAB_ENVIRONMENT"] = true
	}
	for _, service := range session.Services {
		hosts := service.ProviderHosts
		if hosts == nil {
			hosts = []string{}
		}
		services = append(services, AgentLiveService{
			Name:          service.Name,
			Resource:      service.Resource,
			DisplayName:   service.DisplayName,
			Scenario:      service.Scenario,
			ProviderHosts: hosts,
			StoreAs:       service.Name,
			URLName:       service.URLVariable,
			URL:           service.URL,
			SecretName:    service.TokenVariable,
			Secret:        service.Token,
		})
		if service.URLVariable != "" {
			secrets = append(secrets, AgentSecret{Name: service.URLVariable, Value: service.URL, Purpose: "url"})
			seen[service.URLVariable] = true
		}
		if service.TokenVariable != "" {
			secrets = append(secrets, AgentSecret{Name: service.TokenVariable, Value: service.Token, Purpose: "bearer"})
			seen[service.TokenVariable] = true
		}
	}
	for _, variable := range session.Variables {
		if seen[variable.Name] {
			continue
		}
		purpose := secretPurpose(variable.Name)
		if purpose == "" {
			continue
		}
		secrets = append(secrets, AgentSecret{Name: variable.Name, Value: variable.Value, Purpose: purpose})
	}
	return AgentSandbox{
		ID:       session.ID,
		Name:     session.Name,
		Label:    session.Label,
		Kind:     session.Kind,
		Status:   session.Status,
		Proxy:    session.Proxy,
		Anchored: session.Anchored,
		Services: services,
		Secrets:  secrets,
	}
}

func variableValue(session Session, name string) (string, bool) {
	for _, variable := range session.Variables {
		if variable.Name == name {
			return variable.Value, true
		}
	}
	return "", false
}

func secretPurpose(name string) string {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy":
		return "proxy"
	case "SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO":
		return "ca"
	default:
		return ""
	}
}

func sandboxIDsForScenario(sessions []Session, resource, scenario string) []string {
	ids := []string{}
	for _, session := range sessions {
		for _, service := range session.Services {
			if service.Resource == resource && service.Scenario == scenario {
				ids = append(ids, session.ID)
				break
			}
		}
	}
	return ids
}

func sandboxIDsForEnvironmentService(sessions []Session, environmentName, serviceName string) []string {
	ids := []string{}
	for _, session := range sessions {
		if session.Kind != "environment" || session.Name != environmentName {
			continue
		}
		for _, service := range session.Services {
			if service.Name == serviceName {
				ids = append(ids, session.ID)
				break
			}
		}
	}
	return ids
}

func joinIndexURL(base string) string {
	return strings.TrimRight(base, "/") + agentIndexPath
}
