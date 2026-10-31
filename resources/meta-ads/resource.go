package metaads

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/httpresource/specserver"
	"github.com/dumbmachine/fabricate/resources/meta-ads/generated"
	"github.com/dumbmachine/fabricate/scenario"
)

//go:embed scenarios/*.json
var documents embed.FS

//go:embed scenario.schema.json
var stateSchema []byte

type Resource struct{}

func NewResource() *Resource { return &Resource{} }
func (r *Resource) Contract() httpresource.Contract {
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
	return httpresource.Descriptor{ID: "meta-ads", DisplayName: "Meta Ads", Version: "v1", OpenAPIVersion: "3.0.3", OpenAPIDigest: scenario.Digest(r.Contract().OpenAPIJSON), ScenarioVersion: 1, ProviderHosts: []string{"graph.facebook.com"}, SDK: httpresource.SDKDescriptor{Package: "facebook-business", Language: "python", DirectTest: true, ProxyTest: true}}
}
func (r *Resource) Scenarios() httpresource.ScenarioCodec {
	return codec{specserver.NewCodec("meta-ads", stateSchema)}
}
func (r *Resource) ScenarioDocuments() ([]scenario.Document, error) {
	return scenario.Embedded(documents)
}
func (r *Resource) Scenario(id string) (scenario.Document, error) {
	return scenario.LookupEmbedded(documents, id, "meta-ads")
}
func (r *Resource) NewServer(ctx context.Context, d httpresource.ServerDependencies) (httpresource.Server, error) {
	return newServer(ctx, d)
}

type codec struct{ httpresource.ScenarioCodec }

func (c codec) Validate(ctx context.Context, doc scenario.Document) error {
	if err := c.ScenarioCodec.Validate(ctx, doc); err != nil {
		return err
	}
	var state map[string][]map[string]any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		return err
	}
	byCollection := map[string]map[string]map[string]any{}
	for kind, rows := range state {
		seen := map[string]map[string]any{}
		for _, row := range rows {
			id, _ := row["id"].(string)
			if id == "" || seen[id] != nil {
				return fmt.Errorf("duplicate or absent %s ID", kind)
			}
			seen[id] = row
		}
		byCollection[kind] = seen
	}
	for _, kind := range []string{"campaigns", "adsets", "insights"} {
		for _, row := range state[kind] {
			account, _ := row["account_id"].(string)
			if byCollection["accounts"][account] == nil {
				return fmt.Errorf("unknown account in %s", kind)
			}
			if kind != "campaigns" {
				campaign := byCollection["campaigns"][row["campaign_id"].(string)]
				if campaign == nil || campaign["account_id"] != account {
					return fmt.Errorf("invalid campaign reference")
				}
			}
			if kind == "insights" {
				adset := byCollection["adsets"][row["adset_id"].(string)]
				if adset == nil || adset["campaign_id"] != row["campaign_id"] {
					return fmt.Errorf("invalid ad-set reference")
				}
			}
		}
	}
	return nil
}

func (c codec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	doc, err := c.ScenarioCodec.Dump(ctx, db, metadata)
	if err != nil {
		return doc, err
	}
	var state map[string]json.RawMessage
	if err = json.Unmarshal(doc.State, &state); err != nil {
		return doc, err
	}
	for _, kind := range []string{"accounts", "campaigns", "adsets", "insights"} {
		if state[kind] == nil {
			state[kind] = json.RawMessage(`[]`)
		}
	}
	doc.State, err = json.Marshal(state)
	return doc, err
}
