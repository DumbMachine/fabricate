# Vanta contract provenance

The curated surface was taken from the official Manage Vanta reference at
https://developer.vanta.com/ on 2026-10-03. `openapi.yaml` is not a
third-party dump and it is not the full Manage Vanta document. It keeps the
published operation ids, field names, and enums for vendors, tests, and
documents.

- https://developer.vanta.com/api-reference/vendors/list-vendors
- https://developer.vanta.com/api-reference/vendors/get-vendor-by-id
- https://developer.vanta.com/api-reference/vendors/list-security-reviews-by-vendor-id
- https://developer.vanta.com/api-reference/tests/list-tests
- https://developer.vanta.com/api-reference/tests/get-test-by-id
- https://developer.vanta.com/api-reference/tests/get-test-entities-by-test-id
- https://developer.vanta.com/api-reference/documents/list-documents
- https://developer.vanta.com/api-reference/documents/get-document-by-id
- https://developer.vanta.com/api-reference/documents/create-a-custom-document
- https://developer.vanta.com/api-reference/documents/list-documents-uploads

Upstream lists those paths on server `https://api.vanta.com/v1`. This
contract advertises the full public paths (`/v1/...`) on host
`api.vanta.com`.

Vendor status values published for `status` are `MANAGED`, `ARCHIVED`, and
`IN_PROCUREMENT`. Decision status values are `APPROVED`,
`CONDITIONALLY_APPROVED`, and `NOT_APPROVED`. There is no `in_review` value.
Shiprocket is a managed vendor whose `latestDecision` is null and whose
security review has no completion date and no decision. Razorpay's
`latestDecision.status` is `APPROVED`.

Tests are not tagged with a framework, control, or rollout flag. A
`frameworkFilter`, `controlFilter`, or `isInRollout=true` therefore matches
no seeded test. Documents are not tagged with a framework, so
`frameworkMatchesAny` matches no seeded document. `entityStatus` defaults to
`FAILING`, matching the published list-entities behavior.

Authentication on the wire is `Authorization: Bearer`. Production clients
obtain that bearer from `POST https://api.vanta.com/oauth/token`. This
resource does not implement the token endpoint. Direct calls send the
synthetic bearer Fabricate injects; the proxy overwrites `Authorization` on
`api.vanta.com` with the same bearer.
