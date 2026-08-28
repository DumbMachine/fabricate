---
name: fabricate-contributing
description: Contribute a compiled HTTP API resource, scenario, environment, infrastructure profile, or infrastructure engine to Fabricate.
allowed-tools: Bash(make:*), Bash(go:*), Bash(fab-dev:*)
---

# Contributing to Fabricate

Choose the extension boundary first:

- provider HTTP API: `resources/<id>` implementing `httpresource.Resource`;
- provider use case: strict versioned JSON scenario in that resource;
- runnable provider composition: `environments/*.yaml`;
- new seed data for existing infrastructure: `profiles/<engine>/<name>`;
- new infrastructure product: `engine/<id>` implementing `engine.Engine`.

For HTTP APIs, copy the structure—not provider behavior—from `resources/gmail`.
Curate OpenAPI, generate strict bindings, implement SQLite load/dump and
behavior, register once in `resources/all`, and prove direct plus official-client
transparent-proxy conformance. Never recreate `mockd`, `engine/httpmock`,
provider Docker images, or service-specific CLI registries.

Run `make check` and `make generate-check`. After OpenAPI or handler changes,
run `make docs-operations` so integration pages list the compiled surface
and catalog scenario counts.
Run `make e2e` for container engine lifecycle changes and `make conformance
<resource>` for HTTP API client coverage (writes `<id>.compatibility.json`).
`make docs-examples` recaptures documented command snapshots. Operation
catalogs, scenario catalogs, command snapshots, and compatibility reports land
under `packages/docs-content/resources/_generated/`.
