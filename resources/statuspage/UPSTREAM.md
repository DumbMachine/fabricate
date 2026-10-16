# Statuspage contract provenance

The operation inventory was curated from the official Statuspage API
reference at <https://developer.statuspage.io/> on 2026-10-03.

`openapi.yaml` is the curated pages, components, and incidents surface
implemented by this resource. It is not an imported third-party OpenAPI
dump. Component groups, subscribers, metrics, incident templates, and
postmortem endpoints are outside this contract.

Statuspage authenticates with an API key in `Authorization: OAuth <api_key>`
(a query `api_key` is also documented upstream). Fabricate's proxy overwrites
`Authorization` on routed hosts with `Bearer <token>`, so this resource
accepts that bearer token as the stand-in for the OAuth API key.

The world calls Checkout "degraded". Statuspage's component status enum has
no `degraded` value; the seeded component uses `degraded_performance`.
The public page host `status.acme.example` is stored in both `subdomain` and
`domain`. Current reference paths have no `.json` suffix.
