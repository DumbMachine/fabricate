# Authoring profiles

A profile is a directory: `profile.yaml` plus the seed files it
references. The CLI carries the whole loop:

```bash
fab profiles schema                 # commented schema reference
fab engines -o json                 # per-engine seed types + defaults
fab profiles show stripe-payments --files            # a working example's file manifest
fab profiles show stripe-payments --file 00-schema.sql   # one file, head-capped (--head N)
fab profiles init postgres my-app                    # blank scaffold, or:
fab profiles init postgres my-app --from stripe-payments # copy the example and edit
```

`show --file` is deliberately bounded (first 100 lines by default) so
reading examples stays cheap for agents; raise with `--head N` or
`--head 0`.

## profile.yaml

```yaml
name: my-app
engine: postgres              # postgres | mysql | mongodb | redis | prometheus |
                              # ssh | kubernetes | github | aws_console | httpmock
label: "My app — realistic OLTP fixture"
description: |
  Multiline blurb shown by `fab profiles show`.
image: postgres:16-alpine     # optional; override at create time with --image
defaults:
  database: myapp             # engine-specific: db name (sql), authSource (mongo), db index (redis)
  username: postgres          # ignored by prometheus; engine default for ssh
  password: ""                # blank → fab generates one per instance (preferred)
env:
  MOCK_SERVICE: linear        # httpmock: which mockd service; ssh: extra env
healthcheck:
  timeout: 180s               # raise for large seeds
seed:
  - { type: sql,          file: 00-schema.sql }   # postgres, mysql (in order)
  - { type: sql,          file: 10-data.sql }
  - { type: js,           file: 00-seed.js }      # mongodb
  - { type: mongoimport,  file: 00-import.sh }    # mongodb (wrapper shell)
  - { type: redis-cli,    file: 00-keys.redis }   # redis (one command per line)
  - { type: prom-config,  file: prometheus.yml }  # prometheus (optional)
  - { type: prom-rule,    file: api.rules.yml }   # prometheus (any number)
  - { type: shell,        file: 00-bootstrap.sh } # ssh (runs as root)
  - { type: mock-fixture, file: seed.json }       # httpmock (exactly one)
  - { type: moto-seed,    file: seed.yaml }       # aws_console
extensions: [pg_trgm]         # postgres only: CREATE EXTENSION IF NOT EXISTS
tags: [demo, oltp]
```

Seed files live next to `profile.yaml` and run in array order. Paths
must stay inside the profile directory (no `..`, no absolute paths).

## Where profiles live

- **User dir** — `~/.config/fab/profiles/<engine>/<name>/` (override
  with `$FAB_PROFILES_DIR`). Shadows built-ins with the same name;
  the fastest iteration loop while authoring.
- **Built-in catalog** — `profiles/` in this repo, embedded into the
  binary. Add the top-level directory to `profiles/embed.go`'s
  `//go:embed` line when you introduce a new engine/service dir.
- **Registered catalogs** — private packs shipped by downstream
  binaries; see [private-catalogs.md](private-catalogs.md).
- **Template packs** — any GitHub repo or local dir with the
  `<slug>/<name>/profile.yaml` layout installs into the user dir via
  `fab profiles add <owner>/<repo>[#ref] [--list|--profile <n>|--all]`
  (degit-style tarball; `GITHUB_TOKEN` for private repos).

The `-p` argument to `fab create <slug> -p <name>` is resolved by
directory: `<slug>/<name>/profile.yaml`. For httpmock services the
convention is a directory per service (`profiles/linear/…`,
`profiles/gmail/…`) with `engine: httpmock` inside.

## What makes a good seed profile

The built-ins follow a pattern worth copying: **state plus a story**.
Don't just insert rows — plant something to find or do:

- `stripe-payments` hides a refund missing from the ledger and intents
  stuck in `requires_action`.
- `orders-slow` ships a missing index so `EXPLAIN` tells a story.
- `support-inbox` (gmail) seeds an angry follow-up that's begging for a
  reply, plus newsletters as noise.
- `sprint-board` (linear) has an urgent, unassigned production bug.

Describe the story (and how to verify it) in `description:` — it's what
`fab profiles show` prints, and it's how a future user (or agent) knows
what the fixture is *for*. Keep fixtures deterministic: fixed IDs,
fixed timestamps, no randomness at seed time.

## httpmock fixtures

Each mockd service documents its fixture shape in its package comment
(`mockd/services/<name>/<name>.go`). The fixture is a single JSON file
declared as the profile's one `mock-fixture` seed step and loaded into
SQLite at container boot, so every instance starts from the same state
and diverges as your code writes to it.
