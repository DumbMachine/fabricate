# Agent access over MCP

Every `fab serve` deployment exposes `/mcp` using the official Go MCP SDK's
Streamable HTTP transport. It negotiates supported protocol versions and uses
stateless JSON responses. GET/DELETE return 405: MCP transport sessions are not
sandbox lifetimes. Restarting/reconnecting the MCP client does not reset a
sandbox. Origin validation, bearer authentication and project authorization run
on every request. This deployment uses static operator/project bearer credentials;
it does not provide an OAuth login flow.

`/.well-known/fabricate` remains a custom HTTP discovery document for existing
clients. It is a predictable metadata location, not a registered MCP discovery
standard or a JSON-RPC endpoint. MCP clients connect directly to `/mcp`; they do
not need this document. It now links the complete HTTP lifecycle and MCP endpoint.

## Connect Codex

An operator creates a project and issues a named expiring credential using the
operator-only project tools or these authenticated HTTP operations:

- `POST /api/projects`: `{ "id": "demo-team", "maxSandboxes": 8,
  "defaultTTLSeconds": 86400, "maxTTLSeconds": 604800 }`.
- `POST /api/projects/demo-team/tokens`: `{ "name": "codex", "ttlSeconds": 86400 }`.
  Capture the returned `token` privately; it is returned only at issuance.
- `PATCH /api/projects/demo-team` updates quota/default and maximum TTL for future
  creation/renewal, without resetting existing sandboxes.
- `GET /api/projects/demo-team/tokens` lists IDs/expiry/revocation metadata only.
- `DELETE /api/projects/demo-team/tokens/{id}` immediately revokes that credential.

Project credentials require mounted storage and configured operator authentication.
They cannot issue credentials, change projects or operate another project's
sandboxes. The server stores only their SHA-256 digests. Revocation and credential
expiry do not rotate sandbox service tokens; sandbox expiry is a separate policy.

Set `FABRICATE_MCP_TOKEN` in the agent process environment from a secret manager,
then register the endpoint without putting the value in configuration:

```sh
codex mcp add fabricate --url https://fabricate.example.com/mcp --bearer-token-env-var FABRICATE_MCP_TOKEN
```

For local development use the actual loopback console URL. The Codex process
must receive the environment variable, not just the shell used to register MCP.
The UI's Deployment / API access tab provides this command for its own origin.
An operator credential exposes administrative tools; use a project credential
for routine agent work. The browser console continues to use operator sign-in.

## The self-service workflow

The server publishes instructions and typed input/output schemas. A fresh agent
can follow this sequence without checkout access or repository-specific setup:

1. `deployment_get`: inspect scope, capacity and persistent-storage support.
2. `catalog_list`: choose a shipped provider, baseline or multi-service environment.
3. `resource_contract`: first discover a bounded operation catalog; use exact
   operation IDs to fetch compact real OpenAPI and referenced definitions.
   `stateSchema: true` includes the compiled custom-state schema. Use `offset` /
   `limit` to traverse the operation catalog; `full: true` explicitly requests the
   unprojected contract. These views derive from compiled OpenAPI, never query
   recipes or another behavior registry.
4. `scenario_document` / `scenario_view`: inspect a baseline. For custom state,
   use `scenario_validate` before composing. Large documents can exceed the tool
   response bound; use provider pagination or the authenticated HTTP export.
5. `sandbox_create`: choose scenario/environment/composition, service names,
   optional custom state and TTL. In mounted mode supply a stable
   `idempotencyKey` and retain it. Each service gets independent state and token.
6. After uncertain setup, use `sandbox_request_status`. Retry `sandbox_create`
   with the exact same arguments and key to resume/recover the same sandbox.
   Different arguments with that key return 409; closed/expired keys return 410.
7. `service_read` performs provider GET reads. `service_request` calls other
   published paths/methods in a named sandbox service.
   Include the provider's `basePath`. It cannot contact arbitrary hosts or the
   control API. Perform a separate read after a mutation. Provider mutations do
   not inherit creation idempotency; inspect before replaying an uncertain write.
8. Use `sandbox_get`, `sandbox_list`, `sandbox_logs` and `sandbox_state` to verify.
   Truncated logs and oversized outputs fail or identify their bounds explicitly.
9. `sandbox_renew` extends lifetime within project policy. `sandbox_close`
   permanently deletes this synthetic sandbox; repeated close is idempotent.

Connection details work with Access/Krucible's generic HTTP resource: service URL
plus contract `basePath`, the provider's bare OpenAPI document, and that service's
bearer token. Keep operator/project credentials on the control plane. Existing
Access scenario tooling can perform the authorized connection/vault installation;
no Fabricate implementation or provider seeds belong in Access.

## Lifecycle guarantees and limits

Mounted storage retains project ownership, expiry, credentials, creation keys,
service databases and creation status. Incomplete private initialization resumes
from its saved request after restart. A ready sandbox reopens existing databases
and keeps its original service URLs/tokens. Failed initialization with a creation
key can be retried using that same request; no provider mutation is auto-retried.
Creation is serialized by the supervisor to prevent duplicate same-key starts.

Project sandbox quotas and default/maximum TTL are operator-defined. The default
project used by the operator UI retains sandboxes until explicit close unless a
TTL is requested. New named projects default to eight sandboxes, a one-day TTL
and a seven-day maximum. Global capacity defaults to 64 and is configurable with
`--max-sandboxes` / `FAB_MAX_SANDBOXES` (1–4096); size it for the host and datasets.
Expiry denies service access immediately; the cleanup sweep runs every second.
Renewal persists before success and cannot resurrect an expired/deleted sandbox.

Creation keys remain tombstones after deletion, preventing an ambiguous retry
from recreating a closed world. Key retention/compaction is not automated yet.
Mounted mode currently requires direct service routing. Dataset publication is
tracked separately in [issue #20](https://github.com/DumbMachine/fabricate/issues/20)
and is intentionally not implemented here. See the deployment guide for storage
protection, backups, resource-contract upgrades and single-writer ownership.


## Client approval policy

Read-only tools advertise read-only annotations. `service_request` conservatively
advertises write/destructive effects because its method and provider operation
are caller-selected; `sandbox_close` and credential revocation also advertise
destructive effects. Client approval policy still applies. A noninteractive
Codex run with `approval_policy="never"` can reject those calls before they reach
Fabricate. Do not relabel mutations as reads to suppress that protection.

For an authorized unattended test, use Codex's automatic approval review with
`approval_policy="on-request"` and `approvals_reviewer="auto_review"`. Retain a
project-scoped credential and filesystem sandbox. Reviewers can allow or deny
individual actions; server-side project boundaries apply regardless of client
approval settings. Interactive clients can instead prompt for their normal
per-tool approval. No global sandbox bypass is required.
