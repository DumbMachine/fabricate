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
Use a fresh session for each test attempt. This API does not yet provide durable
session recovery, idempotent create, TTL/renewal, a dataset publication registry,
or coordinated reset; callers must retain creation receipts and avoid replaying
an uncertain creation/mutation. Provider state is ephemeral on service shutdown.
