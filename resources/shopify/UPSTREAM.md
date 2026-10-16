# Shopify contract provenance

Shopify does not publish a maintained official OpenAPI description of the
Admin API. This repository previously deferred the Shopify resource for that
reason. This curated Admin REST 2024-10 surface is a small runnable contract
written from the public Admin REST documentation, not an imported community
OpenAPI dump.

- Docs: https://shopify.dev/docs/api/admin-rest
- Retrieval date: 2026-10-03
- Declared version: `2024-10`
- Admin REST is legacy as of 2024-10-01. The paths still follow
  `/admin/api/2024-10/...json`.

Amounts on the wire are decimal rupee strings. Razorpay paise from the Acme
world are not stored. `POST /admin/api/2024-10/fulfillments.json` accepts the
documented `line_items_by_fulfillment_order` payload, and
`fulfillment_order_id` is the order id. There is no FulfillmentOrder resource.
`shipment_status` has no `ndr` value; non-delivery is `attempted_delivery`.
