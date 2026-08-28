# Close contract provenance

`openapi.json` is the unmodified official Close CRM OpenAPI document from
`https://developer.close.com/resources/api-overview/` / the published
`https://api.close.com/api/v1/` schema, retrieved 2026-08-27.

| File | SHA-256 |
| --- | --- |
| `openapi.json` | `48ab63a6026978e419fc0ec6c1660b337cc4a9e0fc9f51c66ad10c2807589195` |

Close paths use trailing slashes. The running server also accepts `/api/v1`
prefixes and adds a trailing slash when a client omits it. Auth is a synthetic
bearer token even though Close's live API uses HTTP Basic.
