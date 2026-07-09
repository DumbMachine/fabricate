# SDK-conformance e2e

Unit tests prove the mocks are *stateful*. These tests prove they are
**compliant**: each REST `httpmock` service is driven by the vendor's
**official client library** — the same package a real integration would
use — not by hand-written `curl`. A mock can be perfectly stateful and
still be useless if the official SDK can't talk to it (wrong envelope
shape, missing fields, different paths). This suite is the guard against
that.

Each test creates a real `fab` instance from a built-in profile, points
the official client at it via the client's base-URL override, exercises
reads **and** stateful writes, then destroys the instance.

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
**skip with a message**, never a silent pass — same policy as the Go e2e.

## Coverage

| Service | Official client | Kind |
|---|---|---|
| `gmail` | `googleapis` (`google.gmail`) | REST ✅ |
| `google-play` | `googleapis` (`androidpublisher`, `playdeveloperreporting`) | REST ✅ |

GraphQL services (`linear`, `railway`) are **not** covered yet: they answer
hand-written queries but are not compatible with the generated GraphQL
SDKs (`@linear/sdk` assumes the full connection / `pageInfo` /
relation-resolution contract). They are marked experimental in the README
until the mock is hardened to the real schema contract.

Other REST services (`vercel`, `cloudflare`, `supabase`, `render`, `fly`)
have engine code but no built-in profile in this repo, so there is nothing
to `fab create` here — add a profile plus a `<service>.test.mjs` when one
lands.

## Adding a service

1. Add `<service>.test.mjs` (copy `gmail.test.mjs`).
2. Point the vendor's official client at `inst.url` via its base-URL
   option (googleapis: `rootUrl`; others vary — `serverURL`, `baseURL`).
3. Assert reads and at least one **stateful write** (write, then re-read
   and confirm it took — that's the whole value proposition).
4. Add the dependency to `package.json`.
