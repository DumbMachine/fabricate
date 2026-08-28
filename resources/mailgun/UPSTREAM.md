# Mailgun contract provenance

`openapi.yaml` is the unmodified official Mailgun OpenAPI document from
`https://raw.githubusercontent.com/mailgun/mailgun-go/master/docs/openapi.yaml`
(and the published Mailgun API schema), retrieved 2026-08-27.

| File | SHA-256 |
| --- | --- |
| `openapi.yaml` | `e50f01dbb8fde9c60d8f17749b0053c12eadd74147d64877460d49fa10e26202` |

The stored document stays unmodified. Send-message is `multipart/form-data` in
the spec; the mock also accepts `application/x-www-form-urlencoded`. Auth is a
synthetic bearer token even though Mailgun's live API uses HTTP Basic.
