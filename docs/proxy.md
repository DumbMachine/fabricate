# Transparent HTTPS proxy

`fab run --proxy -- <command>` lets an application keep a provider SDK's normal
production hostname while all claimed provider traffic is answered locally.
Proxy environment variables and trust material are scoped to the wrapped child
process; Fabricate never modifies the developer's login shell or system trust.

## Runtime path

```text
official client
  -> HTTPS_PROXY / CONNECT provider.example
  -> loopback Fabricate proxy
  -> per-run TLS certificate
  -> exact host/path route from resource descriptor
  -> synthetic sandbox authorization
  -> local compiled resource handler
```

The proxy overwrites `Authorization` on routed provider hosts with the
environment's ephemeral token. Direct-mode clients still send
`$FAB_*_TOKEN` to `$FAB_*_URL`. Proxy-mode examples omit that header.

The child receives `HTTPS_PROXY`, `HTTP_PROXY`, `SSL_CERT_FILE`,
`NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, and `GIT_SSL_CAINFO` as applicable.
The CA and ephemeral service state are destroyed when the wrapper exits.

## Routing and safety

Provider hosts come from `httpresource.Descriptor.ProviderHosts`. A resource
may require narrower path routing on a shared host; Gmail, for example, claims
only `/gmail/` on `www.googleapis.com`. Google OAuth token refresh is handled by
a local synthetic token route so the wrapped application does not need working
Google credentials. The route accepts `POST /token` on `oauth2.googleapis.com`
with `grant_type=refresh_token` and a non-empty refresh token, then returns
the environment's synthetic access token. It does not implement
authorization-code OAuth, store customer tokens, or refresh other providers.

Unknown hosts are tunneled unchanged to their normal destination by default.
This lets one provider be fabricated inside a full application without
interfering with unrelated networked subprocesses. A route claimed by a
Fabricate resource always takes precedence over forwarding.

```yaml
apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata:
  name: acme-gmail
services:
  gmail:
    resource: gmail
    scenario: gmail.acme-corp.v1
proxy:
  unknown_hosts: reject
  passthrough:
    - api.openai.com
```

Use strict mode for isolated provider conformance tests. It denies every
unmapped destination except exact bare hostnames listed under
`proxy.passthrough`:

```yaml
proxy:
  unknown_hosts: reject
```

Strict mode prevents accidental live provider egress from the wrapped process
tree. `passthrough` remains useful when a conformance test has a small, known
secondary dependency.

## Compatibility limits

The proxy only affects clients that honor standard proxy and CA environment
variables. Applications with certificate pinning, custom transports that
ignore proxies, or non-HTTP provider protocols need a direct endpoint seam.
On macOS, Go applications may need to append `SSL_CERT_FILE` to system roots in
their process-scoped transport because platform root loading can ignore the
environment variable.

## Introspection

Every intercepted request and response is appended to the environment's
durable JSONL log. Headers, query parameters, bodies, status, and timing are
captured with a one-MiB body limit. Authorization, cookies, OAuth tokens,
client secrets, and secret-shaped fields are redacted before disk writes.

```bash
fab logs acme-gmail
fab logs acme-gmail --cat
fab logs acme-gmail --all
```
