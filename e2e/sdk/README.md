# SDK-conformance e2e

Unit tests prove the mocks are *stateful*. These tests prove they are
**compliant**: each service is driven by the vendor's **official
first-party JavaScript client** — the same package a real integration
would use — not by hand-written `curl`. A mock can be perfectly
stateful and still be useless if that SDK can't talk to it (wrong
envelope, missing fields, JSON where the SDK form-encodes).

Each test creates a real `fab` instance from a built-in profile, points
the official client at it via that client's documented base-URL
override, exercises reads **and** stateful writes, then destroys the
instance.

The product-level write-up (seams, Stripe form-encoding, what "official"
means): [docs/sdk-conformance.md](../../docs/sdk-conformance.md).

## Run

```bash
make sdk-e2e          # from the repo root: builds fab, installs deps, runs
```

or directly:

```bash
cd e2e/sdk
npm install
FAB_BIN=../../bin/fab npm test     # needs a fab binary + Docker + `make images`
```

A missing prerequisite (no Docker, image not built, no `fab` binary) is a
**skip with a message**, never a silent pass. Same policy as the Go e2e.

## Coverage

| Service | Official JS client | Seam | Status |
|---|---|---|---|
| `gmail` | `googleapis` (`google.gmail`) | `rootUrl` | ✅ |
| `google-play` | `googleapis` (`androidpublisher`, `playdeveloperreporting`) | `rootUrl` | ✅ |
| `github` | `@octokit/rest` | `baseUrl` | ✅ (emulate image) |
| `stripe` | `stripe` (stripe-node) | `host` + `port` + `protocol` | ⏳ skip until `fab create stripe` exists; then a hard gate |
| `linear` | `@linear/sdk` | `apiUrl` | ❌ GraphQL mock is not schema-complete |

## Adding a service

1. Add `<service>.test.mjs` (copy `gmail.test.mjs` or `github.test.mjs`).
2. Point the vendor's official JS client at `inst.url` via **that
   SDK's** override — do not guess. Known seams:
   - `googleapis`: `rootUrl` (trailing slash)
   - `@octokit/rest`: `baseUrl` (no trailing slash; our GitHub engine is
     `api.github.com`-shaped, not `/api/v3`)
   - `stripe`: `host`, `port`, `protocol: "http"` — **not** `apiBase`
   - `@linear/sdk`: `apiUrl` pointing at `/graphql` (later)
3. Assert reads and at least one **stateful write** (write, then re-read
   and confirm it took).
4. Add the dependency to `package.json`, pin the major.
5. A new official service is not done until this file is green in
   `make sdk-e2e`.
