# PostHog OpenAPI source

Curated Fabricate contract: `openapi.yaml`.

Official source (not vendored in git):

- URL: https://us.posthog.com/api/schema/
- SHA-256: `cae6357bc80e2b8e9afe5ccbc7505a873f7241cee812300c3958fc226e6b26d2`

The published schema is OpenAPI 3.1. Fabricate's generator currently emits
Go chi-server bindings from OpenAPI 3.0.3, so this resource keeps a curated
3.0.3 subset of feature-flag list/create/retrieve.
