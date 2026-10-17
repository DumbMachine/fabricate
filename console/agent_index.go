package console

import "strings"

const agentIndexPath = "/.well-known/fabricate"

const agentIndexSummary = "Resources and scenarios this Fabricate console can start, plus the sandboxes that are running. " +
	"control.start creates a sandbox. control.list and control.read return the running ones. " +
	"Call a service by appending the provider path to services[].url. When proxy is false, send Authorization: Bearer services[].secret. " +
	"When proxy is true, store the proxy and ca secrets, keep the provider hostname, and do not send the bearer token. The proxy replaces Authorization. " +
	"Each sandbox logs URL, and control.logs, returns the redacted request trace while that sandbox is running. " +
	"control.close stops a sandbox this console started. " +
	"storeAs is the service name to keep in app config. urlName is the config key for the base URL, and secretName is the config key for the bearer token. " +
	"When a sandbox is running, services[].url and services[].secret are the values to store under those names. " +
	"scenarios[].running and environments[].services[].running are the sandbox ids using that scenario."

// AgentIndex is the document an agent fetches to onboard this console.
type AgentIndex struct {
	Kind         string             `json:"kind"`
	Summary      string             `json:"summary"`
	URL          string             `json:"url"`
	Proxy        bool               `json:"proxy"`
	PublicURL    string             `json:"publicUrl,omitempty"`
	Control      AgentControl       `json:"control"`
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
	Logs     string             `json:"logs"`
	Services []AgentLiveService `json:"services"`
	Secrets  []AgentSecret      `json:"secrets"`
}

// AgentControl is the console API an agent uses to start, call, and inspect sandboxes.
type AgentControl struct {
	Start AgentEndpoint `json:"start"`
	List  AgentEndpoint `json:"list"`
	Read  AgentEndpoint `json:"read"`
	Logs  AgentEndpoint `json:"logs"`
	Close AgentEndpoint `json:"close"`
}

// AgentEndpoint is one console request. URL may contain {id} for the sandbox id.
type AgentEndpoint struct {
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   *AgentStartBody `json:"body,omitempty"`
	Note   string          `json:"note"`
}

// AgentStartBody is the JSON body for control.start.
// Kind is "scenario" or "environment". ID is the scenario id or the environment name.
type AgentStartBody struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
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

	base := strings.TrimSuffix(strings.TrimRight(indexURL, "/"), agentIndexPath)
	sandboxes := make([]AgentSandbox, 0, len(sessions))
	for _, session := range sessions {
		sandboxes = append(sandboxes, sandboxIndex(session, base))
	}

	return AgentIndex{
		Kind:         "fabricate.agent-index",
		Summary:      agentIndexSummary,
		URL:          indexURL,
		Proxy:        deployment.Proxy,
		PublicURL:    deployment.PublicURL,
		Control:      agentControl(base),
		Resources:    resources,
		Environments: environments,
		Sandboxes:    sandboxes,
	}
}

func agentControl(base string) AgentControl {
	sessions := strings.TrimRight(base, "/") + "/api/sessions"
	return AgentControl{
		Start: AgentEndpoint{
			Method: "POST",
			URL:    sessions,
			Body:   &AgentStartBody{Kind: "scenario", ID: "<scenario id>"},
			Note:   `kind is "scenario" or "environment". id is a scenario id or an environment name. The response is the sandbox, including each service url and secret.`,
		},
		List: AgentEndpoint{
			Method: "GET",
			URL:    sessions,
			Note:   "Sandboxes that are running.",
		},
		Read: AgentEndpoint{
			Method: "GET",
			URL:    sessions + "/{id}",
			Note:   "One sandbox, including service urls and secrets. {id} is the sandbox id.",
		},
		Logs: AgentEndpoint{
			Method: "GET",
			URL:    sessions + "/{id}/logs",
			Note:   "Redacted request trace while the sandbox is running. truncated is true when older entries were omitted. Authorization, cookies, and secret-shaped fields are [REDACTED].",
		},
		Close: AgentEndpoint{
			Method: "DELETE",
			URL:    sessions + "/{id}",
			Note:   "Stops a sandbox this console started. 409 when fab run is holding it. Read the log first; it is not available after close.",
		},
	}
}

func sandboxIndex(session Session, base string) AgentSandbox {
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
		Logs:     strings.TrimRight(base, "/") + "/api/sessions/" + session.ID + "/logs",
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
