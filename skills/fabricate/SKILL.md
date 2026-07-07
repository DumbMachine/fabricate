---
name: fabricate
description: Spin up real, seeded, throwaway test resources with the fab CLI — Postgres/MySQL/MongoDB/Redis/Prometheus/SSH/Kubernetes containers and stateful HTTP-API mocks (Linear, Gmail, Google Play, ...) where writes actually take effect. Use when the user needs a database with realistic data, a mock third-party API to develop or test against, or wants to author/install fabricate profiles.
allowed-tools: Bash(fab:*)
---

# fabricate (`fab`)

`fab` provisions ephemeral, seeded resources from declarative profiles
and prints connection credentials. Two kinds:

- **Container engines**: postgres, mysql, mongodb, redis, prometheus,
  ssh, kubernetes (k3s), aws_console (Moto), github (emulate).
- **Stateful HTTP-API mocks** (httpmock engine): linear, gmail,
  google-play, vercel, cloudflare, supabase, render, fly, railway.
  SQLite-backed — POST/PATCH mutate state and later GETs reflect it.

Requirements: a running Docker daemon. Mock profiles additionally need
the `fabricate/httpmock:local` image (`make images` in the repo, or
set `FAB_HTTPMOCK_IMAGE` to a published copy).

## Core loop

```bash
fab profiles                       # every (slug, profile) pair
fab create <slug> -p <profile>     # provision; prints creds
fab creds <name> --env             # re-print creds as shell exports
fab wait <name>                    # block until the port listens (CI)
fab ls                             # live instances
fab destroy <name>                 # stop + remove (--all nukes everything)
```

Every command takes `-o {json,table,env,url}`. Output is JSON by
default on a pipe, so parse `-o json` rather than scraping tables.
Informational text goes to stderr; stdout is the payload.

```bash
fab create postgres -p stripe-payments -o json   # -> {"creds":{"url":...}}
eval "$(fab creds stripe-payments --env)"        # DATABASE_URL, FAB_URL, FAB_PASSWORD, ...
```

Instance names default to the profile name; pass `--name` to run two
copies. State lives in `~/.config/fab/state.json` (override:
`FAB_STATE_FILE`) — creds are always recoverable via `fab creds` until
you destroy.

## Discovering and reading profiles

```bash
fab engines -o json                        # engines + accepted seed types
fab profiles show <name>                   # metadata + the exact create command
fab profiles show <name> --files           # seed-file manifest (names + sizes only)
fab profiles show <name> --file seed.json  # ONE file, first 100 lines (--head N, 0 = all)
```

Read seed files with `--file` (bounded), never assume a fixture's
shape — each mock service documents its shape in its skill and in
`mockd/services/<name>` package docs.

## Authoring profiles

```bash
fab profiles schema                              # commented profile.yaml reference
fab profiles init <slug> <name>                  # blank scaffold in ~/.config/fab/profiles/
fab profiles init linear my-board --from sprint-board   # copy a working example instead
fab create linear -p my-board                    # user profiles work immediately
```

For mock services the slug is the service (linear, gmail, ...); the
scaffold pre-fills `engine: httpmock` + `env.MOCK_SERVICE`. Fill
`seed.json` (learn the shape from the example profile), then create.

## Template packs

Install profiles from any GitHub repo or directory laid out
`<slug>/<name>/profile.yaml` (optionally under `profiles/`):

```bash
fab profiles add owner/repo --list            # discover (never prompts)
fab profiles add owner/repo --profile <name>  # or --all; #ref pins a tag
fab profiles add ./local-pack --all           # local dirs work too
```

`GITHUB_TOKEN` enables private repos. Multi-profile packs without a
selection exit nonzero and print the exact flags to pass.

## Gotchas

- Containers outlive the CLI on purpose; clean up with `fab destroy`.
- `instance "X" already exists` → `fab destroy X` or pass `--name`.
- First create of an image pulls it (progress on stderr); slow pulls
  are not hangs. `--timeout 10m` raises the provisioning budget.
- Mock bearer tokens are not validated; send `FAB_PASSWORD` if the
  client insists on auth.
