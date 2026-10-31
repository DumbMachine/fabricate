# External consumers

Run Fabricate independently of the application under test. Consumers need only
an HTTP origin and an optional operator control credential. They must not import
Fabricate packages, read its checkout, or start its process as part of app startup.

`fab serve --addr 127.0.0.1:4183` serves the API locally. For an operator-managed
deployment set `FAB_CONSOLE_TOKEN` and use HTTPS ingress with `--public-url`.
The token protects `/api/*` and `/.well-known/fabricate`; send it as a bearer
credential. The console supports operator sign-in using that token, exchanged for an
eight-hour HttpOnly browser session. `FAB_CONSOLE_TOKEN_FILE` supports mounted
secrets; use it instead of `FAB_CONSOLE_TOKEN`, not together. API clients retain
bearer authentication. Browser sessions require same-origin mutations and do
not grant access to provider endpoints, which still require service tokens.
This is operator authentication, not multi-tenant account isolation or a complete
SaaS release. See [the deployment guide](../deploy/console/README.md).

## Contracts and datasets

- `GET /.well-known/fabricate`: discover the catalog and lifecycle controls.
- `GET /api/resources/{resource}/contract`: compiled OpenAPI, state schema,
  resource descriptor and provider base path.
- Append `?operations=GetTask,UpdateTask` to select exact compiled operation IDs.
  An absent operation returns 422. References/components remain available.
- `GET /api/scenarios/{id}/document`: full immutable baseline and canonical digest.
- `POST /api/resources/{resource}/validate`: validate a complete scenario document
  with the existing resource codec, returning its digest. Does not start services.

Use operation IDs from discovery; generated IDs can differ in capitalization
from upstream source documents. A contract proves only its published surface.

## Composition

The existing `POST /api/sessions` accepts catalog selections and compositions:

```json
{
  "kind": "composition",
  "id": "task-review",
  "services": {
    "primary": {"resource": "asana", "scenario": "asana.acme-sprint.v1"},
    "control": {"resource": "asana", "scenario": "asana.acme-sprint.v1"}
  }
}
```

Each service can optionally supply `state`, replacing the selected baseline's
state object. Fetch the document/schema, edit explicit synthetic data, and
validate first. Baseline IDs must exist in the resource catalog. No filesystem
path or executable seed code is accepted. Every service is validated before
provisioning and a failed start cleans up its partial runtime.

The response supplies separate URLs and bearer tokens per service, even for
multiple instances of the same resource. Use those service credentials for
provider calls. Do not pass the operator control credential to provider APIs.
For OpenAPI clients, append `basePath` to the service URL. Provider APIs also
retain their normal supported paths and request/response shapes.

## Verification and cleanup

- `GET /api/sessions/{id}`: inspect live connections.
- `GET /api/sessions/{id}/state`: export current per-service scenario documents.
- `GET /api/sessions/{id}/logs`: inspect redacted request/response evidence.
- `DELETE /api/sessions/{id}`: close this session; repeated close is idempotent.

State export is a per-service observation, not an atomic cross-service snapshot.
Quiesce writes when comparing a whole world. Capture logs/state before closing.
Use a fresh session for each test attempt. Mounted storage supports session recovery, idempotent creation and TTL/renewal
as described below. A dataset publication registry and coordinated reset are not
yet provided. Retain creation keys/receipts; provider writes are not automatically
idempotent and must not be blindly replayed.

## Mounted storage

Hosted `fab serve` can use `FAB_STATE_DIR` (or `--state-dir`) on a persistent
mount. With a stable `FAB_CONSOLE_PUBLIC_URL`, direct service URLs, service
tokens, sandbox identities and committed database mutations survive restart.
`GET /api/config` reports `durable: true`. Existing API clients do not need to
recreate connections after recovery. `DELETE /api/sessions/{id}` still deletes
the sandbox and its stored state. Without a state directory, runtime state is
disposable. See [deployment lifecycle](../deploy/console/README.md#lifecycle-and-data)
for storage ownership, recovery failures and backup requirements.


## Agent control and retry safety

The deployment also exposes [Streamable HTTP MCP](mcp.md) at `/mcp`, with typed
tools over the same handlers as this API. Project bearer credentials restrict
list/read/create/provider-call/renew/close to their project. Only the operator
can manage `/api/projects` and their credentials. The browser remains an operator
console. Discovery includes the MCP endpoint and complete HTTP lifecycle links.

Mounted `POST /api/sessions` accepts `project`, `ttlSeconds` and
`idempotencyKey` (or the `Idempotency-Key` header). Repeat identical arguments/key
to recover the original sandbox; different arguments return 409, and a closed
key returns 410. `GET /api/requests/{key}` inspects the current project request.
`DELETE /api/requests/{key}` cancels setup and closes its sandbox, including a
lost creation response. An unknown key is durably reserved as closed, preventing
late creation; repeated cancellation is safe. Operators may supply `?project=ID`;
project credentials cannot change scope. Unpublished expired creations are
cleaned by the same sweep as live sandboxes. Failed deletion remains revoked
and is retried, with cleanup errors reported to server logs.
`POST /api/sessions/{id}/renew` accepts `{ "ttlSeconds": 3600 }`. Expired service
routes stop working immediately and cleanup follows; renewal cannot resurrect
an expired sandbox. Legacy keyless creation remains supported for existing API
clients. New clients should always supply a key in mounted mode.

`GET /api/resources/{resource}/contract?view=operations&offset=0&limit=50` returns
an operation catalog. Add `stateSchema=true` to include the real state schema.
`?operations=GetTask,UpdateTask&view=compact` retains the selected OpenAPI paths
and transitively referenced definitions. Without `view`, existing consumers
continue receiving the full contract. Unsupported operation IDs still fail.
