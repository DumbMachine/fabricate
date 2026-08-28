# Adding a compiled provider HTTP API resource

Provider APIs are compiled resources in the root Go module. Gmail under
`resources/gmail` is the reference layout.

## Required files

```text
resources/<id>/
  openapi.yaml
  oapi-codegen.yaml
  generate.go
  generated/server.gen.go
  resource.go
  scenario.go
  scenario.schema.json
  schema.sql
  server.go
  scenarios/minimal.v1.json
  scenarios/<use-case>.v1.json
  resource_test.go
```

Curate the upstream OpenAPI document to the supported surface. Keep its source
and revision in `UPSTREAM.md`. Generate a strict server with the pinned tool:

```bash
make generate
make generate-check
make docs-operations
```

Implement `httpresource.Resource`:

- a stable descriptor ID and provider host allowlist;
- the embedded OpenAPI and scenario contracts;
- strict scenario lookup and validation;
- SQLite `Initialize`, `Load`, and `Dump` with round-trip fidelity;
- a generated strict server using only injected clock, IDs, secrets, and DB.

Register the resource exactly once in `resources/all/all.go`. Do not add a CLI
registry, engine, image, or environment-variable switch. `make docs-operations`
writes the compiled operation catalog that the integration page must import.

## Scenarios

Scenario documents use `$contract: fabricate.scenario`, contract version 1,
a globally meaningful `$id`, matching `$resource`, explicit resource version,
and an object-valued `state`. Unknown envelope and resource-state fields must
fail validation. Include `minimal.v1` plus at least one realistic workflow.

Tests must prove validation, canonical digest stability, load/dump round trips,
write-then-read behavior, and baseline/live isolation.

## Proxy and official-client proof

Add an environment manifest that selects the resource and scenario. Provider
routes come from `Descriptor.ProviderHosts`; only add exact path routing where
hosts are shared. OAuth behavior belongs in reusable proxy/auth components, not
in a second provider server.

Conformance must include:

1. direct HTTP tests against the generated handler;
2. the provider's official client using its normal production host through
   `fab run --proxy`;
3. mutation followed by a read that observes the new state;
4. request-log assertions and proof that unknown hosts fail closed;
5. redaction tests for provider-specific secrets.

The removed `mockd` service shape is not a compatibility contract. Port data
and behavior intentionally into this format; do not wrap the old router.
