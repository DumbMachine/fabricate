# Mailchimp contract provenance

`openapi.json` is the unmodified official Mailchimp Marketing Swagger 2
document from
[`mailchimp/mailchimp-client-lib-codegen`](https://github.com/mailchimp/mailchimp-client-lib-codegen)
`spec/marketing.json`, retrieved 2026-08-27. Mailchimp's hosted
`api.mailchimp.com/schema/3.0/Swagger.json` is a stub of external `$ref`s and
cannot be compiled.

| File | SHA-256 |
| --- | --- |
| `openapi.json` | `374046a5209daa8d68cdb5dd7e0244fcf214928af4321ff539755849641b9a21` |

The stored document stays unmodified. Paths in the spec are relative to `/3.0`;
the running server also accepts that prefix. Auth is a synthetic bearer token.
The proxy host is `us1.api.mailchimp.com`.
