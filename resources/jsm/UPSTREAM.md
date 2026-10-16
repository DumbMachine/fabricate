# Jira Service Management contract provenance

The operation inventory was curated from the official Jira Service Management
Cloud REST API reference on 2026-10-03:

https://developer.atlassian.com/cloud/jira/service-desk/rest/

`openapi.yaml` keeps the service desk, customer request, and comment
operations this resource implements. It is not the Jira platform spec under
`specs/jira`, and it is not a third-party OpenAPI dump.

Ad-hoc Jira Cloud calls authenticate with HTTP Basic (the account email and
an API token). OAuth 2.0 (3LO) calls send `Authorization: Bearer`. Fabricate's
proxy overwrites `Authorization` with `Bearer <token>`, so this resource
accepts that synthetic bearer token as the stand-in for both.

Comment ids are strings on this surface. The published comment path parameter
is an integer; clients should use the string `id` returned by the API. The
curated create-request body accepts string `summary` and `description` values
only.
