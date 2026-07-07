---
name: fabricate-google-play
description: Run a stateful local mock of the Google Play developer APIs with fabricate (fab create google-play) — app discovery plus review list/get/reply. Use when developing or testing a support agent that answers Play Store reviews without a real Google Play account.
allowed-tools: Bash(fab:*), Bash(curl:*)
---

# Google Play mock (fabricate)

A SQLite-backed subset of Play Developer Reporting + Android
Publisher. Replying to a review mutates it: the next list shows the
developer comment and the unanswered backlog shrinks.

## Start

```bash
fab create google-play -p reviews-demo    # 3 apps + an unanswered-review backlog
eval "$(fab creds reviews-demo --env)"    # FAB_URL, FAB_PASSWORD
```

## Supported endpoints

- `GET  /v1beta1/apps:search` — apps the account can see
- `GET  /androidpublisher/v3/applications/{pkg}/reviews` — paginated
  (`maxResults`, `token`)
- `GET  /androidpublisher/v3/applications/{pkg}/reviews/{reviewId}`
- `POST /androidpublisher/v3/applications/{pkg}/reviews/{reviewId}:reply`
  — `{"replyText": "..."}`

Responses use the real Android Publisher envelopes (`comments[]` with
`userComment` / `developerComment`). Auth is not validated.

## Example loop

```bash
curl -s "$FAB_URL/v1beta1/apps:search" | jq '.apps[].name'

PKG=$(curl -s "$FAB_URL/v1beta1/apps:search" | jq -r '.apps[0].name | ltrimstr("apps/")')
curl -s "$FAB_URL/androidpublisher/v3/applications/$PKG/reviews?maxResults=5" \
  | jq '.reviews[] | {id: .reviewId, comments: (.comments | length)}'   # 1 comment = unanswered

RID=$(curl -s "$FAB_URL/androidpublisher/v3/applications/$PKG/reviews" | jq -r '.reviews[0].reviewId')
curl -s -X POST "$FAB_URL/androidpublisher/v3/applications/$PKG/reviews/$RID:reply" \
     -d '{"replyText": "Thanks — fixed in 2.4.2, rolling out now."}'

# The reply is now the review's developerComment (comments length = 2).
curl -s "$FAB_URL/androidpublisher/v3/applications/$PKG/reviews/$RID" | jq '.comments | length'
```

## Fixture shape (seed.json)

```bash
fab profiles show reviews-demo --files       # note: the demo fixture is large
fab profiles show reviews-demo --file seed.json --head 30
```

Top-level keys: `apps` (packageName/displayName), `reviews`
(reviewId/packageName/author/text/starRating/language/device/
appVersion/lastModified/developerReply/replyModified).
