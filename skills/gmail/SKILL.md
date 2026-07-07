---
name: fabricate-gmail
description: Run a stateful local mock of the Gmail API v1 with fabricate (fab create gmail). Use when developing or testing an agent/app that triages an inbox, reads threads, sends replies, or manages labels — without a real Google account.
allowed-tools: Bash(fab:*), Bash(curl:*)
---

# Gmail mock (fabricate)

A SQLite-backed Gmail API v1 subset. Writes take effect:
`messages.send` appends to the thread, `modify` flips labels, and the
next search reflects both.

## Start

```bash
fab create gmail -p support-inbox        # built-in: support backlog to triage
eval "$(fab creds support-inbox --env)"  # FAB_URL, FAB_PASSWORD
```

`support-inbox` seeds support@acme.dev with 10 messages: 5 unread
(double-charge complaint + follow-up, a bug report, a refund request,
a starred VIP escalation) plus handled threads and newsletters as
noise. Custom mailbox: `fab profiles init gmail my-inbox --from
support-inbox`, edit `seed.json`, create.

## Supported endpoints (base: /gmail/v1/users/{userId}, userId ignored)

- `GET  .../profile`, `GET .../labels`, `GET .../labels/{id}` (with counts)
- `GET  .../messages` — `q`, `labelIds`, `maxResults`, `pageToken`
- `GET  .../messages/{id}` — full payload, body as base64url
- `POST .../messages/{id}/modify` — `{"addLabelIds":[], "removeLabelIds":[]}`
- `POST .../messages/{id}/trash`
- `POST .../messages/send` — `{"raw": "<base64url RFC 822>", "threadId": "..."}`
- `GET  .../threads`, `GET .../threads/{id}`

`q` operators: `is:unread`, `is:starred`, `in:<label>`, `label:<x>`,
`from:`, `to:`, `subject:` (substring), bare words (subject+body).
Unknown operators are ignored. TRASH is excluded unless asked for.
Auth is not validated.

## The triage loop

```bash
curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread" | jq .resultSizeEstimate   # 5

curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge" | jq '.messages | length'  # 2

# Reply — raw is base64url-encoded RFC 822:
RAW=$(printf 'To: dana@northwind.io\nSubject: Re: Charged twice\n\nRefund is on its way.' \
      | base64 | tr '+/' '-_' | tr -d '=\n')
curl -s -X POST "$FAB_URL/gmail/v1/users/me/messages/send" \
     -d "{\"raw\": \"$RAW\", \"threadId\": \"thr-doublecharge\"}"

curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge" | jq '.messages | length'  # 3

curl -s -X POST "$FAB_URL/gmail/v1/users/me/messages/msg-0002/modify" \
     -d '{"removeLabelIds": ["UNREAD"]}'
curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread" | jq .resultSizeEstimate   # 4
```

Message bodies decode with base64url:
`jq -r .payload.body.data | base64 -d` (add `tr '_-' '/+'` first on
strict decoders).

## Fixture shape (seed.json)

```bash
fab profiles show support-inbox --file seed.json --head 40
```

Top-level keys: `emailAddress`, `labels` (system labels INBOX/UNREAD/
SENT/TRASH/STARRED/IMPORTANT are auto-added), `messages`
(id/threadId/from/to/subject/body/labelIds/internalDate-epoch-millis).
