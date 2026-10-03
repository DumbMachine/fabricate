# Google Drive contract provenance

`openapi.yaml` is a curated subset of the pinned Drive v3 description at
`specs/google-drive/openapi.json`. That file is Access's OpenAPI 3 conversion
of Google's official Drive Discovery document:

https://www.googleapis.com/discovery/v1/apis/drive/v3/rest

- Pinned file: `specs/google-drive/openapi.json`
- Declared version: `v3`
- SHA-256: `a6f8fbd46dbc16cda045724ad74e2ef1b67266cc949eeb8d4941bc34156094cf`
- Curation date: 2026-10-03

The stored contract is the curated surface (about, files, and permissions),
not the full Drive API. Paths in the pinned file are relative to
`https://www.googleapis.com/drive/v3` and are rewritten here to public paths
beginning with `/drive/v3/` on `www.googleapis.com`. Field names for the
implemented operations are taken from the pinned specification.

Media upload (`/upload/drive/v3`) is not part of this contract. `files.create`
persists metadata. Seeded file bytes live in the scenario `body` and are
copied by `files.copy`. `files.get` returns metadata, including `description`.
`files.copy` puts the copy in My Drive root when `parents` is omitted, and
folders cannot be copied. `files.delete` also deletes descendants. When
`orderBy` is omitted, `files.list` sorts by `name`, then `id`.
