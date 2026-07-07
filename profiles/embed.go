// Package profiles embeds the built-in profile catalog and wires
// it into the loader at init time. Lives in its own package so the
// embed.FS root sits at the catalog root — embed paths can't
// escape the package directory, so this can't move into the
// profile package itself.
package profiles

import (
	"embed"
	"io/fs"

	"github.com/dumbmachine/fabricate/profile"
)

// Built-in catalog spans every engine fab knows how to spin up.
// Add a new engine? Mirror the directory layout
// (profiles/<engine>/<name>/profile.yaml) and extend the embed
// directive — the loader picks engines up from the FS shape, no
// further wiring required.
//
//go:embed all:postgres all:mysql all:mongodb all:redis all:prometheus all:ssh all:kubernetes all:github all:aws_console all:google-play all:gmail all:linear
var builtin embed.FS

func init() {
	sub, err := fs.Sub(builtin, ".")
	if err != nil {
		panic("fabricate/profiles: embed sub failed: " + err.Error())
	}
	profile.RegisterCatalog("builtin", sub)
}
