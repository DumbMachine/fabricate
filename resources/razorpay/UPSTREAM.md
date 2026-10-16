# Razorpay contract provenance

`openapi.yaml` is a curated Razorpay API v1 surface written from the public
API reference on 2026-10-03. It is not an imported OpenAPI document and it is
not a third-party dump.

- Reference: https://razorpay.com/docs/api/
- Orders: https://razorpay.com/docs/api/orders/ and https://razorpay.com/docs/api/orders/create/
- Payments: https://razorpay.com/docs/api/payments/entity/
- Refunds: https://razorpay.com/docs/api/refunds/ and https://razorpay.com/docs/api/refunds/create-normal/
- Payment links: https://razorpay.com/docs/api/payments/payment-links/entity

The host is `api.razorpay.com`. Paths are the public paths a client requests,
including `/v1/payments` and `/v1/payments/{id}/refund`. Amounts are currency
subunits. For INR that is paise.

The curated operations are list, fetch, and create for orders; list and fetch
for payments; create and list refunds, including the refunds nested under a
payment; and list, fetch, create, and cancel for standard payment links.
Checkout payment creation, capture of an `authorized` payment, customers,
settlements, QR codes, subscriptions, disputes, and batch refunds are outside
this surface.

Razorpay's live API authenticates with HTTP Basic (`key_id:key_secret`).
Fabricate accepts `Authorization: Bearer <token>` because the proxy replaces
`Authorization` on routed hosts with a synthetic bearer token. This resource
does not accept HTTP Basic.

Empty `notes` are JSON objects. Live Razorpay sometimes returns an empty
array when an entity has no notes.
