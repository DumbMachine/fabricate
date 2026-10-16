# Outreach contract provenance

`openapi.yaml` is a curated Outreach REST API v2 surface written for
Fabricate. It is not an imported vendor dump and not a third-party OpenAPI
file.

Retrieved 2026-10-03 from the official Outreach API reference:

- https://developers.outreach.io/api/
- https://developers.outreach.io/api/making-requests
- https://developers.outreach.io/api/reference/sequence.md
- https://developers.outreach.io/api/reference/prospect.md
- https://developers.outreach.io/api/reference/sequence-state.md
- https://developers.outreach.io/api/reference/task.md

The stored contract keeps the public paths under `https://api.outreach.io`
(`/api/v2/sequences`, `/api/v2/prospects`, `/api/v2/sequenceStates`,
`/api/v2/tasks`), JSON:API documents (`application/vnd.api+json`), and the
attribute and relationship names those reference pages document. Sequence
states are included because that is how Outreach records a prospect in a
sequence. Resource ids are JSON:API strings. Outreach's published examples
often render ids as numbers.

Live Outreach authenticates with an OAuth bearer token. This resource accepts
`Authorization: Bearer`, which is also the header Fabricate's proxy injects.
Creating a sequence state does not require a mailbox. Pagination links are
omitted; list responses return the matching collection and `meta.count`.
