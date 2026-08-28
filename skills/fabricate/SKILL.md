---
name: fabricate
description: Run Fabricate infrastructure profiles or compiled HTTP API environments and inspect their request logs.
allowed-tools: Bash(fab:*), Bash(fab-dev:*)
---

# Fabricate

Use `fab create <engine> -p <profile>` for databases, caches, hosts,
Kubernetes, Prometheus, and AWS-console fixtures.

Use `fab run --environment <manifest> --proxy -- <command>` for provider HTTP
APIs. The wrapped command receives proxy and CA environment variables only for
its process tree. Unknown hosts are forwarded unchanged by default; set
`proxy.unknown_hosts: reject` for a fail-closed conformance test, with optional
exact passthrough hosts.

Inspect the newest redacted request trace with `fab logs <environment> --cat`.
Use `--all` to list retained runs.

Do not use `fab create gmail`, `engine: httpmock`, or `MOCK_SERVICE`; those
belong to the removed implementation.
