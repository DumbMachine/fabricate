# Greenhouse Harvest contract provenance

Curated from the official Harvest API v1 reference at
https://developers.greenhouse.io/harvest.html, retrieved 2026-10-03.

`openapi.yaml` is not a third-party dump. It keeps the Harvest paths and
field names for the recruiting surface this resource implements: jobs, job
stages, candidates, applications, offers, scorecards, users, activity-feed
notes, and the note create call.

Harvest authenticates with HTTP Basic. The username is the API key and the
password is empty. This resource accepts `Authorization: Bearer <token>`
because Fabricate's proxy replaces `Authorization` on routed hosts with the
environment's synthetic bearer token. `On-Behalf-Of` is still required on
note creates, matching Harvest.

Published Harvest examples use integer ids. This curated surface types ids as
strings so Acme keys such as `job-support`, `app-jordan`, and `offer-jordan`
round-trip without translation.

Absent optional scalars are empty strings. Pagination uses `page` and
`per_page`. This surface does not emit RFC 5988 `Link` headers. Interview
kits on job stages are not stored; `interviews` is always an empty array.
Activity-feed emails and activities are part of the response shape and stay
empty. Openings, custom-field definitions, and the rest of Harvest are outside
this curated contract.
