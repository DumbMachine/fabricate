package neon

import (
	"embed"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/httpresource/specserver"
)

//go:embed openapi.json
var openAPI []byte

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

func NewResource() httpresource.Resource {
	return specserver.NewBound("neon", "Neon", []string{"console.neon.tech"}, openAPI, scenarioSchema, builtInScenarios, nil)
}
