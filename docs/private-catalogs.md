# Private profile catalogs

The built-in catalog is generic on purpose. Organizations usually have
profiles that shouldn't be public — fixtures shaped like internal
schemas, seeded with product-specific scenarios. There are three ways
to ship them, cheapest first.

## Option 0: a template-pack repo (`fab profiles add`)

Keep profiles in any repo laid out like the catalog
(`<slug>/<name>/profile.yaml`, optionally under `profiles/`) and
install from it directly — private repos work with `GITHUB_TOKEN`:

```bash
fab profiles add acme/fab-profiles --all
fab profiles add acme/fab-profiles#v1.2.0 --profile checkout-db
```

This is a copy at install time (degit-style): update by re-running
with `--force`. Right when profiles change often and you don't want a
custom binary.

## Option A: a profiles directory (no code)

Point `$FAB_PROFILES_DIR` (or drop into `~/.config/fab/profiles`) at a
directory in your private repo:

```
your-repo/fab-profiles/
└── postgres/
    └── acme-core/
        ├── profile.yaml
        └── 00-schema.sql
```

```bash
export FAB_PROFILES_DIR=$HOME/src/your-repo/fab-profiles
fab profiles          # acme-core shows up, source=user
```

Good for teams already sharing a repo checkout; nothing to build.

## Option B: a wrapper binary (catalog as code)

Embed your private profiles in a tiny `main` that wraps the public
CLI. You get a single distributable binary with both catalogs inside:

```go
// yourrepo/cmd/fab/main.go
package main

import (
    "embed"
    "io/fs"

    "github.com/dumbmachine/fabricate/cli"
    "github.com/dumbmachine/fabricate/profile"

    // Side-effect import: registers the public built-in catalog.
    _ "github.com/dumbmachine/fabricate/profiles"
)

//go:embed all:profiles
var private embed.FS

func main() {
    sub, err := fs.Sub(private, "profiles")
    if err != nil {
        panic(err)
    }
    // Registered after the built-ins → your profiles win name clashes.
    profile.RegisterCatalog("acme", sub)
    cli.Execute()
}
```

`fab profiles` labels each entry with its source (`acme`, `builtin`,
`user`), and precedence is: user dir → newest registered catalog →
older catalogs → built-ins.

Private catalogs can target any public engine, including `httpmock` —
a private profile is just `env.MOCK_SERVICE` plus your own fixture, so
you can ship org-specific inboxes/boards for the public gmail/linear
services without forking anything. Truly private mock *services* (not
just fixtures) currently require building your own mockd image; if you
need that, open an issue — a service-plugin registry is a natural next
step.

## Migrating a vendored copy (the dq playbook)

If you vendored this code before it was split out (e.g. as
`apps/fab/`), the migration to the public module is mechanical:

1. **Move private profiles out** of the vendored `profiles/` into your
   own directory (Option A) or wrapper binary (Option B). Private ==
   anything schema- or product-specific; everything generic already
   ships here.
2. **Create the wrapper binary** (Option B above) in your repo, with
   `go.mod` requiring `github.com/dumbmachine/fabricate`.
3. **Repoint your Makefile** `fab` target at the wrapper
   (`go build -o ~/bin/fab ./cmd/fab`) and keep an `images` target
   that builds `fabricate/httpmock:local` / `fabricate/emulate:local`
   from the module cache, e.g.
   `docker build -t fabricate/httpmock:local $(go list -m -f '{{.Dir}}' github.com/dumbmachine/fabricate)/mockd`.
4. **Delete the vendored tree.** State files are compatible
   (`~/.config/fab/state.json` is versioned JSON and the schema is
   unchanged), so live instances survive the swap.

The public surface your wrapper relies on is deliberately small:
`cli.Execute`, `profile.RegisterCatalog`, and the `profiles`
side-effect import. Everything else can keep evolving without breaking
downstream wrappers.
