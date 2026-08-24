# Authoring infrastructure profiles

Profiles describe seeded container/infrastructure engines. Provider HTTP APIs
use compiled resources and environment manifests instead.

Create a starter from any registered engine:

```bash
fab profiles schema
fab engines
fab profiles init postgres my-db
fab profiles init redis my-cache
```

A profile directory contains `profile.yaml` and files referenced by its ordered
`seed` steps. The directory lives at `<catalog>/<engine>/<name>/`.

```yaml
name: my-db
engine: postgres
image: postgres:16-alpine
defaults:
  database: app
  username: app
  password: ""
healthcheck:
  timeout: 180s
seed:
  - type: sql
    file: 00-schema.sql
tags: [local]
```

Use `fab engines` for the accepted seed types. Blank passwords are generated.
Seed paths must remain relative to the profile directory. A user profile under
`~/.config/fab/profiles` shadows a built-in with the same engine/name.

Use `fab profiles show <name> --files` and `--file <name>` to inspect bounded
examples. `fab profiles init <engine> <name> --from <example>` copies a working
profile while preserving comments and seed files.
