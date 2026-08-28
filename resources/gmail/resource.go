package gmail

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/gmail/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

type Resource struct{}

func NewResource() *Resource { return &Resource{} }

func (*Resource) Descriptor() httpresource.Descriptor {
	spec, err := generated.GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("gmail: embedded OpenAPI: %v", err))
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("gmail: marshal embedded OpenAPI: %v", err))
	}
	return httpresource.Descriptor{
		ID: "gmail", DisplayName: "Gmail", Version: "v1", OpenAPIVersion: "3.0.3",
		OpenAPIDigest: httpresourceDigest(raw), ScenarioVersion: 1,
		ProviderHosts: []string{"gmail.googleapis.com", "www.googleapis.com"},
		SDK:           httpresource.SDKDescriptor{Package: "googleapis", Language: "javascript", DirectTest: true},
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
		return scenario.Document{}, fmt.Errorf("gmail: list embedded scenarios: %w", err)
	}
	for _, entry := range entries {
		raw, err := builtInScenarios.ReadFile("scenarios/" + entry.Name())
		if err != nil {
			return scenario.Document{}, fmt.Errorf("gmail: read embedded scenario %s: %w", entry.Name(), err)
		}
		doc, err := scenario.Parse(raw)
		if err != nil {
			return scenario.Document{}, fmt.Errorf("gmail: parse embedded scenario %s: %w", entry.Name(), err)
		}
		if doc.ID == id {
			return doc, nil
		}
	}
	return scenario.Document{}, fmt.Errorf("gmail: unknown scenario %q", id)
}

func (*Resource) NewServer(ctx context.Context, dependencies httpresource.ServerDependencies) (httpresource.Server, error) {
	return newServer(ctx, dependencies)
}

func httpresourceDigest(raw []byte) string {
	return scenarioDigest(raw)
}
