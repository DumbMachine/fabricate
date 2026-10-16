# Gupshup contract provenance

The curated surface was taken from the official Gupshup WhatsApp Self Serve
documentation on 2026-10-03:

- https://docs.gupshup.io/docs
- Session text send: https://docs.gupshup.io/reference/session-text-message
- Template send: https://docs.gupshup.io/docs/template-messages
- Message status events: https://docs.gupshup.io/docs/message-events
- Inbound messages: https://docs.gupshup.io/docs/what-is-an-inbound-message and https://docs.gupshup.io/docs/text
- Business details: https://docs.gupshup.io/reference/get-business-details
- Business profile: https://docs.gupshup.io/reference/get-profile-details-1
- Profile about: https://docs.gupshup.io/reference/get-profile-about
- Templates: https://docs.gupshup.io/reference/get-all-templates-for-an-app and https://docs.gupshup.io/reference/get-template-by-template-id
- Mark inbound read: https://docs.gupshup.io/reference/mark-message-as-read

`openapi.yaml` is not an upstream dump and it is not a third-party OpenAPI
file. It keeps the public paths and field names for the operations this
resource implements.

Gupshup authenticates with an `apikey` header. Fabricate also accepts
`Authorization: Bearer <token>` because the proxy replaces `Authorization`
with the synthetic bearer token from secret key `token`. An `apikey` header
is accepted when its value equals that same token.

Production message status arrives as webhook message-events (`enqueued`,
`failed`, `sent`, `delivered`, `read`). Gupshup does not publish a list or
get message API. `GET /wa/api/v1/msg` and `GET /wa/api/v1/msg/{messageId}` are
Fabricate reads over stored messages so a send can be observed later. Status
values use that message-event vocabulary, plus `submitted` (the send
acknowledgement) and `received` (an inbound customer message).

Session send on this surface is a text message. `source` and `destination`
are strings. Some published session schemas type them as integers, while the
template guide and the curl examples send numeric strings.

The Acme world does not assign a WhatsApp Business phone number or app id.
This scenario uses app id `app_acme_goods`, app name `Acme Goods`, and phone
`918041230101`.
