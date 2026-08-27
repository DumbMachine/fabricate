# Vercel contract provenance

`openapi.yaml` is a curated Fabricate contract for Vercel project list, get, and
create. Operation inventory comes from Vercel's official OpenAPI document at
`https://openapi.vercel.sh`, retrieved 2026-08-27 (SHA-256
`7b02701e08667d0e86b7e85ae5d6ec612046b33a4c18465f15553487945b3368`). The stored
document replaces the vendor's generated generic schemas with bounded objects
so oapi-codegen can emit strict bindings.

oapi-codegen compiles the implemented operation IDs from this document.
Undeclared Vercel routes are not generated and therefore not advertised by the
running server.
