package mailgun

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/mailgun/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

type Resource struct{}

func NewResource() *Resource { return &Resource{} }

func (*Resource) Descriptor() httpresource.Descriptor {
	spec, err := generated.GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("mailgun: embedded OpenAPI: %v", err))
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("mailgun: marshal embedded OpenAPI: %v", err))
	}
	return httpresource.Descriptor{
		ID: "mailgun", DisplayName: "Mailgun", Version: "v3", OpenAPIVersion: "3.0.3",
		OpenAPIDigest: httpresourceDigest(raw), ScenarioVersion: 1,
		ProviderHosts: []string{"api.mailgun.net"},
		SDK:           httpresource.SDKDescriptor{Package: "curl", Language: "http", DirectTest: true, ProxyTest: true},
	}
}

func (*Resource) Contract() httpresource.Contract {
	spec, err := generated.GetSwagger()
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(err)
	}
	return httpresource.Contract{OpenAPIJSON: raw, ScenarioJSON: append([]byte(nil), scenarioSchema...)}
}

func (*Resource) Scenarios() httpresource.ScenarioCodec { return scenarioCodec{} }

func (*Resource) ScenarioIDs() ([]string, error) { return scenario.EmbeddedIDs(builtInScenarios) }

func (*Resource) Scenario(id string) (scenario.Document, error) {
	entries, err := builtInScenarios.ReadDir("scenarios")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("mailgun: list embedded scenarios: %w", err)
	}
	for _, entry := range entries {
		raw, err := builtInScenarios.ReadFile("scenarios/" + entry.Name())
		if err != nil {
			return scenario.Document{}, fmt.Errorf("mailgun: read embedded scenario %s: %w", entry.Name(), err)
		}
		doc, err := scenario.Parse(raw)
		if err != nil {
			return scenario.Document{}, fmt.Errorf("mailgun: parse embedded scenario %s: %w", entry.Name(), err)
		}
		if doc.ID == id {
			return doc, nil
		}
	}
	return scenario.Document{}, fmt.Errorf("mailgun: unknown scenario %q", id)
}

func (*Resource) NewServer(ctx context.Context, dependencies httpresource.ServerDependencies) (httpresource.Server, error) {
	return newServer(ctx, dependencies)
}

func httpresourceDigest(raw []byte) string { return scenarioDigest(raw) }
