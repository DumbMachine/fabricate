# impact.com contract provenance

The curated surface was taken from the official impact.com Brand API
Reference v14 on 2026-10-03. `openapi.yaml` is that curated surface. It is
not an imported third-party OpenAPI dump.

The path `https://integrations.impact.com/impact-brand/reference` was not
published on that date. The reference index and the pages below are the
source:

- https://integrations.impact.com/brand-api-reference
- https://integrations.impact.com/brand-api-reference/readme/authentication
- https://integrations.impact.com/brand-api-reference/readme/pagination
- https://integrations.impact.com/brand-api-reference/reference/programs/programs
- https://integrations.impact.com/brand-api-reference/reference/partners/partners
- https://integrations.impact.com/brand-api-reference/reference/actions/actions
- https://integrations.impact.com/brand-api-reference/reference/notes/notes
- https://integrations.impact.com/brand-api-reference/reference/conversions/conversions

impact.com authenticates with HTTP Basic: the Account SID is the username
and the Auth Token is the password. Fabricate's proxy overwrites
`Authorization` with `Bearer <token>`, so this resource accepts that bearer
stand-in.

Wire amounts are major currency units (`Payout` 150 and `Currency` INR).
The scenario stores the same commission as `payoutPaise` 15000. Partner ids
such as `partner_northwind` are strings; the published schema types several
partner id fields as integers. Conversions and notes are JSON here; live
impact.com uses form and multipart bodies. List responses are always JSON.
Live impact.com returns XML unless `Accept` is `application/json`.
