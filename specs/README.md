# OpenAPI source catalog

This directory contains provider API descriptions only. Adding a file here does
not register a Fabricate resource, start a service, generate bindings, or claim
that the provider is emulated. It is the input catalog for the next supported
integrations.

## Imported specifications

| Provider | File | Provenance | Version | SHA-256 |
| --- | --- | --- | --- | --- |
| Slack | `slack/openapi.json` | Access's normalized OpenAPI 3 copy of [Slack's official Web API specification](https://github.com/slackapi/slack-api-specs/blob/master/web-api/slack_web_openapi_v2.json) | `1.7.0` | `36a285bef59ca6e1666bb027b27557a5660a34dff92873abc7c1e27cb4fb4512` |
| Google Calendar | `google-calendar/openapi.json` | Access's OpenAPI 3 conversion of [Google's official Calendar Discovery document](https://www.googleapis.com/discovery/v1/apis/calendar/v3/rest) | `v3` | `38165c6389efb54531431a47e6e4d501d7057c68fa58fe750189fa1fdbcde7ab` |
| Google Drive | `google-drive/openapi.json` | Access's OpenAPI 3 conversion of [Google's official Drive Discovery document](https://www.googleapis.com/discovery/v1/apis/drive/v3/rest) | `v3` | `a6f8fbd46dbc16cda045724ad74e2ef1b67266cc949eeb8d4941bc34156094cf` |
| Google Sheets | `google-sheets/openapi.json` | Access's OpenAPI 3 conversion of [Google's official Sheets Discovery document](https://sheets.googleapis.com/$discovery/rest?version=v4) | `v4` | `1d8ae01779e68d8b64094dcb08176f84b968bd6005ef01e90f303da32272ac97` |
| Jira Cloud | `jira/openapi.json` | Access's pinned copy of [Atlassian's official Jira Cloud OpenAPI](https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json) | `1001.0.0-SNAPSHOT-cdb1aeb9e06abec2915aeac4d71b1942463211c5` | `377ddb805bfc6efb96c5233f896891ef53f22689ff1b08281e6b1a20310ad4e6` |
| Stripe | `stripe/openapi.json` | Access's pinned copy of [Stripe's official OpenAPI specification](https://github.com/stripe/openapi) | `2026-07-29.dahlia` | `44dba30c9226fe6b3650a8860cfbafac46ce8d5d6cf37728a84ee59294974687` |
| Confluence Cloud | `confluence/openapi.json` | [Atlassian's official Confluence Cloud OpenAPI](https://developer.atlassian.com/cloud/confluence/swagger.v3.json), downloaded 2026-08-25 | `1.0.0` | `70088490f4069e0a5b082411ac5296b822041b2848256444f0dfca3f3d319e73` |
| Okta | `okta/openapi.yaml` | [Okta's official Management OpenAPI](https://github.com/okta/okta-management-openapi-spec) `management-minimal.yaml`, downloaded 2026-08-25 | `2026.08.1` | `457a82fd913b3858abda99191d6b7ed6b8747447f98266d4d0dd19dcbf781a74` |

The first benchmark environment these enable is **Acme Support Desk**: Gmail,
Slack, Jira, and Confluence. Stripe and Okta are the inputs for later Commerce
and Access & Onboarding environments.

## Deliberately deferred

- **Shopify:** Shopify does not publish a maintained official OpenAPI artifact,
  and its Admin REST API is legacy in favor of GraphQL. Do not add an
  unverified community REST specification.
- **Google Admin Directory:** Google publishes an official Discovery document,
  not native OpenAPI. Convert and validate that source using the existing
  Access discovery-to-OpenAPI path before importing it.

Gmail is already a curated Fabricate resource. The Calendar, Drive, and Sheets
inputs are imported here for future Workspace and knowledge-work environments.
