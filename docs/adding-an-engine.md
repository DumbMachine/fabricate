# Adding an engine

An engine teaches fab a new *kind* of container: how to start it, wait
for readiness, seed it, and hand back credentials.

## Do you actually need one?

Three extension points, cheapest first:

| You want | You need | Guide |
|---|---|---|
| Different data/config in a container fab already runs (your schema in Postgres, your rules in Prometheus, a different image tag of the same software) | **a profile** — no code | [authoring-profiles.md](authoring-profiles.md) |
| A mock of an HTTP API (Stripe, Slack, Jira, an internal service) | **a mockd service** — one Go package, no engine changes | [adding-a-mock-service.md](adding-a-mock-service.md) |
| New infrastructure software fab can't run at all (Kafka, MinIO, Elasticsearch, ClickHouse...) | **an engine** — this guide | below |

Rule of thumb: if the thing speaks HTTP and you'd be happy with a
faithful subset, write a mockd service. Engines are for real software
whose real protocol you need (SQL wire, Kafka protocol, S3 API from
the real MinIO, ...).

## What an engine is

One package under `engine/<slug>/` implementing three methods
(`engine/engine.go`):

```go
type Engine interface {
    Info() Info                     // static metadata for `fab engines` + agents
    Create(ctx, name, *profile.Profile) (*Instance, error)
    Destroy(ctx, containerID string) error   // almost always engine.DockerRM
}
```

`engine/redis/redis.go` (~190 lines) is the best template: it has the
full shape — password generation, config injection, wait strategy,
post-boot seeding, URL construction, teardown-on-partial-failure —
without the bulk of the bigger engines.

## Walkthrough (hypothetical `minio` engine)

### 1. Info — the contract agents discover

```go
const (
    Engine       = "minio"
    defaultImage = "minio/minio:RELEASE.2026-01-01T00-00-00Z" // pin, don't use :latest
)

func (eng) Info() engine.Info {
    return engine.Info{
        Slug:           Engine,
        DefaultImage:   defaultImage,
        DefaultPort:    9000,
        SupportedSeeds: []string{profile.SeedTypeShell}, // or a new seed type
        Description:    "S3-compatible object store. Returns endpoint + access/secret keys.",
    }
}
```

If no existing seed type fits, add a constant to
`profile/profile.go` (`SeedType...`) and document it in the seed-step
comment there — engines must reject types they don't understand, so
every engine names its accepted set explicitly.

### 2. Create — start, wait, seed, return creds

The invariants every engine keeps:

- **Generate secrets per instance**: `engine.GeneratePassword()` when
  the profile leaves `defaults.password` blank. Never bake a default
  password into the engine.
- **Resolve the image** with `engine.OrDefault(p.Image, defaultImage)`
  so `--image` and profile overrides keep working.
- **Wait for real readiness**, not just process start — a log line +
  listening port (`tcwait.ForLog(...)`, `ForListeningPort`,
  `ForHTTP`), budgeted by
  `engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)`.
- **Apply seeds in array order**; reject unknown seed types with an
  error naming the step index and type (a typo must never no-op).
  Seed either via the image's first-boot mechanism (mounted init dirs,
  like postgres) or post-boot via `docker exec` (like redis/ssh).
- **Terminate on partial failure.** If seeding fails after the
  container started, `container.Terminate(ctx)` before returning the
  error — otherwise an orphan container outlives any state record.
- **Return a usable URL.** `Creds.URL` should paste straight into the
  ecosystem's standard client (`postgres://...`, `redis://...`,
  `http://...`). Anything extra (region, console port) goes in
  `Creds.Extra`.
- **Don't auto-reap.** Ryuk is disabled on purpose
  (`engine/engine.go` init); the container must outlive the CLI.
  Cleanup is `Destroy` — which is just `engine.DockerRM(ctx, id)`,
  already idempotent about missing containers.

### 3. Register + ship a profile

- Add the engine to the `engines` map in `cli/engines.go`.
- Create `profiles/minio/empty/profile.yaml` (every engine ships at
  least an `empty`) and add `all:minio` to the `//go:embed` line in
  `profiles/embed.go`.
- Prefer at least one seeded profile with a story in it — see
  "What makes a good seed profile" in
  [authoring-profiles.md](authoring-profiles.md).

### 4. Verify

```bash
make check                      # compiles, vet, unit tests
make install
fab engines                     # your slug + seed types show up
fab create minio -p empty       # real container
eval "$(fab creds empty --env)" # creds work
fab destroy empty               # teardown works; fab ls is empty
```

Add a lifecycle case to `e2e/e2e_test.go` (copy `TestRedisLifecycle`)
if the image is small enough for CI; `make e2e` must stay green either
way.

### 5. Optional: Kubernetes target

If the engine should also work with `--target kubernetes`, extend
`target/kubetarget` (it maps engines onto
Secret/ConfigMap/Deployment/Service + seed Jobs). Docker-only engines
are fine — `github` is one, because its image is local-only.

## Custom images

If the engine needs an image that doesn't exist upstream (the way
`httpmock` and `github` do), keep the Dockerfile next to the engine
(or as its own module like `mockd/`), add it to `make images` with a
`fabricate/<name>:local` tag, honor a `FAB_<NAME>_IMAGE` env override
between the profile pin and the compiled default, and wire it into
`.github/workflows/release.yml`. The publishing/pinning strategy is
documented in [releasing.md](releasing.md).
