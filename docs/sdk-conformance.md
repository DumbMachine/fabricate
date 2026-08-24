# Official-SDK conformance

A mock that is stateful but fails the vendor's **first-party JavaScript
SDK** is not a product. Companies will point `stripe`, `@octokit/rest`,
and `googleapis` at us. Those clients are the wire contract — not our
curl examples, not the OpenAPI file.

This is the bar for every official HTTP service. It is also how we
prove the CI seam in [ci-and-devex.md](ci-and-devex.md) is real:
the same constructor options a company sets from env vars are the
ones these tests set from `fab create`.

Suite: `e2e/sdk/`. Run: `make sdk-e2e`. CI already calls it. A missing
Docker/image is a **skip with a message**, never a silent pass.

---

## What we researched (first-party Node SDKs)

### Stripe — npm `stripe` (stripe-node)

The SDK companies already have. It does **not** take `apiBase` (that's
PHP/Python). Node splits the URL:

```js
import Stripe from "stripe";

const u = new URL(process.env.STRIPE_API_BASE); // http://127.0.0.1:18761
const stripe = new Stripe(process.env.STRIPE_SECRET_KEY, {
  host: u.hostname,
  port: Number(u.port),
  protocol: "http",          // never talk HTTPS to a local mock
  maxNetworkRetries: 0,
  telemetry: false,
});
```

Constructor knobs (from stripe-node README): `host` default
`api.stripe.com`, `port` default 443, `protocol` default `https`,
`apiVersion` default = version bundled with the SDK release.

Wire details the mock **must** get right or stripe-node throws before
our handlers run:

| Fact | Why it matters |
|---|---|
| Paths live under `/v1/…` | SDK prefixes `DEFAULT_BASE_PATH = '/v1/'` |
| POST bodies are `application/x-www-form-urlencoded`, not JSON | `customers.create({ email })` becomes `email=…` |
| Lists are `{ object: "list", data: [], has_more, url }` | `stripe.customers.list()` decodes this shape |
| Resources have `id` + `object` (`customer`, `price`, …) | typed accessors |
| Errors are `{ error: { type, message, code? } }` | `Stripe.errors.StripeError` |
| `Authorization: Bearer sk_…` | any `sk_test_…` is fine locally |
| `Stripe-Version: YYYY-MM-DD` | pin or ignore; don't 400 unknown versions |
| `Idempotency-Key` on writes | replay the first result for the same key |

Acceptance test: [`e2e/sdk/stripe.test.mjs`](../e2e/sdk/stripe.test.mjs).
It skips until `fab create stripe` exists, then it is a hard gate:
list seeded customers/products/prices, `customers.create`, retrieve.
**Stripe is not an official service until this file is green.**

### GitHub — npm `@octokit/rest`

GitHub's own JS REST client (Octokit). The Enterprise seam is
`baseUrl` — same option Actions uses via `GITHUB_API_URL`.

```js
import { Octokit } from "@octokit/rest";

const octokit = new Octokit({
  auth: process.env.GITHUB_TOKEN,
  baseUrl: process.env.GITHUB_API_URL, // mock URL, no trailing slash
});
```

Our GitHub engine (vercel-labs emulate) is `api.github.com`-shaped:
paths are `/repos/{owner}/{repo}/issues`, not `/api/v3/…`. So
`baseUrl` is the instance URL itself, not `url + "/api/v3"`.

Acceptance test: [`e2e/sdk/github.test.mjs`](../e2e/sdk/github.test.mjs)
against the `issues` profile — `users.getAuthenticated`, `repos.get`,
`issues.listForRepo`, `issues.create`, re-list. Needs the emulate
image (`make images`).

### Gmail / Play — npm `googleapis`

Already green. Seam is `rootUrl` (trailing slash required):

```js
google.gmail({ version: "v1", auth, rootUrl: process.env.GMAIL_API_ROOT + "/" });
```

There is no standard env var. We still export `GMAIL_API_ROOT`.

### Linear — npm `@linear/sdk` (not yet)

```js
new LinearClient({
  apiKey: process.env.LINEAR_API_KEY,
  apiUrl: `${process.env.LINEAR_API_URL}/graphql`,
});
```

`apiUrl` defaults to `https://api.linear.app/graphql`. The generated
client assumes the full GraphQL connection contract (`nodes`,
`pageInfo`, relation resolvers). Our Linear mock still dispatches on
query text. **Do not add `@linear/sdk` tests until the mock speaks
that schema** — a failing generated-SDK test that we skip forever
teaches the wrong lesson. Keep Linear experimental.

---

## The rule

| Service | Official JS SDK | Seam | Suite |
|---|---|---|---|
| Gmail | `googleapis` | `rootUrl` | ✅ `gmail.test.mjs` |
| Google Play | `googleapis` | `rootUrl` | ✅ `googleplay.test.mjs` |
| GitHub | `@octokit/rest` | `baseUrl` | ✅ `github.test.mjs` |
| Stripe | `stripe` | `host` + `port` + `protocol` | ⏳ `stripe.test.mjs` (skip until catalog) |
| Linear | `@linear/sdk` | `apiUrl` | ❌ blocked on GraphQL contract |

A new official HTTP service ships **with** its `<service>.test.mjs`.
The contributing guide and `make verify` already require this. Pin the
SDK major in `e2e/sdk/package.json`; bumping it *is* a mock-compat
change.

Minimum assertions per file:

1. One read of seeded state through the SDK (not curl).
2. One write through the SDK.
3. A follow-up read that proves the write stuck.

That is the same loop a company's `npm test` will run after
`eval "$(fab env)"`.

---

## Mapping onto the product

- **Adapters.** A service adapter is not done at "handlers exist." It
  is done at "first-party SDK e2e is green." Stripe's `Load`/`Dump`
  JSON contract must round-trip objects stripe-node can decode.
- **User states.** `stripe.language-learning.v1` is only useful if
  `stripe.customers.list()` returns those students. The SDK test
  should run against a catalog state, not an empty world.
- **CI DevEx.** The constructor options above *are* the company seam.
  `fab env` must emit `STRIPE_API_BASE` / `STRIPE_SECRET_KEY` /
  `GITHUB_API_URL` / `GITHUB_TOKEN`. Document Node's host/port split
  next to PHP's `api_base` so we don't copy the wrong knob.
- **Vision.** Official-SDK e2e stays on the "protect while
  simplifying" list, now with names: `stripe`, `@octokit/rest`,
  `googleapis`. GraphQL generated clients are a later hardening
  milestone, not a skip we forget.

First slice addendum: when Stripe lands, `make sdk-e2e` includes
`stripe.test.mjs` going green on `language-learning` (or `minimal`)
before we call the service official.
