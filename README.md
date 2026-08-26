# fabricate

Fabricate runs reproducible local environments for applications and agents.
It has two deliberately separate extension paths:

- infrastructure resources use `fab create` with container profiles;
- provider HTTP APIs use compiled OpenAPI resources, versioned scenarios, and
  `fab run` environments.

Gmail is the reference HTTP resource. Its Acme Corp scenario contains twelve
handcrafted messages and works with applications that use Google's normal API
hosts and OAuth refresh flow—no client endpoint override is required.

## Gmail through the transparent proxy

Install the live-source contributor command once:

```bash
make install-dev
export PATH="$HOME/bin:$PATH"   # only if ~/bin is not already on PATH
```

Wrap the application that should see fabricated Gmail:

```bash
fab-dev run \
  --environment /Users/dumbmachine/github.com/dumbmachine/fabricate/environments/acme-gmail.yaml \
  --proxy \
  -- <your command>
```

For the Access checkout:

```bash
cd /Users/dumbmachine/github.com/dumbmachine/access-mcp
PORT=14820 fab-dev run \
  --environment /Users/dumbmachine/github.com/dumbmachine/fabricate/environments/acme-gmail.yaml \
  --proxy \
  -- make run
```

Fabricate starts isolated baseline/live SQLite databases, a strict generated
Gmail server, a CONNECT/TLS proxy with a per-run CA, and a synthetic Google
OAuth token endpoint. Unknown remote hosts fail closed unless the environment
explicitly lists them under `proxy.passthrough`.

Every run prints a durable, redacted JSONL request-log path. Find it later with:

```bash
fab-dev logs acme-gmail
fab-dev logs acme-gmail --cat
fab-dev logs acme-gmail --all
```

## Infrastructure resources

Container profiles remain available for databases, caches, hosts, Kubernetes,
Prometheus, and a Moto-backed AWS environment:

```bash
fab-dev profiles
fab-dev create postgres -p stripe-payments
fab-dev create redis -p hot-keys
fab-dev destroy --all
```

Profiles are only for infrastructure engines. Provider APIs do not use
`engine: httpmock`, `MOCK_SERVICE`, service-specific Docker images, or
`fab create`.

## Bringing back another HTTP API

Follow [Adding an HTTP API resource](docs/adding-an-http-api-resource.md). The short
version is:

1. add `resources/<id>/` with a curated OpenAPI contract and generated strict
   server bindings;
2. implement `httpresource.Resource`, its SQLite scenario codec, and behavior;
3. add strict, versioned scenarios under that resource;
4. register it once in `resources/all`;
5. add an environment manifest and direct/proxy conformance tests using the
   provider's official client.

The old `mockd`, `engine/httpmock`, and emulate-based GitHub paths have been
removed. New APIs extend the same root-module engine used by Gmail.

## Development

```bash
make check           # vet, unit tests, and formatting check
make generate        # regenerate committed OpenAPI bindings
make generate-check  # regenerate and fail if bindings differ
make e2e             # container-engine smoke tests; needs Docker
```

`fab-dev` is an absolute symlink back to the checkout. Each invocation rebuilds
current source through Go's build cache while preserving the caller's working
directory. Set `FAB_INSTALL_DIR` or `FAB_DEV_GO` to override its install path or
Go executable.

Architecture: [docs/architecture.md](docs/architecture.md). Detailed delivery
contract: [docs/http-api-engine-v1-plan.md](docs/http-api-engine-v1-plan.md).

## License

MIT
