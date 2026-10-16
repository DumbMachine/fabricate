# Canny contract provenance

The operation inventory was curated from the official Canny API reference at
<https://developers.canny.io/api-reference> on 2026-10-03.

Canny does not publish an OpenAPI document for this surface. `openapi.yaml` is
not a third-party dump. It keeps the v1 POST routes for boards, posts, users,
and votes. Each route is `POST /api/v1/<resource>.<action>` with a JSON body,
which is the method and content type the reference requires.

Canny's live API authenticates by sending a secret `apiKey` field in that
body. Fabricate ignores `apiKey`. Every operation requires
`Authorization: Bearer <token>`, where the token is the synthetic secret key
`token`. The transparent proxy replaces `Authorization` on `canny.io` with
that bearer token, so a client that only knew how to send `apiKey` still
works once the proxy is in front. A body `apiKey` is optional and is not
checked.

The descriptor host is `canny.io` with `HostPrefixes` `{"canny.io": "/api/v1/"}`.
v2 list routes (`/api/v2/users/list`, `/api/v2/votes/list`,
`/api/v2/status_changes/list`) sit outside that prefix and are not
implemented. Votes are retrieved with `POST /api/v1/votes/retrieve`.

The curated records omit categories, tags, comments, companies, changelog
entries, opportunities, ideas, and third-party links (ClickUp, Jira, Linear,
Zendesk). Board responses omit the private board token. `score` is the number
of stored votes, not Canny's weighted score. `trending` follows that vote
count, then creation time. `relevance` is accepted only with `search` and
then uses the same order as `newest`. Status-change comments and
`shouldNotifyVoters` are accepted and not stored; no notification is sent.
Custom status strings are stored as given.

The Acme CSV-export post uses the same title as Asana task `task-csv-export`.
The post object has no external-task field, so that Asana id is not stored.
