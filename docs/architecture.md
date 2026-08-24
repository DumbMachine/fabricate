# Architecture

Fabricate has a shared CLI but two independent lifecycle models.

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
  exact host/path routing, synthetic authentication, and fail-closed behavior.
- `requestlog/` stores redacted request/response JSONL outside ephemeral state.

Gmail is the first reference resource. `gmail.acme-corp.v1` contains twelve
handcrafted messages. The environment at `environments/acme-gmail.yaml` routes
normal Gmail and Google OAuth hosts to local handlers, allowing an unmodified
official Google API client to run inside the wrapper.

```bash
fab run --environment ./environments/acme-gmail.yaml --proxy -- <command>
```

The current supervisor is foreground-only. It deletes live state on shutdown
but retains the redacted request log. Detached environments, explicit reset,
capture, and general multi-resource lifecycle commands remain future work.

## Infrastructure profiles

The established profile pipeline remains for real infrastructure:

```text
profile.yaml -> profile.Load -> engine.Engine -> target -> Instance/Creds
```

`fab create`, `fab ls`, `fab creds`, `fab wait`, and `fab destroy` operate on
that pipeline. Docker is the default target; supported engines may also use the
Kubernetes target. Profile seeds are engine-specific SQL, JavaScript,
Redis commands, Prometheus config, shell, and similar infrastructure inputs.

HTTP APIs do not pass through the profile pipeline. This prevents two resource
registries, two state formats, and provider-specific container lifecycles from
reappearing.

## Extension boundary

- New provider API: implement `httpresource.Resource` under `resources/`.
- New provider scenario: add a strict versioned document under its resource.
- New runnable composition: add an `environment` manifest.
- New infrastructure product: implement `engine.Engine` and add profiles.

The detailed HTTP/API v1 contract is
[http-api-engine-v1-plan.md](http-api-engine-v1-plan.md). The SQLite/module
decision is [ADR 0001](adr/0001-http-api-sqlite-and-module.md).
