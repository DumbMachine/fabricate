# BambooHR contract provenance

The curated surface was taken from BambooHR's official API reference at
<https://documentation.bamboohr.com/reference> on 2026-10-03. Getting started,
including HTTP Basic authentication, is documented at
<https://documentation.bamboohr.com/docs/getting-started>.

`openapi.yaml` is not a vendor dump and not a third-party OpenAPI import.
It keeps only the employee, department list, and time off operations this
resource implements. Paths are the public paths on the company host
(`https://{companyDomain}.bamboohr.com/api/v1/...`). Field names and status
values follow those reference pages.

Pages used:

- <https://documentation.bamboohr.com/reference/get-meta-company>
- <https://documentation.bamboohr.com/reference/list-employees>
- <https://documentation.bamboohr.com/reference/get-employee>
- <https://documentation.bamboohr.com/reference/get-employees-directory>
- <https://documentation.bamboohr.com/reference/list-list-fields>
- <https://documentation.bamboohr.com/reference/list-time-off-types>
- <https://documentation.bamboohr.com/reference/list-time-off-requests>
- <https://documentation.bamboohr.com/reference/create-time-off-request>
- <https://documentation.bamboohr.com/reference/update-time-off-request-status>
- <https://documentation.bamboohr.com/reference/list-whos-out>

## Authentication stand-in

BambooHR authenticates with HTTP Basic. The API key is the username and the
password is any string (`curl -u "{API Key}:x"`). Fabricate's proxy overwrites
`Authorization` with `Bearer <token>` on routed hosts, so this resource accepts
that synthetic bearer token from secret key `token` instead of Basic. Do not
send a live BambooHR API key.
