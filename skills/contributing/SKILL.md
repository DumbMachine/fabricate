---
name: fabricate-contributing
description: How to contribute to fabricate — add a stateful HTTP-API mock service, a profile, or an engine to the catalog of live, stateful environments. Use when extending fab: where files go (mockd/services/, profiles/, engine/), the registry wiring, the tests, the official-SDK-compatibility bar, and the verify loops. Points at the authoritative in-repo docs.
allowed-tools: Bash(make:*), Bash(go:*), Bash(fab:*)
---

# Contributing to fabricate

fabricate is a **registry for live, stateful environments** — seeded
databases, caches, and real API mocks you pull, use, and throw away.
The catalog is open and **contributions are welcome**. This skill is the
map: what to add, where it goes, and how to prove it works.

Pick the smallest extension that fits:

| You want to add | Write | Where it lives |
|---|---|---|
| Data/config for an environment fab already runs | a **profile** (YAML + seed files, no code) | `profiles/<slug>/<name>/` |
| A stateful mock of an HTTP API (Stripe, GitHub, Slack, an internal service) | a **mockd service** on the HTTP/OpenAPI engine | `mockd/services/<name>/` |
| New infrastructure software (Kafka, MinIO, Elasticsearch…) | an **engine** (Info/Create/Destroy) | `engine/<name>/` |

Do **not** add a new engine for an HTTP API. GitHub's `engine/github`
(emulate) is a stopgap; new API services — including a real GitHub —
are mockd packages like Gmail.

## Repo layout (three Go modules)

- `cli/` cobra commands; `cmd/fab/` the entrypoint.
- `engine/` the `Engine` interface + one package per **infra** engine
  (`postgres/`, `redis/`, `httpmock/` as the API runtime, `github/`
  emulate stopgap). All API mocks live under mockd, not a new engine.
- `mockd/` — **own module** — the HTTP-mock server that runs inside the
  `httpmock` container. `mockd/mock/` is the framework (router, `Ctx`,
  SQLite store); `mockd/services/` is one package per mocked API.
- `profiles/` embedded built-in catalog: `profiles/<slug>/<name>/`.
- `e2e/` build-tagged Go e2e; `e2e/sdk/` official-client conformance.
- `skills/` agent skills (this file is one).

## Add a mock service (the most common contribution)

1. **`mockd/services/<name>/<name>.go`** — `New() *mock.Service`:
   declare `Tables` (SQLite DDL), a `Seed(db, fixture)` JSON loader, and
   routes (`s.GET("/path/{param}", handler)`; `:verb` suffixes for
   AIP-style custom methods). Handlers get `Ctx` (DB, Params, Query,
   Body, `JSON`/`Bind`/`GErr`). Copy `services/gmail` (clean REST).
2. **Register it twice:** an entry in `mockd/main.go`'s `registry` map,
   **and** the same slug in `engine/httpmock`'s `KnownServices` (the
   CLI's discovery copy — the two modules can't import each other).
3. **Unit tests beside it:** in-memory DB, fixture JSON, httptest. Cover
   the stateful loop (write → the next read reflects it), not just
   reads. Assert the **real resource shape** — every field the official
   client reads, not just the fields your handler happens to emit.
4. **Profile:** `profiles/<name>/<profile>/{profile.yaml,seed.json}`
   with `engine: httpmock` and `env.MOCK_SERVICE: <name>`; add the new
   top-level dir to `profiles/embed.go`'s `//go:embed` line.
5. **SDK conformance (the bar — see below):** if the service has an
   official client library, add `e2e/sdk/<service>.test.mjs` driving the
   real vendor client against a `fab`-created instance (copy
   `e2e/sdk/gmail.test.mjs`) and add the dep to `e2e/sdk/package.json`.
6. **Verify:** `make check`, then `make images` (rebuilds the httpmock
   image with your service baked in), then `make e2e` / `make sdk-e2e`.
   Flagship services also get an agent skill (`skills/<service>/SKILL.md`).

## The compatibility bar

Stateful is necessary but **not sufficient**. A mock earns its place by
being **compatible with the service's official client SDK**: a real
integration should be able to point its SDK at the mock's URL and work —
reads *and* stateful writes. We aim for every REST mock to clear this
bar, verified end to end in `e2e/sdk/` against the vendor's own client
(Gmail/Play: `googleapis`; GitHub: `@octokit/rest`; Stripe: npm `stripe`
once the mock ships — see `docs/sdk-conformance.md`). Match the
real wire contract — paths, response envelopes, field names, error codes,
and Stripe's form-encoded POSTs — because that is exactly what the
generated client depends on.

GraphQL mocks are harder: generated GraphQL SDKs expect the full
connection / `pageInfo` / relation-resolution contract, which the
current query-text-dispatch approach does not satisfy — so `linear` /
`railway` are **experimental** and not yet in the conformance suite.

## Verify loops (each stronger than the last)

```bash
make check     # gofmt + vet + unit tests, all modules, no Docker, seconds
make images    # rebuild fabricate/httpmock:local (needed by the two below)
make e2e       # real containers through the real CLI
make sdk-e2e   # official vendor clients ⇄ the mocks (needs Node)
make verify    # check + e2e + sdk-e2e — run before calling work done
```

Don't call a change done until the strongest loop that can observe it
passes. A skip (Docker/image/Node missing) is **not** a pass.

## Conventions

- Comments explain *why* (locking, races, API quirks). Wrap error chains
  with a package prefix (`fmt.Errorf("httpmock: …: %w", err)`).
- Output stays machine-parseable: the payload on stdout, everything
  informational on stderr.
- The state file holds plaintext creds for local throwaways — never log
  it, never commit it.

## Authoritative guides

- `docs/adding-a-mock-service.md` — full mock-service walkthrough.
- `docs/adding-an-engine.md` — engines (+ the profile-vs-service-vs-engine triage).
- `docs/authoring-profiles.md` — profile schema and seed files.
- `CLAUDE.md` — the in-repo development guide.

Open a PR — contributions welcome.
