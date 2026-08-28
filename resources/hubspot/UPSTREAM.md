# HubSpot contract provenance

The four JSON files next to this resource are unmodified official HubSpot CRM
v3 OpenAPI documents from
[`HubSpot/HubSpot-public-api-spec-collection`](https://github.com/HubSpot/HubSpot-public-api-spec-collection)
`PublicApiSpecs/CRM/<Object>/Rollouts/424/v3/` on `main` at retrieval date
2026-08-26. HubSpot publishes one specification per CRM object, not a single
mega-file. Each file is stored in full.

| File | Object | SHA-256 |
| --- | --- | --- |
| `contacts.json` | Contacts | `40c68680daa2eba157cf4b1e0183f8c30db349f662efb138874d6505794d85a0` |
| `companies.json` | Companies | `2b5afcbb2def04de0ebd728c512b1c40db7f9a1afbac555b5e88bf196872663e` |
| `deals.json` | Deals | `33480fbc4010f376f9df7b21aabb78ba880b330faa6f6d03607c01c16c19bd7b` |
| `tickets.json` | Tickets | `5cf9ac37b1f285f41d6a6eabfdec521e0d1ed1e37537ae972f3f7165d3fa3c6d` |

The deals document uses HubSpot's object-type-id path `/crm/v3/objects/0-3`.
The running server also accepts the documented alias `/crm/v3/objects/deals`.
oapi-codegen compiles the implemented operation IDs from a merged, gitignored
prepare output. The stored JSON files stay unmodified.
