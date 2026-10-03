# DocuSign contract provenance

`openapi.yaml` is a curated DocuSign eSignature REST API v2.1 surface. It is
not an imported third-party OpenAPI dump.

- Official reference: https://developers.docusign.com/docs/esign-rest-api/
- Retrieval date: 2026-10-03
- API version: `v2.1`
- Demo host: `demo.docusign.net`
- Public base path: `/restapi`

Operation ids and field names follow the published v2.1 reference for
accounts, users, envelopes, recipients, documents, and the recipient view.
Only the operations this resource implements are stored.

The live list-envelopes method requires `from_date` unless `envelope_ids` is
set. This curated surface treats `from_date` as optional and returns the
account's envelopes when it is omitted.

The live API authenticates with an OAuth bearer token. Fabricate accepts the
synthetic bearer token from secret key `token`. The proxy overwrites
`Authorization` with that token.

Account id `acct-acme` is synthetic.
