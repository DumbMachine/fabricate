# fabricate development guide

Fabricate has two product planes. Do not merge their authoring models.

## Repository map

```text
cmd/fab/             CLI entrypoint
cli/                 Cobra commands
engine/              infrastructure engines plus shared engine/http runtime
environment/         foreground environment supervisor and proxy composition
httpresource/        provider-independent compiled HTTP resource contract
requestlog/          durable redacted JSONL request traces
resources/<id>/      OpenAPI, generated strict bindings, SQLite state, behavior
resources/all/       single compile-time official-resource registry
scenario/            strict immutable scenario envelope and digest
environments/        runnable multi-service environment manifests
profile/, profiles/  container-engine profile catalog only
target/              Docker/Kubernetes provisioning for container engines
```

## HTTP API rule

Every provider API is a package under `resources/<id>` implementing
`httpresource.Resource`. It owns its curated OpenAPI contract, generated strict
server, schema, scenario codec, behavior, and scenarios. Register it once in
`resources/all/all.go`.

Do not reintroduce `mockd`, `engine/httpmock`, `MOCK_SERVICE`, provider-specific
engines, provider Docker images, or endpoint-only test doubles. The Gmail
resource is the reference implementation.

Provider correctness requires both direct HTTP tests and a transparent-proxy
test with the official client using its normal production hostname. Proxy
routes are derived from the resource descriptor and remain fail closed.

## Infrastructure rule

Profiles and `fab create` are for actual infrastructure/container engines.
Adding an infrastructure engine requires `engine.Engine`, registry wiring in
`cli/engines.go`, at least one profile, and lifecycle tests. Keep destroy
idempotent and preserve the state-lock behavior.

## Verification

```bash
make check
make generate-check
```

Run `make e2e` when container lifecycle changes. Run the relevant official
client application through `fab-dev run --environment ... --proxy -- ...` when
an HTTP resource, authentication flow, CA handling, or proxy behavior changes.

Informational output goes to stderr; machine-readable payloads go to stdout.
Never log secrets. Request logs must retain payload usefulness while redacting
authorization, cookies, OAuth tokens, client secrets, and secret-shaped fields.
