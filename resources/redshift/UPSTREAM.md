# Amazon Redshift Data API contract provenance

Retrieved 2026-10-03 from the official Amazon Redshift Data API reference:

- https://docs.aws.amazon.com/redshift-data/latest/APIReference/Welcome.html
- https://docs.aws.amazon.com/redshift-data/latest/APIReference/API_ExecuteStatement.html
- https://docs.aws.amazon.com/redshift-data/latest/APIReference/API_DescribeStatement.html
- https://docs.aws.amazon.com/redshift-data/latest/APIReference/API_GetStatementResult.html
- https://docs.aws.amazon.com/redshift-data/latest/APIReference/API_ListStatements.html

`openapi.yaml` is a curated Fabricate contract for four operations:
`ExecuteStatement`, `DescribeStatement`, `GetStatementResult`, and
`ListStatements`. It is not an imported third-party OpenAPI dump.

The production protocol is AWS JSON 1.1. Clients `POST /` on
`redshift-data.us-east-1.amazonaws.com` with `Content-Type:
application/x-amz-json-1.1` and `X-Amz-Target: RedshiftData.<Operation>`.
Fabricate accepts that header form and the explicit paths
`POST /ExecuteStatement`, `POST /DescribeStatement`,
`POST /GetStatementResult`, and `POST /ListStatements`.

The real API is SigV4. Fabricate accepts `Authorization: Bearer` because the
proxy injects a bearer token.

The Data API service model published by AWS is `2019-12-20`. This resource's
Fabricate version is `2012-12-01`, the version fixed by the Acme world
contract. Timestamps in responses are Unix epoch seconds, which is the AWS
JSON 1.1 encoding. `WithEvent` is accepted and ignored; Fabricate does not
emit EventBridge events. `SessionId` and `SessionKeepAliveSeconds` are
rejected. `ResultFormat` `CSV` is rejected because `GetStatementResult`
returns JSON.

`ExecuteStatement` runs only a single `SELECT` against `commerce.orders`,
`commerce.payments`, `commerce.shipments`, or `commerce.saas_invoices`. Any
other SQL returns `ValidationException`.
