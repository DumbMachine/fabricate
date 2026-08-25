# Environment documentation guide

Environment pages describe a runnable composition of one or more integrations.
Treat the manifest as the source of truth for its identifier, services, and
selected scenarios.

## Naming

- Use a reader-friendly environment name for the page title and sidebar label.
  The current `acme-gmail` manifest is displayed as **Acme's Gmail**.
- State the machine-readable manifest identifier separately in backticks when a
  reader needs it. Do not turn display names into new manifest filenames or
  `metadata.name` values.

## Page contents

- List every included integration with its correct icon, service name, and
  scenario.
- Explain the scenario's data and why it is useful for an application, agent,
  or test workflow.
- Include a one-paste startup command that reads the canonical raw manifest
  through `fab run --environment /dev/stdin`; readers must not need to clone
  Fabricate first.
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
