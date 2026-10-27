package x

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/internal/socialstate"
	"github.com/dumbmachine/fabricate/resources/x/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

//go:embed scenarios/*.json
var documents embed.FS

//go:embed scenario.schema.json
var stateSchema []byte

type Resource struct{}

func NewResource() *Resource { return &Resource{} }
func (*Resource) Contract() httpresource.Contract {
	spec, err := generated.GetSpec()
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(err)
	}
	return httpresource.Contract{OpenAPIJSON: raw, ScenarioJSON: stateSchema}
}
func (r *Resource) Descriptor() httpresource.Descriptor {
	return httpresource.Descriptor{ID: "x", DisplayName: "X", Version: "v1", OpenAPIVersion: "3.0.3", OpenAPIDigest: scenario.Digest(r.Contract().OpenAPIJSON), ScenarioVersion: 1, ProviderHosts: []string{"api.x.com"}, SDK: httpresource.SDKDescriptor{Package: "curl", Language: "http", DirectTest: true, ProxyTest: true}}
}
func (*Resource) Scenarios() httpresource.ScenarioCodec {
	return socialstate.Codec{Resource: "x", Schema: stateSchema}
}
func (*Resource) ScenarioDocuments() ([]scenario.Document, error) {
	return scenario.Embedded(documents)
}
func (*Resource) Scenario(id string) (scenario.Document, error) {
	return scenario.LookupEmbedded(documents, id, "x")
}
func (*Resource) NewServer(ctx context.Context, d httpresource.ServerDependencies) (httpresource.Server, error) {
	if d.DB == nil || d.Secrets == nil {
		return nil, fmt.Errorf("missing dependencies")
	}
	return newServer(d)
}
