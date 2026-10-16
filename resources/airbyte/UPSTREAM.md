# Airbyte contract provenance

Official reference: https://reference.airbyte.com/

Retrieved: 2026-10-03

`openapi.yaml` is a curated subset of the Airbyte API v1 reference. It is not
an imported OpenAPI dump. Pages used:

- https://reference.airbyte.com/reference/getting-started
- https://reference.airbyte.com/reference/listsources
- https://reference.airbyte.com/reference/listdestinations
- https://reference.airbyte.com/reference/listconnections
- https://reference.airbyte.com/reference/listjobs
- https://reference.airbyte.com/reference/getjob
- https://reference.airbyte.com/reference/createjob

The stored contract keeps the public host `api.airbyte.com`, version `v1`,
and the operation ids `listSources`, `getSource`, `listDestinations`,
`getDestination`, `listConnections`, `getConnection`, `listJobs`, `getJob`,
and `createJob`. Paths are the full public paths (`/v1/...`).

Deviations required by the Acme world:

- Ids are the synthetic strings `source_shopify`, `source_razorpay`,
  `source_shiprocket`, `dest_redshift`, `conn_shopify_goods`,
  `conn_razorpay`, `job_10482`, and `job_fail_ndr`. The published schemas type
  resource ids as UUIDs and `jobId` as int64.
- The published `JobResponse` requires `connectionId` and has no failure text.
  `job_fail_ndr` failed for `source_shiprocket`, and the world names no
  Shiprocket connection, so `connectionId` is optional, `sourceId` names that
  source, and `failureReason` carries the error text for shipment `10483`.
- Destination configuration stores the Acme warehouse cluster
  `acme-warehouse` in `host`, with `database` `analytics` and `schema`
  `commerce`. Those are the published Redshift destination fields.
- `workspaceId` `ws_acme` fills the required workspace field. The world doc
  does not name an Airbyte workspace.
- Connector configuration unions are bounded to the fields this resource
  stores.
- Auth is the synthetic bearer token from secret key `token`. Airbyte's live
  API also uses `Authorization: Bearer`.
