# Chargebee contract provenance

The curated surface was taken from Chargebee's official API v2 reference at
<https://apidocs.chargebee.com/docs/api> on 2026-10-03.

Chargebee publishes a full OpenAPI 3.0.1 document and a Postman collection.
This resource does not import those files. `openapi.yaml` keeps only the
customer, subscription, invoice, transaction, and comment operations
implemented here, using Product Catalog 2.0 shapes (`subscription_items` and
`cancel_for_items`).

Amounts on the wire and in scenario state are minor currency units (cents).
`124000` with `currency_code` `USD` is 1240.00 USD. The Acme site is SaaS
billing in USD. Shop orders denominated in INR are not part of this resource.

Chargebee's live API authenticates with HTTP Basic: the API key is the
username and the password is empty. Fabricate's proxy overwrites
`Authorization` with `Bearer <token>` from secret key `token`, so this
resource accepts that synthetic bearer token as the stand-in for Basic.
