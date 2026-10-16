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

Absent optional scalars are empty strings. Interview kits on job stages are
not stored; `interviews` is always an empty array.
Activity-feed emails and activities are part of the response shape and stay
empty. Openings, custom-field definitions, and the rest of Harvest are outside
this curated contract.

## Pagination

List endpoints use `page` (1-based, default 1) and `per_page` (1 to 500,
default 100), and return Harvest's RFC 8288 `Link` header as one
comma-separated value:

```text
Link: <https://harvest.greenhouse.io/v1/candidates?page=1&per_page=2>; rel="prev", <https://harvest.greenhouse.io/v1/candidates?page=3&per_page=2>; rel="next", <https://harvest.greenhouse.io/v1/candidates?page=4&per_page=2>; rel="last"
```

`prev` appears after page 1, `next` appears while more pages remain, and
`last` is always present (page 1 for an empty or single-page list). Each link
keeps the request's other query parameters, such as `email`, `job_id`, or
`status`, and query keys are sorted. Links are absolute: through the
transparent proxy the host is `harvest.greenhouse.io` over `https`; direct
requests use the local listener's scheme and `host:port`. Harvest's
`skip_count` option is not implemented.
