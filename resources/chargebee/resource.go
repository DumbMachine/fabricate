package chargebee

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/chargebee/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

type Resource struct{}

func NewResource() *Resource { return &Resource{} }

func (*Resource) Descriptor() httpresource.Descriptor {
	spec, err := generated.GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("chargebee: embedded OpenAPI: %v", err))
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("chargebee: marshal embedded OpenAPI: %v", err))
	}
	return httpresource.Descriptor{
		ID: "chargebee", DisplayName: "Chargebee", Version: "v2", OpenAPIVersion: "3.0.3",
		OpenAPIDigest: httpresourceDigest(raw), ScenarioVersion: 1,
		ProviderHosts: []string{"acme.chargebee.com"},
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

func (*Resource) ScenarioDocuments() ([]scenario.Document, error) {
	return scenario.Embedded(builtInScenarios)
}

func (*Resource) Scenario(id string) (scenario.Document, error) {
	return scenario.LookupEmbedded(builtInScenarios, id, "chargebee")
}

func (*Resource) NewServer(ctx context.Context, dependencies httpresource.ServerDependencies) (httpresource.Server, error) {
	return newServer(ctx, dependencies)
}

func httpresourceDigest(raw []byte) string {
	return scenarioDigest(raw)
}
