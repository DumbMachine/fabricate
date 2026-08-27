# Neon contract provenance

`openapi.yaml` is a curated Fabricate contract for Neon project list, get, and
create. Operation inventory comes from Neon's official OpenAPI document at
`https://neon.tech/api_spec/release/v2.json`, retrieved 2026-08-27 (SHA-256
`545a747bad71c82333cb8ed36e3c1c424ee56f4cdcc093454cc0e5a425829373`). The stored
document uses bounded object schemas so oapi-codegen can emit strict bindings.

oapi-codegen compiles the implemented operation IDs from this document.
Undeclared Neon routes are not generated and therefore not advertised by the
running server.
