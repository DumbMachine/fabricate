// Package all is the single compile-time registry for official HTTP
// resources. The CLI, engine, and supervisor must receive this same object.
package all

import (
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/asana"
	"github.com/dumbmachine/fabricate/resources/gmail"
	"github.com/dumbmachine/fabricate/resources/hubspot"
)

func Registry() *httpresource.Registry {
	registry, err := httpresource.NewRegistry(asana.NewResource(), gmail.NewResource(), hubspot.NewResource())
	if err != nil {
		panic(fmt.Sprintf("official HTTP resource registry: %v", err))
	}
	return registry
}
