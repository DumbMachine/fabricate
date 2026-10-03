# Gainsight contract provenance

The curated surface was taken from Gainsight's public NXT REST articles on
2026-10-03. Fabricate does not import a third-party OpenAPI dump.

- Company: https://support.gainsight.com/gainsight_nxt/API_and_Developer_Docs/Company_and_Relationship_API/Company_API_Documentation
- Call to Action: https://support.gainsight.com/gainsight_nxt/API_and_Developer_Docs/Cockpit_API/Call_To_Action_(CTA)_API_Documentation
- Success Plan: https://support.gainsight.com/gainsight_nxt/API_and_Developer_Docs/Success_Plan_APIs/Success_Plan_APIs
- Timeline: https://support.gainsight.com/gainsight_nxt/API_and_Developer_Docs/Timeline_API/Timeline_APIs

Production calls send an `accesskey` header. This resource accepts
`Authorization: Bearer` because the Fabricate proxy replaces `Authorization`
on routed hosts with the environment token.

`openapi.yaml` keeps the Company query, insert, update, and delete operations,
Cockpit CTA list, create, and update, Success Plan list and create, Timeline
activity save, and the activity_timeline read. Relationship and
GS_Opportunity timeline contexts are named in the Timeline article and are
not part of this surface.

The Company parameter table has no standard health-color or domain column.
Scenarios store those as the custom fields `Health__gc` (Red, Yellow, Green)
and `Domain__gc`. Company Read is `POST /v1/data/objects/query/Company`; a
single company is that query filtered by `Gsid`. Date fields are returned as
strings. Some Company insert samples show epoch milliseconds for contract
dates; those epoch values are not reproduced. `CreatedDate` and
`ModifiedDate` are Unix milliseconds.
