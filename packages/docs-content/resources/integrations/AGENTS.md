# Integration documentation guide

Each integration page must let a reader try its documented flow without
checking out Fabricate or creating an environment file by hand.

## Runnable commands

- Every command block must be usable as a single paste in a POSIX shell.
- When an example needs a repository manifest, fetch its canonical raw GitHub
  file and pipe it to `fab run /dev/stdin`; `fab run` accepts a
  file path, not an environment URL.
- Use `curl -fsSL` for manifest downloads and `curl -sS` for request examples
  so successful output stays readable while failures remain visible.
- Do not leave placeholder commands such as `your-app` as the only runnable
  example. Show a concrete request first, then explain what users should
  replace.
- Keep secrets in Fabricate-injected environment variables. Never put a token,
  credential, or local path that a reader cannot have in a published command.

## Page structure

Implemented integration pages use this order:

1. Frontmatter `description` and the opening sentence: named entities from the
   compiled surface, so coverage is visible at a glance. Do not start with
   “A reproducible … API” or “Fabricate ships …”.
2. `## Integration`: properties (API version, auth, `ProviderHosts`, direct
   URL, transparent proxy), then start commands.
3. `## Scenarios` — `<ResourceScenarios resource="<id>" />`. Optional
   `descriptions` add qualitative copy; counts come from the generated catalog.
4. `## Compatibility verification`
5. `## Supported operations`

Planned pages (GitHub, Shopify) are not runnable resources. Do not invent an
entity list or integration table for them.

## Generated page artifacts

Command output, operation catalogs, scenario counts, and compatibility tables
are the same kind of evidence: committed JSON under
`packages/docs-content/resources/_generated/`. The docs site reads those
files at build time; it never starts environments.

```mdx
<CommandOutput id="<example-id>" showCommand={false} />
<ResourceScenarios resource="<integration>" />
<ScenarioPeek
  resource="<integration>"
  scenario="<file-without-json>"
  collection="<optional-collection>"
/>
<IntegrationCompatibility resource="<integration>" />
<SupportedOperations resource="<integration>" />
```

Do not import `_generated/*.json` from the page. The components read those
files at build time, validate the shape, and render a “snapshot unavailable”
card when a file is missing or corrupt. Do not hand-write JSON or bespoke
status UI. Do not hand-edit files in `_generated/`. After the inputs change,
recapture and commit the snapshots. CI fails if they drifted.

- Command snapshots: specs live under
  `packages/docs-content/resources/_examples/` with shared invalidation roots
  in `catalog.json`. `make docs-examples` recaptures dirty examples across the
  catalog; `make docs-examples gmail asana` limits to named resources and
  fails if none match. Use `showCommand={false}` when the page already has a
  copyable command block. Force selected examples with
  `make docs-examples DOCS_EXAMPLES_FLAGS=--all`.
- Scenario peeks: `<ScenarioPeek />` is a Blume island in `apps/docs/islands`.
  Pass `resource` plus `scenario` (filename without `.json`, with or without
  the resource prefix). It fetches that JSON in the browser, renders
  whatever collections the scenario document seeds, and caps rows (`limit`,
  default 25). Missing files, slow fetches, and invalid JSON get a skeleton,
  timeout, and retry. The inline table is a preview; Expand opens a sheet
  with sorting. Do not inline scenario documents into the page, and do not
  special-case a service in the island.
- Operations catalogs: `make docs-operations` walks the official resource
  registry and writes `<id>.operations.json` from each compiled OpenAPI
  contract, plus `<id>.scenarios.json` from catalog scenario state (top-level
  array counts, and Gmail's default-list size when spam or trash is present).
  `make docs-operations gmail asana` limits to named resources.
  Empty path stubs are omitted. Do not list operations or scenario counts by
  hand. Render `<SupportedOperations resource="<id>" />` and
  `<ResourceScenarios resource="<id>" />`.
- Compatibility reports: each resource owns `resources/<id>/conformance/` with
  `curl.sh`, `sdk.json`, or both. `make conformance` runs every client that
  exists and writes `<id>.compatibility.json`; `make conformance gmail asana`
  limits to named resources. The page snapshot prefers the SDK report when
  both clients ran. Timestamps are ignored when the payload is unchanged.
  The test must fail when a declared client prerequisite is unavailable; a
  skipped compatibility test is not a pass.

Do not include ephemeral URLs, generated paths, request-log locations, or
tokens in page examples. Shared documentation styling gives code content a
readable minimum height and a viewport-aware maximum height with scrolling.
Do not add per-page height rules unless an example has an exceptional,
documented need. Check direct-URL and proxy examples independently when both
are documented.

## Compatibility verification

Every implemented integration page must include a `## Compatibility
verification` section after Scenarios. State that Fabricate works
with any API client, then explain exactly which client the latest check used.
Do not imply that the tested SDK is the only supported way to call the API.

The report must identify the environment, test commit and time, verification
client, direct/proxy modes where applicable, and the result of each operation.
Start every integration with `curl` verification. Add official-SDK
verification only when the integration requirement specifically names an SDK.
The report’s `verification` object supports two evidence kinds:

- `curl`: direct HTTP read/write/read coverage that proves API behavior is
  stateful. Record the client as `curl@<major>` (not a patch version) so local
  and CI machines do not churn the snapshot.
- `official-sdk`: add this only when an SDK is named. It provides the same
  stateful coverage using the provider’s official SDK. When that SDK can use
  normal provider hosts, it must run in both direct base-URL and `fab run
  --proxy` modes; proxy mode proves an existing SDK integration can run without
  a provider base-URL change.

Add the resource and its shared runtime dependencies to CI path filters.

For a new integration, use this report shape as the starting point:

```json
{
  "integration": "example",
  "environment": {"label": "Example environment", "manifest": "environments/example.yaml", "messages": 0},
  "verification": {"kind": "curl", "label": "HTTP client", "client": "curl@8", "title": "HTTP API verification"},
  "operationLabels": {"read": "Read record", "write": "Create record", "persistence": "Confirm persistence"},
  "modes": {"direct": {"operations": {"read": "passed", "write": "passed", "persistence": "passed"}, "status": "passed"}},
  "testedAt": "2026-01-01T00:00:00Z",
  "testedCommit": "abcdef0"
}
```

## Supported operations

Every implemented integration page must finish with a `## Supported operations`
section after compatibility verification. Render
`<SupportedOperations resource="<id>" />`. That table is the advertised HTTP
surface, not the conformance workflow. Do not hand-write the method/path list.