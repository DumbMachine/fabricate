# fabricate — development guide

`fab` spins up real, seeded, throwaway resources (databases, caches, SSH
hosts, stateful HTTP-API mocks) and prints credentials. This file is the
guide for working on the codebase — humans and coding agents alike.

## Repo map (three Go modules)

```
cmd/fab/          main() — thin wrapper over cli.Execute()
cli/              cobra commands (create/ls/creds/destroy/profiles/engines/wait)
                  PUBLIC package: downstream repos wrap it to ship private catalogs
engine/           the Engine interface + one package per engine
  engine.go       Engine, Creds, Instance, Info — start here
  postgres/ mysql/ mongodb/ redis/ prometheus/ ssh/ kubernetes/
  awsconsole/     Moto-backed fake AWS (seeds via real AWS SDK calls)
  github/         STOPGAP: vercel-labs/emulate image. Do not extend.
                  Port GitHub onto mockd (same Service iface as Gmail).
  httpmock/       THE API engine — runs mockd. All API-like services go here.
profile/          profile.yaml loading, validation, catalog stack (RegisterCatalog)
profiles/         embedded built-in catalog: profiles/<slug>/<name>/profile.yaml
target/           docker vs kubernetes provisioning targets (kubetarget/)
internal/state/   ~/.config/fab/state.json instance store
internal/output/  table/json/env/url renderers
mockd/            OWN MODULE — the httpmock server that runs inside the container
  mock/           framework: Service, router ({param}, :verb), Ctx, SQLite store
  services/       one package per mocked API (linear, gmail, googleplay, ...)
e2e/              build-tagged (`e2e`) tests driving the real binary + real Docker
skills/           agent skills (SKILL.md per topic; installable via `npx skills add`)
examples/         full-suite scenarios (rideshare: 4 resources + k8s, one incident)
```

Key indirections:

- The CLI resolves `fab create <slug> -p <name>` by **directory**
  `profiles/<slug>/<name>/`; the profile's `engine:` field picks the
  implementation. That's why `fab create linear ...` works: the
  directory is `profiles/linear/`, the engine is `httpmock`.
- `profiles/embed.go` embeds the catalog and registers it via
  `profile.RegisterCatalog` in init(). New top-level profile dirs must
  be added to its `//go:embed` directive.
- `mockd` is a separate module because it's built inside
  `mockd/Dockerfile` (CGO + go-sqlite3 on alpine) with mockd/ as the
  build context.

## Verification loops

Work in this order; each loop is strictly slower and stronger than the
one before. Don't call a change done until the strongest loop that can
observe it passes.

1. **Unit loop (seconds, no Docker):**
   `make check` — gofmt + vet + unit tests for all modules.
   Mock services are fully testable here: `Init(":memory:", fixture)`
   then drive `svc.ServeHTTP` with httptest (see any `services/*_test.go`).
2. **E2E loop (tens of seconds, Docker):**
   `make images && make e2e` — builds the fab binary, creates real
   containers (Redis lifecycle, gmail + linear httpmock flows), asserts
   through the public CLI + HTTP surface, destroys them. Tests skip
   with a clear message when Docker or an image is missing — a skip is
   NOT a pass for changes those tests cover.
3. **SDK-conformance loop (Docker + Node):**
   `make sdk-e2e` — drives services with each vendor's OFFICIAL first-party
   JS client (`e2e/sdk/`): `googleapis` (Gmail, Play), `@octokit/rest`
   (GitHub — today emulate, target mockd), `stripe` (gate for the Stripe mock). Proves the
   mocks match the real wire contract, not just hand-written curl. Same
   skip policy. GraphQL services (linear, railway) are NOT yet covered
   — they don't satisfy `@linear/sdk`. See docs/sdk-conformance.md.
4. **Manual probe (when touching an engine):**
   `make install && fab create <engine> -p <profile>` then talk to the
   returned URL yourself; `fab destroy` after. `fab ls -o json` and
   `docker ps --filter label=org.testcontainers=true` show leaks.

`make verify` = 1 + 2 + 3. CI (.github/workflows/ci.yml) runs both jobs.

## How to add things

### A mock service (most common change)

1. `mockd/services/<name>/<name>.go`: `New() *mock.Service` — declare
   `Tables` (SQLite DDL), a `Seed(db, fixture)` JSON loader, and routes
   (`s.GET("/path/{param}", handler)`; `:verb` suffixes supported for
   AIP-style custom methods). Handlers get `Ctx` (DB, Params, Query,
   Body, JSON/Bind/GErr helpers). REST example: `services/gmail`.
   GraphQL-ish example (dispatch on query text): `services/linear`.
2. Register it in `mockd/main.go`'s `registry` map.
3. Unit tests beside it: in-memory DB, fixture JSON, httptest. Cover
   the stateful loop (write → read reflects it), not just reads. Assert
   the real resource shape (every field the official client reads), not
   just the fields your handler happens to emit — that's how a missing
   field gets caught in the fast loop.
4. Profile: `profiles/<name>/<profile-name>/{profile.yaml,seed.json}`
   with `engine: httpmock` and `env.MOCK_SERVICE: <name>`; add the new
   top-level dir to `profiles/embed.go`'s `//go:embed` line.
5. `make check`, then `make images && make e2e` (add an e2e case if the
   service is a flagship like gmail/linear).
6. If the service has an official client library, add an SDK-conformance
   test: `e2e/sdk/<service>.test.mjs` driving the real vendor client
   against a `fab`-created instance (copy `e2e/sdk/gmail.test.mjs`), add
   the dep to `e2e/sdk/package.json`, and run `make sdk-e2e`. This is the
   contract that stops a stateful-but-non-compliant mock from shipping.
   (REST services map cleanly; the query-text-dispatch GraphQL services
   do not yet satisfy generated SDKs — leave those uncovered.)
7. Flagship services also get an agent skill: `skills/<service>/SKILL.md`
   (endpoints, the stateful loop, fixture shape — copy skills/gmail).

### An engine

Full guide: docs/adding-an-engine.md (including the profile-vs-mock-
service-vs-engine triage). Short version:

1. `engine/<name>/<name>.go` implementing `engine.Engine` (Info /
   Create / Destroy). Copy `engine/redis` (the cleanest template);
   reject unknown seed types loudly (see any engine's seed loop).
2. Register in `cli/engines.go`'s `engines` map.
3. Ship at least an `empty` profile under `profiles/<name>/` and add
   the dir to `profiles/embed.go`.
4. If it should work on `--target kubernetes`, extend
   `target/kubetarget`.

### A profile

`profiles/<engine>/<name>/profile.yaml` + seed files. Schema reference:
`fab profiles schema`, authoring guide: docs/authoring-profiles.md.
Test cheaply with `FAB_PROFILES_DIR` pointing at a scratch dir before
embedding. Related CLI surface to keep coherent when touching this
area: `profiles show --files/--file/--head` (bounded example reading),
`profiles init --from` (copy-an-example), `profiles add` (template
packs from GitHub/local dirs — cli/profiles_add.go).

## Conventions & gotchas

- Ryuk (testcontainers' reaper) is deliberately disabled in
  `engine/engine.go` init — fab containers must outlive the CLI
  process. Don't "fix" that.
- `fab destroy` must stay idempotent: a missing container is success
  (`engine.DockerRM`), otherwise state entries strand.
- The state file is plain JSON with plaintext creds for local
  throwaways; never log it, never commit it.
- Images `fabricate/httpmock:local` and `fabricate/emulate:local` are
  the local dev tags (built by `make images`); releases push GHCR
  copies via .github/workflows/release.yml, and engines honor
  `FAB_HTTPMOCK_IMAGE` / `FAB_EMULATE_IMAGE` overrides between the
  profile pin and the compiled default. Publishing/pinning strategy:
  docs/releasing.md. Error messages reference `make images` — keep
  them accurate if you rename targets.
- Comments explain *why* (locking, races, API quirks); keep that
  density when editing. Wrap error chains with package prefixes
  (`fmt.Errorf("httpmock engine: ...: %w", err)`).
- Output must stay machine-parseable: anything informational goes to
  stderr, only the payload goes to stdout.
