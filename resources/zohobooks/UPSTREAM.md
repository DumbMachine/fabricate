# Zoho Books contract provenance

The operation inventory was curated from Zoho's public Books API v3
documentation on 2026-10-03.

- Introduction: https://www.zoho.com/books/api/v3/introduction/
- Contacts: https://www.zoho.com/books/api/v3/contacts/
- Invoices: https://www.zoho.com/books/api/v3/invoices/
- Credit notes: https://www.zoho.com/books/api/v3/credit-notes/
- Customer payments: https://www.zoho.com/books/api/v3/customer-payments/

`openapi.yaml` is the curated surface Fabricate implements (organizations,
contacts, invoices, credit notes, and customer payments). It is not an
imported third-party OpenAPI dump, and it is not the full Books catalog.

Public paths are `/books/v3/...` on `www.zohoapis.com`. The resource claims
only the `/books/` host prefix so Zoho Inventory can use `/inventory/` on the
same host.

Zoho's live API sends `Authorization: Zoho-oauthtoken <access_token>`.
Fabricate accepts `Authorization: Bearer <token>` because the transparent
proxy replaces `Authorization` with the environment's synthetic bearer token.
The Acme organization id is `org-acme`, passed as the `organization_id` query
parameter on every route except the organization list and get calls.

A custom `invoice_number` in a create body is stored as given. Zoho's
production API also requires `ignore_auto_number_generation=true` for that;
the query parameter is accepted and does not change that behavior.
