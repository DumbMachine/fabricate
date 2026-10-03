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

Okta's `apiToken` scheme sends `Authorization: SSWS {API Token}`. Okta also
accepts an OAuth access token as `Authorization: Bearer {access_token}`.
Fabricate's proxy overwrites `Authorization` with `Bearer <token>` from the
secret key `token`. This resource accepts that bearer stand-in and does not
require the `SSWS` prefix.
