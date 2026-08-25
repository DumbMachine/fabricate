# OpenAPI source catalog guide

This directory is intentionally spec-only. Do not add runtime behavior,
generated bindings, resource registration, scenarios, or environment manifests
as part of a specification import.

- Prefer an official vendor OpenAPI document. Record its direct source URL,
  declared version, retrieval date when the source is mutable, and SHA-256 in
  `README.md`.
- Reuse Access's locally pinned OpenAPI or Discovery-derived files when they
  already cover the provider. Preserve their provenance; do not silently
  replace them with a different community conversion.
- If a provider publishes only Discovery or GraphQL metadata, record that fact
  and defer import until there is a deliberate, reproducible conversion path.
  Do not substitute an unverified community OpenAPI document merely to fill a
  catalog gap.
- Keep benchmark planning separate from implementation. A provider moves from
  this catalog into `resources/<id>/` only with a curated contract, stateful
  behavior, scenario, and direct/proxy conformance coverage.
