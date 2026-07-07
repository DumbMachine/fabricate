# Architecture

`fab` is a small pipeline: **profile → engine → target → creds**.

```
profile.yaml ──▶ profile.Load ──▶ engine.Create ──▶ Instance{Creds} ──▶ state.json
   (what)          (loader)      (how: container)     (connection)      (lifecycle)
```

## Profiles (what to build)

A profile is a directory — `profile.yaml` plus seed files — describing
one fixture: which engine, which image, what state to seed. Profiles
come from a stack of sources, highest precedence first:

1. the user dir (`~/.config/fab/profiles`, or `$FAB_PROFILES_DIR`)
2. catalogs registered via `profile.RegisterCatalog`, newest first
   (this is how downstream repos ship private profiles — see
   [private-catalogs.md](private-catalogs.md))
3. the embedded built-in catalog (`profiles/`, registered in init)

Same-name profiles shadow down the stack. `fab create <slug> -p <name>`
resolves by directory (`<slug>/<name>/profile.yaml`); the yaml's
`engine:` field selects the implementation, which is how
`fab create linear ...` maps onto the `httpmock` engine.

## Engines (how to build it)

`engine.Engine` is three methods: `Info` (agent-discoverable metadata:
default image, port, accepted seed types), `Create` (profile → running
container → `Creds`), `Destroy` (container ID → gone). Each engine maps
the profile's `seed:` steps onto whatever first-boot mechanism its
image provides — init SQL for Postgres/MySQL, `.js` for Mongo,
`redis-cli` lines post-boot, shell for SSH, a JSON fixture for
httpmock. Engines reject seed types they don't understand so a typo
never silently no-ops.

Containers are started via testcontainers-go with Ryuk (the reaper)
disabled on purpose: the CLI exits, the container stays. Cleanup is
`fab destroy`, driven by the state file.

## The httpmock engine and mockd

Container engines give you real databases. `httpmock` gives you real
*APIs*: one Go server (`mockd/`, its own module) hosts many mock
services; `env.MOCK_SERVICE` in the profile picks which one a container
serves, and the profile's `mock-fixture` seed is mounted at
`/seed.json` and loaded into per-container SQLite at boot.

Because the backing store is a real database, writes behave like the
real API: `messages.send` inserts a row that the next `threads.get`
returns; `issueCreate` allocates the next `ENG-<n>`. This is the
difference from recorded-fixture mocking — the read-after-write loop
works.

A service is one package under `mockd/services/`: SQLite DDL, a fixture
loader, and Express-style handlers on a tiny router (`{param}` segments
and AIP `:verb` suffixes). REST and GraphQL-dispatch styles both fit —
see `services/gmail` and `services/linear`.

## Targets (where to build it)

The default target is local Docker. `--target kubernetes`
(`target/kubetarget`) creates the same fixture as labeled Kubernetes
primitives (Secret/ConfigMap/Deployment/Service + seed Jobs) in an
existing cluster via your kubeconfig, returning in-cluster DNS
endpoints instead of localhost ports.

## State and creds

Every `fab create` writes the full instance record — including the
generated password / SSH key / kubeconfig — to `~/.config/fab/state.json`
(atomic rename; `$FAB_STATE_FILE` overrides). `fab ls / creds / destroy`
are views and edits of that file. Losing your terminal never loses the
credentials; `fab destroy` is the only way they become unrecoverable,
at which point the container is gone too.
