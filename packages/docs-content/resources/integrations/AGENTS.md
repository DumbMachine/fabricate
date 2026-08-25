# Integration documentation guide

Each integration page must let a reader try its documented flow without
checking out Fabricate or creating an environment file by hand.

## Runnable commands

- Every command block must be usable as a single paste in a POSIX shell.
- When an example needs a repository manifest, fetch its canonical raw GitHub
  file and pipe it to `fab run --environment /dev/stdin`; `fab run` accepts a
  file path, not an environment URL.
- Use `curl -fsSL` for manifest downloads and `curl -sS` for request examples
  so successful output stays readable while failures remain visible.
- Do not leave placeholder commands such as `your-app` as the only runnable
  example. Show a concrete request first, then explain what users should
  replace.
- Keep secrets in Fabricate-injected environment variables. Never put a token,
  credential, or local path that a reader cannot have in a published command.

## Expected results

- Show representative response output for a read-only request when it helps a
  reader confirm that the integration works.
- Keep output in a `json` code block, pretty-print it with two-space
  indentation, and match the scenario's actual seeded data. Do not include
  ephemeral URLs, generated paths, request-log locations, or tokens.
- Shared documentation styling gives code content a readable minimum height and
  a viewport-aware maximum height with scrolling. Do not add per-page height
  rules unless an example has an exceptional, documented need.
- Re-run a changed example against the referenced manifest and scenario before
  publishing. Check direct-URL and proxy examples independently when both are
  documented.

## Planned integrations

- A planned integration page must say that it is not runnable yet in its first
  visible content. Do not present a command, scenario, or capability as
  available before its resource and environment exist.
- For GitHub, keep the first contract to repository metadata and collaboration.
  Do not imply support for Git smart HTTP, repository mutation, Actions,
  releases, uploads, webhooks, or GraphQL.
