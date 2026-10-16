# Carta contract provenance

The live Carta API at `https://api.carta.com` is approval-gated. Access to
production data requires Carta to approve the integration. This file is a
curated public surface, not a private spec and not a third-party OpenAPI dump.

`openapi.yaml` keeps the published operation ids, paths, and field names for
the issuer reads and the draft option grant write implemented here. It was
curated on 2026-10-03 from Carta's public API reference:

- https://docs.carta.com/api-platform/reference/v1alpha1issuersgetissuer
- https://docs.carta.com/api-platform/reference/v1alpha1issuersliststakeholders
- https://docs.carta.com/api-platform/reference/v1alpha1issuersgetstakeholder
- https://docs.carta.com/api-platform/reference/v1alpha1issuerslistcertificates
- https://docs.carta.com/api-platform/reference/v1alpha1issuersgetcertificate
- https://docs.carta.com/api-platform/reference/v1alpha1issuerslistoptiongrants
- https://docs.carta.com/api-platform/reference/v1alpha1issuersgetoptiongrant
- https://docs.carta.com/api-platform/reference/v1alpha1issuerslistfairmarketvalues
- https://docs.carta.com/api-platform/reference/v1alpha1issuersdraftsecuritiescreatedraftoptiongrant
- https://docs.carta.com/api-platform/reference/v1alpha1issuersdraftsecuritiesgetdraftoptiongrant

The published collection version on those pages is `v1alpha1`. Fabricate's
resource version is `v1`. Production, playground, and mock hosts are listed in
the reference; this resource serves `api.carta.com` only.

Carta's live authorization is OAuth 2.0 authorization-code. Fabricate accepts
`Authorization: Bearer` because the proxy replaces that header with the
synthetic token from secret key `token`.

The published fair market value object describes 409A-derived prices and has
no report filename. The Acme world names the valuation file `409a-2026.pdf`;
that name is not a field on the published issuer surface, so it is not stored
here. The seeded fair market value is `fmv-2026`.
