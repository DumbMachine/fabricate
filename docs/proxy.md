# Transparent proxy: no `apiBase` required

OneCLI’s architecture is an identity gateway: agents call **real**
`api.stripe.com` / `api.github.com`, a MITM HTTPS proxy matches host
+ path, injects the employee’s real secret, and forwards. The agent
never holds a live key. Setup is process-scoped:

```
HTTPS_PROXY=http://gateway:10255
SSL_CERT_FILE=~/.onecli/ca-bundle.pem   # trust the gateway CA
onecli run -- pytest
```

We can steal the **interception**, invert the **destination**.

OneCLI:  `api.stripe.com` + placeholder key  →  real Stripe + real `sk_live`
Fabricate: `api.stripe.com` + any key        →  sandbox Stripe + sandbox token

The company seam we asked for in [ci-and-devex.md](ci-and-devex.md)
(`STRIPE_API_BASE`) is still the honest default. The proxy is the
**zero-code** path for apps that hardcode vendor hosts — which is most
of them, and most official SDKs.

---

## What OneCLI actually does (so we copy the right slice)

From [how-it-works](https://onecli.sh/docs/how-it-works) and
`onecli run`:

| Piece | OneCLI | We take? |
|---|---|---|
| HTTPS MITM proxy + local CA | yes | **yes** — that's the seam |
| `HTTPS_PROXY` + `SSL_CERT_FILE` / `NODE_EXTRA_CA_CERTS` on a wrapped process | yes (`onecli run -- cmd`) | **yes** (`fab test -- cmd`) |
| Host + path pattern matching | yes | **yes** — map vendor host → sandbox env |
| Inject secrets the process never sees | real vault keys | **sandbox tokens**, not production keys |
| Policy / grants / approval cards | identity gateway | **no** — not our product |
| Audit of who called what | yes | later, as request log on the sandbox |
| Agents never hold a real secret | the point of OneCLI | nice side effect in tests; our point is **never hit production** |

Do not become an identity platform. Copy the network layer.

---

## Two seams, one `fab.yaml`

```yaml
name: lingo
environments:
  stripe:   { state: stripe.language-learning.v1 }
  github:   { state: github.issues.v1 }
  postgres: { state: postgres.lingo-app.v1 }

# Seam A — explicit (always available)
env:
  STRIPE_SECRET_KEY: ${stripe.token}
  DATABASE_URL:      ${postgres.url}

# Seam B — transparent proxy (opt-in)
proxy:
  enabled: true
  unmatched: deny          # never leak api.stripe.com to the real network
  intercept:
    - host: api.stripe.com
      env: stripe
    - host: api.github.com
      env: github
    - host: gmail.googleapis.com
      env: gmail
```

`fab test -- npm test` then:

1. `fab up` the sandbox.
2. Start a loopback HTTPS proxy with a sandbox-scoped CA.
3. Inject into the **child only**:

   ```
   HTTPS_PROXY=http://127.0.0.1:$PORT
   HTTP_PROXY=http://127.0.0.1:$PORT
   SSL_CERT_FILE=…/fab-ca.pem
   NODE_EXTRA_CA_CERTS=…/fab-ca.pem
   REQUESTS_CA_BUNDLE=…/fab-ca.pem
   GIT_SSL_CAINFO=…/fab-ca.pem
   ```

4. Run `npm test`. Stripe-node still dials `api.stripe.com`. The
   proxy terminates TLS, maps host → the stripe environment, injects
   the sandbox token, forwards. Postgres still uses `DATABASE_URL`
   (wire protocols are not HTTP; don't pretend).
5. Always `fab down`. The CA dies with the sandbox.

No `STRIPE_API_BASE`. No SDK constructor change. Official clients
keep their default hosts — which is exactly what
[sdk-conformance.md](sdk-conformance.md) already tests, except the
proxy is now the thing that rewrites the host instead of the test
file.

---

## Request path

```
app  --HTTPS CONNECT api.stripe.com-->  fab proxy
                                          │
                          allowlist hit?  │  unmatched?
                                │         │
                                ▼         ▼
                     terminate TLS     deny (default)
                     with fab CA       or passthrough
                                │
                                ▼
                     POST /v1/customers  →  sandbox stripe
                     Authorization: Bearer $sandbox_token
```

Rules that keep this from being a footgun:

1. **Allowlist only.** `api.stripe.com` is rewritten because `fab.yaml`
   said so. `api.openai.com` is not. Default unmatched = **deny** in
   `fab test`, so a forgotten mapping fails closed instead of charging
   a real card.
2. **Child process only.** Never set `HTTPS_PROXY` on the developer's
   login shell. `fab test --` / `fab run --` wrap one command, like
   `onecli run`.
3. **Sandbox-scoped CA.** Generated per sandbox, trusted only via env
   vars on that child. Not installed in the system keychain.
4. **HTTP APIs only.** Postgres, Redis, gRPC, Mongo stay on seam A
   (`DATABASE_URL`, …). The proxy does not speak those protocols.
5. **Token inject is optional.** If the child already has
   `STRIPE_SECRET_KEY=sk_test_fab`, pass it through. If it has a
   production-shaped key, **strip it** and replace with the sandbox
   token so a misconfigured CI secret cannot reach even the mock
   (and cannot leak to the network because of rule 1).

---

## Config surface

Per official service, ship a default intercept host list so `proxy:
enabled: true` is enough:

| Env | Default hosts |
|---|---|
| stripe | `api.stripe.com`, `files.stripe.com` |
| github | `api.github.com`, `uploads.github.com` |
| gmail | `gmail.googleapis.com`, `www.googleapis.com` (path-prefix `/gmail/`) |
| slack | `slack.com`, `*.slack.com` |

Overrides in `fab.yaml` for weird official SDK hosts (Stripe Connect,
GitHub uploads). Path prefixes exist because Google multiplexes many
APIs on `www.googleapis.com`.

CLI:

```bash
fab proxy status          # listening addr, mapped hosts, CA path
fab test -- npm test      # up + proxy + env + run + down
fab run -- ./scripts/qa   # same wrap, no test runner implied
```

`fab env` in proxy mode still prints `DATABASE_URL` for infra, plus
`HTTPS_PROXY` / `SSL_CERT_FILE` instead of `STRIPE_API_BASE`.

---

## What will not honor the proxy

Be honest; this is why seam A stays.

- SDKs with **certificate pinning** (some mobile / Java).
- Clients that ignore `HTTPS_PROXY` (parts of gRPC, some AWS SDK
  modes, Go when `ProxyFromEnvironment` is overridden).
- Binary protocols: Postgres, Redis, Mongo, SSH, k8s API via
  client-go may proxy HTTP but still need the real endpoint.
- Git over SSH.

For those, keep `STRIPE_API_BASE` / `DATABASE_URL`. The proxy is the
happy path for Node/Python/Ruby HTTP SDKs and `curl`, which is most
company CI.

Our own `e2e/sdk` tests should grow a **proxy mode** job: stripe-node
with **no** `host:` override, only `HTTPS_PROXY` + CA, dialing the
real `api.stripe.com` hostname. If that is green, the zero-code seam
is real.

---

## Safety vs OneCLI

OneCLI’s failure mode is “agent bypasses the proxy and has no real
key, so the call fails.” Ours must be “test bypasses the proxy and
**must not** hit live Stripe.”

So:

- In `fab test`, unmatched intercept-able vendor hosts **deny**.
- Never install the CA globally.
- Never document `export HTTPS_PROXY` in a shell rc.
- Print a one-line warning if the child could reach the public
  `api.stripe.com` (proxy down, CA untrusted → TLS error, which is
  the correct failure).

---

## First slice

Do **not** build MITM in the Gmail retrofit. Order:

1. Seam A (`STRIPE_API_BASE`, conventional env) — already planned.
2. Official SDK e2e with explicit host override — already planned.
3. Then a **loopback HTTP proxy** (CONNECT + MITM) behind
   `fab test --`, allowlist from the service catalog.
4. Add `e2e/sdk/stripe.proxy.test.mjs`: stripe-node default host,
   proxy env only.
5. Path-prefix matching for Google.

Until (3), tell companies the one constructor seam. After (3),
`proxy: enabled: true` is how we beat “we don’t want to touch
application code.”
