# Okta contract provenance

The curated operations were transcribed from the official Okta Management
OpenAPI cataloged in [`specs/README.md`](../../specs/README.md).

- Catalog: `specs/README.md`
- File: `specs/okta/openapi.yaml`
- Provenance: [Okta's official Management OpenAPI](https://github.com/okta/okta-management-openapi-spec) `management-minimal.yaml`, downloaded 2026-08-25
- Declared upstream version: `2026.08.1`
- SHA-256: `457a82fd913b3858abda99191d6b7ed6b8747447f98266d4d0dd19dcbf781a74`
- Curation date: 2026-10-03

`openapi.yaml` keeps only the user, group, and application operations this
resource implements. It is not a copy of the upstream document. The Fabricate
resource version is `2024-07`, the Acme world contract, on host `acme.okta.com`.

## Authentication

Okta's `apiToken` scheme sends `Authorization: SSWS {API Token}`, and the
official SDKs (`@okta/okta-sdk-nodejs`, okta-sdk-golang) default to it. Okta
also accepts an OAuth access token as `Authorization: Bearer {access_token}`.
The contract declares both schemes. A direct client configured like a real
app sends `SSWS <token>` with the environment's secret key `token`; through
the transparent proxy, Fabricate overwrites `Authorization` with
`Bearer <token>`, and this resource accepts that bearer stand-in. Each scheme
must use its exact prefix and the synthetic token.

The official-client check is `conformance/okta-sdk.mjs`
(`@okta/okta-sdk-nodejs`, pinned in `conformance/sdk.json`). It pages users
with `limit=3` through the SDK's collection iterator, which follows only the
`rel="next"` links described below.

## Pagination

List endpoints take `limit` (1 to 200, default 200) and the opaque `after`
cursor, which is the id of the last object on the previous page. Like Okta,
every list response carries a `rel="self"` link, and a `rel="next"` link when
more results exist, as separate `Link` header fields:

```text
Link: <https://acme.okta.com/api/v1/users?limit=5>; rel="self"
Link: <https://acme.okta.com/api/v1/users?after=00u-leo&limit=5>; rel="next"
```

The next link keeps the request's other query parameters (`q`, `search`,
`filter`, `limit`) with keys sorted; the last page has no `rel="next"`. Links
are absolute: through the transparent proxy the host is `acme.okta.com` over
`https`; direct requests use the local listener's scheme and `host:port`. The
generated strict response writer can only set one `Link` value, so the
handlers add the separate fields themselves.
