# Google Analytics 4 contract provenance

The operation inventory was curated from the official Google Analytics Data API
discovery document.

- URL: https://analyticsdata.googleapis.com/$discovery/rest?version=v1beta
- Retrieval date: 2026-10-03
- Discovery `revision`: `20260930`
- Discovery SHA-256: `323e4f7653f8949fe4798a080c4ef1af9e2e4e525eb1fde82e2fb1804c94757f`

`openapi.yaml` is the curated surface Fabricate implements. It is not a copy
of the discovery document and it is not a community OpenAPI dump. Request and
response field names match the discovery schemas for the fields this resource
accepts. `int64` request fields (`limit`, `offset`) are JSON strings, matching
Google's JSON mapping.

Implemented discovery methods:

| operationId | Method and path |
| --- | --- |
| `properties.runReport` | `POST /v1beta/properties/{property}:runReport` |
| `properties.batchRunReports` | `POST /v1beta/properties/{property}:batchRunReports` |
| `properties.runRealtimeReport` | `POST /v1beta/properties/{property}:runRealtimeReport` |
| `properties.checkCompatibility` | `POST /v1beta/properties/{property}:checkCompatibility` |
| `properties.getMetadata` | `GET /v1beta/properties/{property}/metadata` |
| `properties.audienceExports.list` | `GET /v1beta/properties/{property}/audienceExports` |
| `properties.audienceExports.get` | `GET /v1beta/properties/{property}/audienceExports/{audienceExport}` |
| `properties.audienceExports.create` | `POST /v1beta/properties/{property}/audienceExports` |
| `properties.audienceExports.query` | `POST /v1beta/properties/{property}/audienceExports/{audienceExport}:query` |

The Data API has no `properties.get`. Property display name, currency, and time
zone live in the scenario. `properties.getMetadata` is the property-scoped
read from this discovery document. The Admin API `properties.get` is a
different host and is not part of this resource.

## Curated behavior

- The path parameter is the numeric property id, so a client calls
  `POST /v1beta/properties/309712345:runReport`. Discovery templates the same
  URL as `v1beta/{+property}:runReport` with `property` = `properties/309712345`.
- Authorization is `Authorization: Bearer`. The live Data API accepts OAuth
  bearer tokens. Fabricate's proxy overwrites `Authorization` on
  `analyticsdata.googleapis.com` with the synthetic service token.
- `properties.runReport` and `properties.batchRunReports` aggregate scenario
  events. Dimensions: `transactionId`, `sessionSource`, `sessionMedium`,
  `sessionCampaignName`, `eventName`, `date`, `dateRange`. Metrics:
  `eventCount`, `transactions`, `purchaseRevenue`, `itemRefundAmount`,
  `totalRevenue`. Empty stored dimension values are returned as `(not set)`.
  `totalRevenue` is `purchaseRevenue - itemRefundAmount`.
- Currency conversion is not applied. `metadata.currencyCode` and
  `metadata.timeZone` are the property's scenario values. Relative dates
  (`today`, `yesterday`, `NdaysAgo`) use that time zone and the injected clock.
- Seeded event rows are returned even when every requested metric is zero, so
  a refund event stays visible. The live API omits all-zero rows unless
  `keepEmptyRows` is true.
- Realtime reports accept the realtime dimension and metric subset. Seeded
  events have a date and no minute, so they are outside the realtime window
  and the response rows are empty.
- `properties.audienceExports.create` is the persisted write on
  `analyticsdata.googleapis.com`. The live method returns a long-running
  Operation. This resource completes it immediately (`done: true`, `state:
  ACTIVE`) and stores the `AudienceExport`. A following get, list, or query
  observes it. No user-level audience members are seeded, so `query` returns
  `audienceRows: []`.
- Cohort specs, comparisons, pivots, dimension expressions, metric expressions,
  and property quota from the discovery document are outside this surface.
  Unknown request fields are rejected.
