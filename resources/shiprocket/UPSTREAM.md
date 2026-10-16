# Shiprocket contract provenance

The curated surface was taken from Shiprocket's official API documentation at
<https://apidocs.shiprocket.in/> on 2026-10-03.

That site publishes the Shiprocket external REST collection (host
`apiv2.shiprocket.in`, prefix `/v1/external`). On 2026-10-03 the documentation
host returned HTTP 502, so the operation inventory was read from Shiprocket's
public Postman workspace for the same collection
(`shiprocketdev` / Shiprocket API). `openapi.yaml` keeps only the logistics
operations this resource implements. It is not an imported third-party OpenAPI
dump.

## Authentication

Production Shiprocket exchanges an API user's email and password at
`POST /v1/external/auth/login` for a JWT (about 10 days) and then requires
`Authorization: Bearer <token>`.

Fabricate's proxy replaces `Authorization` with `Bearer <synthetic token>`
from the secret key `token`. This resource checks that bearer on every route
except login. Login is implemented and returns the official login shape
(`id`, `company_id`, `email`, `first_name`, `last_name`, `created_at`,
`token`). The `token` value is the same synthetic bearer. Any non-empty
password is accepted. The password is not checked against a Shiprocket
account and is not stored or logged.

Pincodes and phone numbers are strings so Acme world values such as
`+91-98450-11223` round-trip. Shiprocket's published examples often type
those fields as integers. State and country use the Acme world codes
(`KA`, `MH`, `IN`).
