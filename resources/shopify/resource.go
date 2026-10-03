package shopify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/shopify/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

type Resource struct{}

func NewResource() *Resource { return &Resource{} }

func (*Resource) Descriptor() httpresource.Descriptor {
	spec, err := generated.GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("shopify: embedded OpenAPI: %v", err))
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("shopify: marshal embedded OpenAPI: %v", err))
	}
	return httpresource.Descriptor{
		ID: "shopify", DisplayName: "Shopify", Version: "2024-10", OpenAPIVersion: "3.0.3",
		OpenAPIDigest: httpresourceDigest(raw), ScenarioVersion: 1,
		ProviderHosts: []string{
			"acme-goods.myshopify.com",
			"northwind-market.myshopify.com",
			"tinyshop.myshopify.com",
			"fernworks-studio.myshopify.com",
		},
		SDK: httpresource.SDKDescriptor{Package: "curl", Language: "http", DirectTest: true, ProxyTest: true},
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
	return scenario.LookupEmbedded(builtInScenarios, id, "shopify")
}

func (*Resource) NewServer(ctx context.Context, dependencies httpresource.ServerDependencies) (httpresource.Server, error) {
	return newServer(ctx, dependencies)
}

func httpresourceDigest(raw []byte) string {
	return scenarioDigest(raw)
}
