# Ironclad contract provenance

The curated surface was taken from Ironclad's public API reference at
<https://developer.ironcladapp.com/> on 2026-10-03. Pages used:

- <https://developer.ironcladapp.com/reference/list-all-workflows>
- <https://developer.ironcladapp.com/reference/retrieve-a-workflow>
- <https://developer.ironcladapp.com/reference/launch-a-new-workflow>
- <https://developer.ironcladapp.com/reference/list-all-comments-in-a-workflow>
- <https://developer.ironcladapp.com/reference/create-a-comment-on-a-workflow>
- <https://developer.ironcladapp.com/reference/list-all-workflow-approvals>
- <https://developer.ironcladapp.com/reference/list-all-records>
- <https://developer.ironcladapp.com/reference/retrieve-a-record>
- <https://developer.ironcladapp.com/reference/update-record-metadata>

`openapi.yaml` is not an imported vendor or third-party dump. It keeps the
published operation ids, path shapes, and field names for workflows, workflow
comments, workflow approvals, and records, and drops the rest of the public
API. The vendor server URL is `https://na1.ironcladapp.com/public/api/v1`
with paths such as `/workflows`. This contract inlines that prefix, so the
paths a client requests on `na1.ironcladapp.com` are `/public/api/v1/...`.

List workflows follows the published default: omitting `status` returns
active workflows only. Repeat `status` to include `completed`, `paused`, or
`cancelled`.

Ironclad production auth is an OAuth bearer token. This resource accepts
`Authorization: Bearer` with the synthetic token from secret key `token`.
`x-as-user-email` and `x-as-user-id` are not required; the bearer token's
actor is the scenario's current user.
