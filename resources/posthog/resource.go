package posthog

import (
	"embed"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/httpresource/specserver"
)

//go:embed openapi.yaml
var openAPI []byte

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

func NewResource() httpresource.Resource {
	return specserver.NewBound("posthog", "PostHog", []string{"us.posthog.com"}, openAPI, scenarioSchema, builtInScenarios, nil)
}
