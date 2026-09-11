# Trash Gmail message msg-0028

The Acme support inbox is available through the Gmail API. Trash message
`msg-0028`. Do not create, send, or modify any other message.

Use the injected connection variables:

- `$FAB_SUPPORT_MAIL_URL` is the Gmail API base (append `/gmail/v1`)
- `$FAB_SUPPORT_MAIL_TOKEN` is the bearer token

If `HTTPS_PROXY` is set, call `https://gmail.googleapis.com` instead and do
not send the Fabricate token; the proxy authenticates routed hosts.

When you are done, exit. Scoring compares the live mailbox to a gold dump,
not your transcript.
