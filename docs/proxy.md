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

The child receives `HTTPS_PROXY`, `HTTP_PROXY`, `SSL_CERT_FILE`,
`NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, and `GIT_SSL_CAINFO` as applicable.
The CA and ephemeral service state are destroyed when the wrapper exits.

## Routing and safety

Provider hosts come from `httpresource.Descriptor.ProviderHosts`. A resource
may require narrower path routing on a shared host; Gmail, for example, claims
only `/gmail/` on `www.googleapis.com`. Google OAuth token refresh is handled by
a local synthetic token route so the wrapped application does not need working
Google credentials.

Unknown hosts fail closed. An environment may name exact bare hostnames under
`proxy.passthrough` for unrelated live dependencies. Passthrough is never
inferred and cannot override a route claimed by a Fabricate resource.

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
  enabled: true
  passthrough:
    - api.openai.com
```

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
