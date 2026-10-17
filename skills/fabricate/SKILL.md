---
name: fabricate
description: Run Fabricate infrastructure profiles or compiled HTTP API environments, drive a managed console, and inspect redacted request logs.
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

When a managed console is running (`fab serve`, or `fab run --ui`), fetch
`GET /.well-known/fabricate` on that console. The document lists resources,
scenarios, environments, and running sandboxes, and `control` gives the
requests that start a sandbox, read it, call it, fetch its redacted request
log, and close it. `POST /api/sessions` with `{"kind":"scenario","id":"<scenario id>"}`
or `{"kind":"environment","id":"<environment name>"}` starts one. Append the
provider path to the service `url`. Without proxy mode, send
`Authorization: Bearer` and the service secret. `GET /api/sessions/<id>/logs`
is the redacted trace while the sandbox is running. `DELETE /api/sessions/<id>`
closes a sandbox this console started. Read the log before closing it.

Runnable sample apps live in `examples/`. Wrap them with
`fab-dev run <environment> -- ...` (or `fab` from a release). See
`examples/README.md`.

Do not use `fab create gmail`, `engine: httpmock`, or `MOCK_SERVICE`; those
belong to the removed implementation.
