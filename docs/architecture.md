# Architecture

Fabricate runs compiled provider APIs inside disposable environments.

## Compiled HTTP/API environments

```text
scenario.Document
  -> resource ScenarioCodec
  -> engine/http baseline.db + live.db
  -> generated strict provider server
  -> request validation + synthetic authentication
  -> direct listener and/or transparent HTTPS proxy
  -> official provider client
```

The packages have narrow ownership:

- `scenario/` defines the strict immutable envelope and canonical digest.
- `httpresource/` defines the provider-independent resource contract.
- `resources/<id>/` owns one provider's OpenAPI contract, generated bindings,
  SQLite schema, scenario codec, behavior, and built-in scenarios.
- `resources/all/` is the single compile-time registry.
- `engine/http/` materializes isolated baseline and live SQLite state and hosts
  resource handlers.
- `environment/` composes named services and the proxy for one foreground run.
- `engine/http/proxy/` provides CONNECT/TLS interception, per-run CA material,
  exact host/path routing, synthetic authentication, default forwarding for
  unknown hosts, and opt-in strict rejection.
- `requestlog/` stores redacted request/response JSONL outside ephemeral state.

Gmail is the first reference resource. `gmail.acme-corp.v1` contains 28
handcrafted messages. Single-service manifests such as
`environments/acme-gmail.yaml` start one provider. Composed manifests such as
`environments/acme-support-desk.yaml` start Gmail, Intercom, Asana, and HubSpot
together with shared Acme incident data, and route each provider's normal hosts
to the local handlers.

```bash
fab run acme-support-desk --proxy -- <command>
```

The current supervisor is foreground-only. It deletes live state on shutdown
but retains the redacted request log. Detached environments, explicit reset,
capture, and general multi-resource lifecycle commands remain future work.

## Legacy boundary

The old container-profile implementation remains in the repository as inactive
legacy code. It is not registered in the CLI and must not be changed or
extended. Infrastructure workflows may return through a clean refactor in a
future version.

## Extension boundary

- New provider API: implement `httpresource.Resource` under `resources/`.
- New provider scenario: add a strict versioned document under its resource.
- New runnable composition: add an `environment` manifest.

The detailed HTTP/API v1 contract is
[http-api-engine-v1-plan.md](http-api-engine-v1-plan.md). The SQLite/module
decision is [ADR 0001](adr/0001-http-api-sqlite-and-module.md).
