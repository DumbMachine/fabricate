# Search marketing sandboxes

Three compiled resources use the existing Fabricate Resource, ScenarioCodec,
SQLite service, transparent proxy, synthetic authorization, request log and
registry contracts. Scenario files supply the recipe business data. No recipe,
query, title or customer-specific branch exists in the HTTP handlers.

`fitfoodie-search.yaml` selects coordinated synthetic scenarios. This is a local
workflow demonstration; it is not production data from Fit Foodie Finds.
WordPress uses the reserved `wordpress.example.test` sandbox hostname so an
editorial demo cannot accidentally publish to the real website.

## Bounded surfaces

- `search-console`: Search Analytics query for URL-prefix properties, dates,
  page/query dimensions, AND equality filters and row limits. The fixture stores
  period totals on representative dates; no daily series is implied.
- `serp`: Google organic live advanced task envelopes with known
  keyword/location/language observations, organic items and AI Overview sources.
  Unknown combinations return 422 rather than inventing a result.
- `wordpress`: list posts, read one post, update its title/content, read the
  updated version. State is isolated per sandbox run and stored in SQLite.

Unsupported dimensions, request fields, task combinations and routes fail
explicitly. These are manually implemented, limited HTTP subsets with embedded
curated OpenAPI descriptions. They do not yet have generated strict-server
bindings, complete provider schemas or official provider SDK conformance. The
SDK descriptor records that limit. Existing shipped provider behavior is reused
through the normal registry; these modules do not add another HTTP runtime.

## Upstream references

References checked 15 October 2026:

- https://developers.google.com/webmaster-tools/v1/searchanalytics/query
- https://docs.dataforseo.com/v3/serp/google/organic/live/advanced/
- https://developer.wordpress.org/rest-api/reference/posts/

Request and response shapes are curated from those API reference pages. The
synthetic scenario documents are authored examples, not upstream API captures.

## Checks

`go test ./resources/searchmarketing` covers state round trips, validation,
mutations across two post IDs, isolation across two service instances,
Search Console comparison metrics, two SERP queries and unsupported surfaces.
The Access companion scenario includes `checks/sandbox-check.py`, which runs
Search Console, SERP and mutation/read-back checks through `fab run --proxy`
using Python's HTTPS client. Full provider SDK conformance remains unverified.
Generate operation docs with `make docs-operations`.
