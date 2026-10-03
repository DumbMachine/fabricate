# Slack contract provenance

`openapi.yaml` is the curated Slack Web API surface this resource implements.
It is not the full vendor document.

Request and response shapes were transcribed on 2026-10-03 from
`specs/slack/openapi.json`. That file is the copy already recorded in
`specs/README.md`: Access's normalized OpenAPI 3 conversion of Slack's official
Web API specification.

- Official source: https://github.com/slackapi/slack-api-specs/blob/master/web-api/slack_web_openapi_v2.json
- Method reference: https://api.slack.com/methods
- Imported file: `specs/slack/openapi.json`
- Declared version in the imported file: `1.7.0`
- SHA-256: `36a285bef59ca6e1666bb027b27557a5660a34dff92873abc7c1e27cb4fb4512`
- Retrieval date for this curated contract: 2026-10-03

Fabricate's resource version is `v2`. Public paths are `/api/<method>` on
`slack.com`, with `HostPrefixes` `{"slack.com": "/api/"}`.

The curated contract keeps the upstream property names for the fields this
resource stores and drops the rest of each object (avatars, blocks, files,
enterprise grids, and undeclared admin methods).

Deviations from the imported document:

- Authentication accepts `Authorization: Bearer` because the Fabricate proxy
  replaces `Authorization` with the synthetic bearer token. The same secret is
  also accepted as the Slack `token` query parameter, the `token` form or JSON
  field, or the `token` header used by `auth.test` in the imported spec.
- `chat.delete` types `ts` as a number, and `conversations.history` /
  `conversations.replies` type `latest` and `oldest` as numbers. Slack
  timestamps are `seconds.microseconds` (`defs_ts` in the imported spec), so
  those fields are strings here.
- `conversations.history` and `conversations.replies` include
  `response_metadata.next_cursor` so a cursor has a next page token. The
  imported success schemas for those two methods do not list that property.
