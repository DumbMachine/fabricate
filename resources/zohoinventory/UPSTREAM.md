# Zoho Inventory contract provenance

The operation inventory was curated from Zoho's public Inventory API v1
documentation on 2026-10-03.

- Introduction: https://www.zoho.com/inventory/api/v1/
- Items: https://www.zoho.com/inventory/api/v1/items/
- Sales orders: https://www.zoho.com/inventory/api/v1/salesorders/
- Purchase orders: https://www.zoho.com/inventory/api/v1/purchaseorders/
- Warehouses: https://www.zoho.com/inventory/api/v1/multi-warehouse/

`openapi.yaml` is the curated surface Fabricate implements (organizations,
warehouses, items, sales orders, and purchase orders). It is not an imported
third-party OpenAPI dump, and it is not the full Inventory catalog.

Public paths are `/inventory/v1/...` on `www.zohoapis.com`. The resource claims
only the `/inventory/` host prefix so Zoho Books can keep `/books/` on the
same host.

Zoho's live API sends `Authorization: Zoho-oauthtoken <access_token>`.
Fabricate accepts `Authorization: Bearer <token>` because the transparent
proxy replaces `Authorization` with the environment's synthetic bearer token.
The Acme organization id is `org-acme`, passed as the `organization_id` query
parameter on every route except the organization list and get calls.

The published sales-order list example also requires `salesorder_ids`. That
parameter belongs to the bulk delete on the same page. This list does not
require it. Item responses include `stock_on_hand` (a documented sort column,
and present on item variants) and a `warehouses` array using
`warehouse_stock_on_hand` from the multi-warehouse API. The 2026 item example
shows `locations` instead. Line items include `sku`, which the items API
documents and the sales-order line example omits.

`POST /purchaseorders/{purchaseorder_id}/approve` is documented to return
`{code, message}` and does not name the resulting status. This resource sets
`status` to `approved` and appends a purchase-order comment
(`operation_type` `approved`, `commented_by` the stored approver). The Acme
purchase order uses `pending_approval`, the approval-workflow status named
for PO-7721. Published examples on the purchase-order page use values such
as `draft` and `Partially_Received`.
