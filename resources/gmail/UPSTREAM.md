# Gmail contract provenance

The operation inventory was curated from
[`gmail-api-openapi-spec.yaml`](https://github.com/MarkusPfundstein/mcp-gsuite/blob/main/gmail-api-openapi-spec.yaml)
in `MarkusPfundstein/mcp-gsuite` at revision
`af509cf2d608537d69fd80981672c97a94116f05` (the `main` head resolved on
2026-08-24).

That source is Swagger 2.0 and its generated definition fields are largely
placeholder `type: post` values. Fabricate therefore does not compile it
directly. `openapi.yaml` retains only the profile, label, message, and thread
operations implemented by the Gmail resource, converts them to OpenAPI 3.0.3,
and replaces placeholder definitions with bounded schemas exercised by the
official `googleapis` client.

The upstream repository is licensed under MIT. Gmail API field semantics are
also checked against Google's public API behavior through the official client
conformance test.
