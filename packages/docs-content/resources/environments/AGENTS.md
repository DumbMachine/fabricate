# Environment documentation guide

Environment pages describe a runnable composition of one or more integrations.
Treat the manifest as the source of truth for its identifier, services, and
selected scenarios.

## Naming

- Use a reader-friendly environment name for the page title and sidebar label.
  `acme-support-desk` is displayed as **Acme Support Desk**; `acme-gmail` is
  **Acme's Gmail**.
- State the machine-readable manifest identifier separately in backticks when a
  reader needs it. Do not turn display names into new manifest filenames or
  `metadata.name` values.

## Page contents

- List every included integration with the registered MDX primitive. Do not
  build a bespoke icon list on individual pages:

```mdx
<IncludedIntegrations
  services={[
    {
      resource: "gmail",
      label: "Gmail",
      href: "/resources/integrations/gmail",
      service: "support-mail",
      scenario: "gmail.acme-corp.v1",
      summary: "One-liner for the selected scenario.",
      data: "One-liner for the seeded records.",
    },
  ]}
/>
```

The component shows a row of resource icons, then a table of each service
with a scenario one-liner and a following line about the data. Keep `resource`,
`service`, and `scenario` aligned with the manifest.
- Explain the scenario's data and why it is useful for an application, agent,
  or test workflow.
- Include a one-paste startup command that reads the canonical raw manifest
  through `fab run --environment /dev/stdin --proxy`. The primary example
  should call each service on its normal production hostname, with one `curl`
  per line so the requests stay distinct. Separate curl bodies with `echo`.
  Do not include Authorization headers; the proxy supplies the local token.
  Do not wrap the curls in `printf` JSON array syntax. Readers must not need
  to clone Fabricate first.
- Render that command and its captured response with the registered MDX
  primitive. Do not hand-write JSON output on the page:

```mdx
<CommandOutput id="<id>" />
```

  Add a spec next to the Gmail example at
  `packages/docs-content/resources/_examples/<id>.json`. `catalog.json` is
  shared invalidation roots only; every `*.json` in that directory except
  `catalog.json` is a spec. Copy the Gmail spec’s fields:

  - `id`: `{manifest}-{what}` for an environment page (`acme-support-desk-inv-4812`)
    or `{resource}-{action}` for a single-service example (`gmail-list-messages`).
  - `environment` / `environmentLabel` / `proxy` / `outputPath` under
    `packages/docs-content/resources/_generated/`.
  - `publishedCommand`: the GitHub-raw one-paste readers copy.
  - `argv`: only the inner command after `fab run ... --`. For several curls
    that is `["sh", "-c", "..."]`. Capture still runs the **local**
    `environments/` file, not the GitHub URL.

  `make docs-examples` recaptures dirty specs. Pass resource names to limit
  the set (`make docs-examples gmail asana`) or
  `DOCS_EXAMPLES_FLAGS='--id <id>'` for one spec. Unknown resource names fail.
  Capture requires JSON stdout. Several curl bodies are stored as a JSON
  array in `output`; a single curl stays an object. Do not hand-edit
  `_generated/`. The docs site reads the snapshot at build time; it must not
  start environments. Force recapture with
  `make docs-examples DOCS_EXAMPLES_FLAGS=--all`. `make docs-operations` writes
  `*.operations.json` from each compiled OpenAPI contract. `make conformance`
  writes `*.compatibility.json`. Those three pipelines are separate.
  Missing or corrupt snapshots render an unavailable card instead of failing
  the docs build.
- Keep claims scoped to services actually present in the manifest. Link to the
  integration page for API coverage, response examples, and proxy details.

## Benchmark fixtures

- A benchmark environment is a coherent business world, not a list of provider
  demos. Once an environment contains multiple services, name it for that
  workflow (for example, `Acme Support Desk`) rather than for one provider.
- Seed cross-system references and realistic ambiguity: duplicates, stale
  records, incomplete prior work, approvals, and conflicting evidence. A safe
  task should require corroborating at least two services before a write.
- Keep baselines versioned and resettable. Benchmark tasks need explicit
  expected final state and negative checks for duplicate, unauthorized, or
  unrelated side effects.
