# HTTP/API Engine v1: clean-slate implementation plan

Status: target contract and implementation handoff

Implementation started: 2026-08-24

Current proved slice:

- strict scenario envelope parsing, canonical JSON, and SHA-256 digests;
- minimal HTTP resource interfaces and explicit registry;
- pinned `oapi-codegen` v2.8.0 and patched `kin-openapi` v0.146.0;
- pure-Go SQLite baseline/live state manager with reset and cleanup tests;
- curated Gmail OpenAPI 3.0.3 subset with 10 implemented operations;
- generated strict stdlib server, explicit bearer authentication, request
  validation, scenario codec, and read/write/read behavior tests;
- `gmail.minimal.v1` and a handcrafted 12-message
  `gmail.acme-corp.v1` baseline.
- foreground `fab run` for compiled Gmail environments, including loopback
  serving, process-scoped CONNECT/TLS interception, an ephemeral CA, synthetic
  OAuth refresh responses, and fail-closed provider-host routing.
- durable per-run JSONL request traces with request/response payloads, timing,
  pre-write credential redaction, and `fab logs <environment>` discovery.

Still pending before Phase 1 exit: the official `googleapis` conformance runner,
generic capture/diff APIs, and complete reset/server reconstruction integration.
The foreground proxy slice must still pass that official-client conformance
suite before broad proxy support is advertised.

Legacy cleanup completed on 2026-08-24: the separate `mockd` module,
`engine/httpmock`, the Emulate-based GitHub engine, provider HTTP profiles,
provider image/release plumbing, and the old Node conformance harness were
removed. Non-Gmail APIs are intentionally absent until they return as compiled
resources under this contract.

This plan replaces Fabricate's current HTTP mock architecture. It is written
for an implementation agent and is intentionally prescriptive about package
boundaries, interfaces, generated code, lifecycle, tests, and deletion.

The existing HTTP code is evidence, not a compatibility contract. Preserve
useful provider behavior and test assertions where they remain correct, but do
not preserve old abstractions merely because they exist. In particular, do not
build adapters around `mock.Service`, `engine/httpmock`, `MOCK_SERVICE`, HTTP
profiles, the Emulate container, or the duplicated service registries.

Real infrastructure engines such as Postgres, MySQL, Redis, Kubernetes, and
SSH are outside this refactor. They may keep their specialized lifecycle and
native interaction models. This plan standardizes only provider-shaped HTTP
and API services.

---

## 1. Outcome

Fabricate must support disposable environments composed from named services:

```text
Environment = fn(services)
Service     = fn(resource, scenario)
Resource    = compiled HTTP contract + Go behavior + scenario codec
Scenario    = immutable, versioned initial state
Live state  = mutable state produced after the service starts
Engine      = shared HTTP lifecycle, storage, serving, and connection surface
```

Example:

```yaml
apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata:
  name: duolingo-development

services:
  billing:
    resource: stripe
    scenario: stripe.duolingo.v1

  support-mail:
    resource: gmail
    scenario: gmail.acme-corp.v1

  source:
    resource: github
    scenario: github.duolingo-app.v1
```

After startup, an application must be able to use each service in either of
two ways:

1. Direct endpoint mode: configure the provider SDK with the Fabricate URL and
   synthetic credentials.
2. Transparent proxy mode: leave the provider SDK on its normal vendor host
   and run it through an environment-scoped HTTPS proxy.

The direct endpoint is the default. The proxy is an opt-in compatibility
surface and a conformance test.

The product must not run a second implementation in Docker. Local, detached,
CI, and optional Docker isolation modes must execute the same generated Go
server bindings and the same resource behavior.

---

## 2. Non-negotiable rules

1. **OpenAPI is the HTTP source of truth.** Paths, methods, operation IDs,
   parameters, request bodies, responses, media types, and security schemes
   originate in a curated OpenAPI document.
2. **OpenAPI is compiled.** Generate typed Go models, a strict Go server
   interface, routing glue, and an embedded normalized specification during
   development. Do not build an ad hoc route interpreter.
3. **Generated Go artifacts are committed.** CI regenerates and fails if the
   worktree changes.
4. **Do not generate a JavaScript client.** Provider compatibility is tested
   through the provider's official Node SDK when one exists. Use a small
   handwritten JavaScript HTTP client only when no suitable official SDK
   exists.
5. **A declared operation must work.** The curated spec contains only the
   surface Fabricate intentionally supports. Never publish a huge vendor spec
   while returning generic placeholders for most operations.
6. **Custom behavior is ordinary Go.** Transactions, webhooks, OAuth, state
   transitions, provider-specific errors, and other complex behavior implement
   the generated server interface. Custom code does not register undeclared
   paths.
7. **Scenarios are explicit data.** A scenario is immutable initial state. It
   never changes because a live service was used.
8. **Reset restores the scenario baseline.** Capture creates a new scenario;
   it never overwrites the source scenario.
9. **One HTTP engine, isolated service instances.** Multiple HTTP resources use
   the same engine implementation, middleware, lifecycle, storage manager, and
   supervisor. Each service instance receives isolated state and credentials.
10. **Direct access works without a proxy.** Proxying is not required for the
    basic development loop.
11. **No production egress by default.** Unknown proxy hosts and external
    webhook destinations are rejected unless explicitly allowed.
12. **No compatibility scaffolding for obsolete HTTP paths.** Delete rather
    than wrap old HTTP abstractions once the corresponding new vertical slice
    is proven.

---

## 3. Scope

### In scope

- Environment manifests containing named HTTP service instances.
- One shared HTTP engine.
- Curated OpenAPI specifications.
- Reproducible Go code generation.
- Resource-specific Go behavior.
- Versioned JSON scenarios.
- Scenario validation, load, dump, diff, reset, and capture.
- Per-service SQLite state.
- Local foreground and detached supervisors.
- Direct endpoint connection manifests.
- Environment-scoped transparent HTTPS proxying.
- `fab run` for development and CI.
- Mandatory Go fixture and state tests.
- Official Node SDK conformance tests where an SDK exists.
- Handwritten JavaScript compatibility tests where no official SDK exists.
- Gmail as the first reference resource.
- GitHub as the migration that removes Emulate.
- Stripe as the first clean-slate new resource.

### Explicitly out of scope for v1

- Reworking Postgres, MySQL, Redis, Kubernetes, SSH, or other real engines.
- A universal engine execution interface for SQL, CLI, and HTTP resources.
- Drive filesystem emulation.
- Google Sheets grid/formula modeling.
- Airtable dynamic bases and formulas.
- General binary asset packages.
- Scenario inheritance or runtime merge chains.
- Arbitrary scenario scripting.
- LLM-generated hidden seed data.
- A hosted control plane.
- Untrusted in-process third-party Go plugins.
- Go's dynamic `plugin` package.
- Multi-user security boundaries inside one local process.

---

## 4. Vocabulary and invariants

### Environment

An environment is a named lifecycle and isolation boundary containing one or
more named service instances. It owns aggregate startup, readiness, rollback,
reset, connection output, logs, and destruction.

An environment is ready only when all requested services are ready. If one
service fails to start, the environment supervisor cleans up every service it
already started and returns one error identifying the failed service.

### Service

A service is one named runtime instance:

```text
service = resource + scenario + instance configuration
```

The name is local to an environment and stable for its lifetime. Examples are
`billing`, `support-mail`, and `source`. Multiple instances of the same
resource are allowed only when they have distinct names.

### Resource

A resource is reusable compiled code for one provider API, such as `gmail`,
`github`, or `stripe`. It owns:

- the curated OpenAPI source;
- generated Go server bindings and models;
- provider behavior;
- provider-shaped errors;
- scenario schema and semantic validation;
- scenario-to-SQLite loading;
- SQLite-to-scenario dumping;
- declared provider hosts;
- official SDK construction guidance and tests.

A resource is not a service instance and must not contain package-global live
state.

### Scenario

A scenario is an immutable, versioned document describing initial resource
state. It is resource-specific. `stripe.duolingo.v1` and
`stripe.netflix.v1` share a resource contract but contain different data.

Scenario loading uses replacement semantics. Omitted resource collections are
empty unless the scenario schema explicitly defines a default. Loading does
not silently merge with built-in seed data.

### Live state

Live state begins as an exact materialization of the scenario and then mutates
through provider API calls. It is stored separately from the immutable
baseline.

### HTTP engine

The HTTP engine is how the CLI and agents manage HTTP service lifecycle. It
creates isolated state, constructs resource handlers, retains listeners,
returns endpoints and synthetic credentials, supports reset/capture, and
shuts services down.

It does not know Stripe or Gmail operation semantics.

---

## 5. Target repository structure

Use one Go module for the CLI, supervisor, HTTP engine, generated resource
servers, and resource implementations. A separate module for `mockd` is not a
target architecture.

```text
cmd/
  fab/
    main.go

environment/
  spec.go
  validate.go
  orchestrator.go
  connections.go

scenario/
  document.go
  ref.go
  catalog.go
  canonical.go
  digest.go
  diff.go

engine/
  http/
    engine.go
    service.go
    state_manager.go
    listener.go
    middleware.go
    request_context.go
    supervisor.go
    control_api.go
    connections.go
    proxy/
      proxy.go
      routes.go
      ca.go
      transport.go

httpresource/
  resource.go
  descriptor.go
  scenario_codec.go
  registry.go
  runtime.go
  conformance.go

resources/
  all/
    all.go
  gmail/
    openapi.yaml
    oapi-codegen.yaml
    generate.go
    generated/
      openapi.gen.json
      models.gen.go
      server.gen.go
      spec.gen.go
    resource.go
    server.go
    scenario.go
    schema.sql
    scenario.schema.json
    scenarios/
      minimal.v1.json
      acme-corp.v1.json
    resource_test.go
    scenario_test.go
  github/
  stripe/

cli/
  environment.go
  run.go
  services.go
  connections.go
  scenario.go

e2e/
  environment_test.go
  proxy_test.go
  sdk/
    package.json
    lib/
      environment.mjs
      assertions.mjs
    gmail.direct.test.mjs
    gmail.proxy.test.mjs
    github.direct.test.mjs
    github.proxy.test.mjs
    stripe.direct.test.mjs
    stripe.proxy.test.mjs

internal/
  generatedcheck/
  testenv/
```

Names may change to match Go conventions, but the dependency direction must
remain:

```text
CLI -> environment orchestrator -> HTTP engine -> HTTP resource interface
                                          ^               |
                                          |               v
                                      supervisor      resource packages
```

Resource packages must not import the CLI, environment orchestrator, or
supervisor.

---

## 6. Core data model

These signatures are the target shape. Small changes are acceptable when Go
requires them, but do not collapse these concepts back into the old profile or
engine structures.

### Environment specification

```go
package environment

type Spec struct {
    APIVersion string                 `yaml:"apiVersion" json:"apiVersion"`
    Kind       string                 `yaml:"kind" json:"kind"`
    Metadata   Metadata               `yaml:"metadata" json:"metadata"`
    Services   map[string]ServiceSpec `yaml:"services" json:"services"`
    Proxy      *ProxySpec             `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

type Metadata struct {
    Name string `yaml:"name" json:"name"`
}

type ServiceSpec struct {
    Resource string            `yaml:"resource" json:"resource"`
    Scenario scenario.Ref      `yaml:"scenario" json:"scenario"`
    Expose   map[string]string `yaml:"expose,omitempty" json:"expose,omitempty"`
}

type ProxySpec struct {
    Enabled bool              `yaml:"enabled,omitempty" json:"enabled,omitempty"`
    Hosts   map[string]string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
}
```

Validation rules:

- environment name is required and filesystem-safe;
- at least one service is required;
- service names are unique, stable, and filesystem-safe;
- every resource exists in the resource registry;
- every scenario resolves and matches its resource;
- scenario digests match when pinned;
- explicit proxy host mappings name existing services;
- a vendor host cannot resolve to two service instances;
- unknown fields fail parsing;
- all validation completes before any listener or database is created.

### Scenario reference

Support catalog IDs and project-local paths without overloading one string
internally:

```go
package scenario

type Ref struct {
    ID     string `yaml:"id,omitempty" json:"id,omitempty"`
    Path   string `yaml:"path,omitempty" json:"path,omitempty"`
    Digest string `yaml:"digest,omitempty" json:"digest,omitempty"`
}

func (r Ref) Validate() error
```

The YAML decoder may accept the shorthand:

```yaml
scenario: stripe.duolingo.v1
```

but it must normalize immediately into a `scenario.Ref`.

### Scenario document

```go
package scenario

type Document struct {
    Contract        string          `json:"$contract"`
    ContractVersion int             `json:"$contractVersion"`
    ID              string          `json:"$id"`
    Resource        string          `json:"$resource"`
    ResourceVersion string          `json:"$resourceVersion"`
    State           json.RawMessage `json:"state"`
}

type Metadata struct {
    ID              string
    Resource        string
    ResourceVersion string
}

type Resolved struct {
    Ref           Ref
    Document      Document
    CanonicalJSON []byte
    Digest        string
    Source        string // catalog ID or absolute path, for diagnostics only
}
```

Example:

```json
{
  "$contract": "fabricate.scenario",
  "$contractVersion": 1,
  "$id": "stripe.duolingo.v1",
  "$resource": "stripe",
  "$resourceVersion": "2026-08-01",
  "state": {
    "customers": [],
    "products": [],
    "prices": [],
    "subscriptions": []
  }
}
```

V1 rules:

- JSON is the canonical stored representation;
- unknown envelope fields fail validation;
- no inheritance is resolved at startup;
- no arbitrary variable substitution;
- no setup scripts or raw HTTP post-seed calls;
- IDs needed by tests are explicit and stable;
- runtime ports, URLs, environment IDs, logs, and credentials are forbidden;
- canonical JSON and its SHA-256 digest are deterministic;
- secret scanning runs before load and before capture writes a file.

`fab scenario new --from` copies and materializes the complete source
scenario. Provenance may be recorded outside the canonical state, but the new
scenario must load independently.

---

## 7. HTTP resource interfaces

### Descriptor

```go
package httpresource

type Descriptor struct {
    ID              string
    DisplayName     string
    Version         string
    OpenAPIVersion  string
    OpenAPIDigest   string
    ScenarioVersion int
    ProviderHosts   []string
    SDK             SDKDescriptor
}

type SDKDescriptor struct {
    Package     string // e.g. "googleapis", "@octokit/rest", "stripe"
    Language    string // "javascript"
    DirectTest  bool
    ProxyTest   bool
    Exception   string // required when no official SDK test exists
}
```

Descriptors are returned by code and cataloged once. Do not maintain a second
CLI-only list like `KnownServices`.

### Resource

```go
package httpresource

type Resource interface {
    Descriptor() Descriptor
    Contract() Contract
    Scenarios() ScenarioCodec
    NewServer(context.Context, ServerDependencies) (Server, error)
}

type Contract struct {
    OpenAPIJSON  []byte
    ScenarioJSON []byte
}

type ServerDependencies struct {
    DB      *sql.DB
    Clock   Clock
    IDs     IDGenerator
    Secrets SecretSource
    Events  EventSink
}

type Server interface {
    Handler() http.Handler
    Close(context.Context) error
}

type Clock interface {
    Now() time.Time
}

type IDGenerator interface {
    Next(context.Context, string) (string, error)
}

type SecretSource interface {
    Get(context.Context, string) (string, error)
}

type Event struct {
    Type    string
    Subject string
    Data    json.RawMessage
}

type EventSink interface {
    Emit(context.Context, Event) error
}
```

`NewServer` constructs the generated strict server implementation and returns
the generated `net/http` handler wrapped only in resource-local middleware
that cannot be shared.

Rules:

- a new server object is created for every service instance;
- no package-global DBs, auth maps, queues, clocks, or mutable caches;
- `Close` drains resource-owned workers and releases them;
- live durable state belongs in the supplied DB;
- reset reconstructs the server, so hidden mutable state is not retained;
- custom code must implement an operation generated from OpenAPI;
- resource code cannot add a new path directly to a router.

### Scenario codec

```go
package httpresource

type ScenarioCodec interface {
    Validate(context.Context, scenario.Document) error
    Initialize(context.Context, *sql.DB) error
    Load(context.Context, *sql.DB, scenario.Document) error
    Dump(context.Context, *sql.DB, scenario.Metadata) (scenario.Document, error)
}
```

Responsibilities:

Common scenario package:

- envelope validation;
- catalog/path resolution;
- version and digest validation;
- canonicalization;
- secret scanning;
- diffing;
- atomic writes.

Resource scenario codec:

- resource-specific JSON Schema validation;
- semantic references and invariants;
- SQLite schema initialization;
- replacement loading;
- provider ID and default rules;
- deterministic dumping;
- migration errors when a resource version is unsupported.

Do not infer SQLite DDL or behavior from OpenAPI. OpenAPI describes the wire
contract, not domain relationships or state transitions.

### Runtime request context

Provider responses sometimes contain absolute URLs. They must be correct in
both direct and proxy modes.

```go
package httpresource

type RequestInfo struct {
    EnvironmentID string
    ServiceName   string
    ResourceID    string
    OperationID   string
    ExternalURL   *url.URL
}

func RequestInfoFromContext(context.Context) (RequestInfo, bool)
```

The HTTP engine derives `ExternalURL` from the trusted inbound listener and
proxy metadata. Resource code uses it when constructing absolute URLs. It must
not hard-code `localhost`, container ports, or vendor hosts.

The engine strips any user-supplied internal forwarding headers before adding
trusted request context.

---

## 8. OpenAPI build contract

### Standard

Use curated OpenAPI 3.0.3 documents for v1 unless a resource has a proven need
for a later feature. Consistency is more valuable than accepting every vendor
spec dialect initially.

Every operation must have:

- a unique, stable `operationId`;
- explicit request parameters and body media types;
- explicit success responses;
- provider-shaped error responses for supported failures;
- security declarations;
- bounded schemas that generated Go code can represent;
- examples for at least the primary success request and response.

### Generator

Use a pinned `oapi-codegen` v2 toolchain with strict server generation,
stdlib `net/http` server generation, models, and embedded specification. Do not
enable client generation.

Illustrative configuration:

```yaml
package: generated
generate:
  models: true
  std-http-server: true
  strict-server: true
  embedded-spec: true
output: generated/server.gen.go
```

If current generator behavior requires models and server output to be split,
use multiple pinned configs, but retain one source `openapi.yaml`.

The strict server generator handles typed request/response envelopes but does
not replace request schema validation. Add pinned OpenAPI request validation
middleware around generated routes. Authentication must be implemented
explicitly; never rely on a validation library's no-op authentication default.

### Generation entrypoint

Each resource owns a `generate.go` file:

```go
package gmail

//go:generate go tool oapi-codegen -config oapi-codegen.yaml openapi.yaml
//go:generate go run ../../internal/cmd/compile-openapi -in openapi.yaml -out generated/openapi.gen.json
```

Prefer pinned Go tool dependencies over `@latest`. Exact commands may change
after the first spike, but `go generate ./resources/...` must be deterministic.

Provide repository commands:

```text
make generate
make generate-check
```

`generate-check` runs generation and fails if `git diff --exit-code` is not
clean. It is required in CI.

### Generated strict server example

Expected generated shape:

```go
type StrictServerInterface interface {
    ListMessages(
        context.Context,
        ListMessagesRequestObject,
    ) (ListMessagesResponseObject, error)

    SendMessage(
        context.Context,
        SendMessageRequestObject,
    ) (SendMessageResponseObject, error)
}
```

Resource behavior implements the interface directly:

```go
type Server struct {
    db    *sql.DB
    clock httpresource.Clock
    ids   httpresource.IDGenerator
}

var _ generated.StrictServerInterface = (*Server)(nil)

func (s *Server) SendMessage(
    ctx context.Context,
    req generated.SendMessageRequestObject,
) (generated.SendMessageResponseObject, error) {
    // Provider behavior and state transition live here.
}
```

Do not introduce `GET(path, handler)` or `Handle(operationID, handler)` as a
second authoring API when the generated strict interface can express the
operation. If the generator cannot express a legitimate protocol feature,
document the exact limitation before adding a narrowly scoped escape hatch.

### Curating an upstream specification

For a vendor with a public spec:

1. Record the upstream repository, file, revision, and license.
2. Extract only operations Fabricate will support.
3. Preserve provider operation names when stable; otherwise assign stable
   Fabricate operation IDs once and do not churn them.
4. Remove unreachable schema components.
5. Patch schemas only when required for observed provider or SDK behavior.
6. Document patches next to the resource.
7. Compile and run all resource tests.

The generated Go code and official SDK tests are independent checks. A server
and its generated models can agree with a wrong spec; the official SDK catches
that self-consistent lie.

---

## 9. Registry and construction

Use explicit compile-time registration. Do not use `init`, reflection,
filesystem scanning, or duplicated metadata lists.

```go
package httpresource

type Registry struct {
    byID map[string]Resource
}

func NewRegistry(resources ...Resource) (*Registry, error)
func (r *Registry) Get(id string) (Resource, bool)
func (r *Registry) Descriptors() []Descriptor
```

One package provides the official set:

```go
package all

func Registry() *httpresource.Registry {
    return httpresource.MustRegistry(
        gmail.NewResource(),
        github.NewResource(),
        stripe.NewResource(),
    )
}
```

The CLI, environment validator, supervisor, direct runtime, and optional
Docker runtime receive this same registry object. Catalog output is derived
from resource descriptors and scenario catalogs.

---

## 10. State manager

Each HTTP service instance gets its own directory and SQLite files:

```text
<data-root>/environments/<environment-id>/services/<service-name>/
  service.json
  baseline.db
  live.db
  requests.ndjson
```

Do not put every service into one giant SQLite database. Separate files avoid
table collisions, migration coupling, lock contention, and accidental
cross-resource reads.

### Creation

```text
resolve scenario
  -> validate common envelope
  -> validate resource schema and semantics
  -> create temporary SQLite DB
  -> resource.Initialize
  -> resource.Load using replacement semantics
  -> close DB
  -> fsync and atomically rename to baseline.db
  -> copy baseline.db to live.db
  -> open live.db
  -> construct resource server
  -> start listener
```

If any step fails, delete the temporary service directory and do not publish
connections.

### Reset

```text
mark service resetting
  -> stop accepting new requests
  -> drain in-flight requests with timeout
  -> close resource server and DB
  -> copy baseline.db to temporary live DB
  -> atomically replace live.db
  -> reopen DB
  -> reconstruct resource server
  -> reopen listener dispatch
  -> increment service revision
```

Environment reset quiesces all requested services before replacing any state.
If one reset fails, report the environment as degraded and retain enough
metadata to retry or destroy it. Do not claim cross-file transactional
atomicity that SQLite cannot provide.

### Load

Loading a scenario into an existing service validates and materializes a new
temporary baseline first. Only after success does the engine quiesce the
service, replace baseline and live state, reconstruct the server, and resume.

### Dump and capture

`Dump` reads live state through the resource codec and returns canonical JSON.
It excludes:

- credentials;
- OAuth codes and short-lived sessions unless they are intentional domain
  state;
- request logs;
- ports and environment IDs;
- runtime-generated local URLs;
- background queue bookkeeping.

`Capture` validates and secret-scans the dump, computes its digest, and writes
a new scenario atomically. It refuses to overwrite an existing scenario ID.

### Diff

Diff canonical scenario state using JSON Pointer paths. The initial v1 output
may be a deterministic structural JSON diff; provider-aware summaries can be
added later.

### SQLite requirements

- one connection configuration is owned by the state manager;
- enable foreign keys;
- configure busy timeout deliberately;
- use WAL only if copy/reset semantics are proven and checkpointed correctly;
- close and checkpoint before copying a baseline;
- use a pure-Go SQLite driver if it removes the separate module and CGO build
  without sacrificing correctness; record the driver decision in an ADR;
- never expose `*sql.DB` outside the resource instance and scenario codec.

---

## 11. HTTP engine

### Interface

```go
package httpengine

type Engine struct {
    registry *httpresource.Registry
    dataRoot string
    clock    httpresource.Clock
}

type StartRequest struct {
    EnvironmentID string
    ServiceName   string
    ResourceID    string
    Scenario      scenario.Resolved
}

type RunningService interface {
    Connection() Connection
    Status(context.Context) Status
    Reset(context.Context) error
    Load(context.Context, scenario.Resolved) error
    Dump(context.Context) (scenario.Document, error)
    Close(context.Context) error
}

type Status struct {
    State     string // starting, ready, resetting, degraded, stopped
    Revision  uint64
    LastError string
}

func (e *Engine) Start(context.Context, StartRequest) (RunningService, error)
```

Do not force non-HTTP engines to implement this exact interface in this
refactor. The environment orchestrator may use an internal adapter when it
later composes real engines with HTTP services.

### Listener topology

Use one Go supervisor process and one retained listener per service instance by
default:

```text
127.0.0.1:43121 -> billing/stripe
127.0.0.1:43122 -> support-mail/gmail
127.0.0.1:43123 -> source/github
```

Bind to `127.0.0.1:0` and retain the returned listener. Never probe a free port
and then bind it later.

Separate listeners preserve provider root paths and avoid prefix rewriting for
OAuth redirects, `Location` headers, signed URLs, and official SDK assumptions.
This is still one HTTP engine and one process, not one process per service.

### Shared middleware order

The engine owns a stable middleware chain:

```text
panic recovery
  -> request ID
  -> environment/service context
  -> body size limit
  -> request logging/redaction
  -> OpenAPI request validation
  -> synthetic authentication dispatch
  -> resource generated handler
  -> response metrics/logging
```

Provider-specific error bodies and authorization semantics remain resource
behavior. Shared middleware may reject structurally invalid or unsafe traffic,
but it must not flatten all providers into one fake error format.

Response validation runs in tests and optionally in a development debug mode.
Do not impose full schema validation on every normal request until its cost and
provider edge cases are understood.

### Credentials

Generate environment- and service-scoped synthetic credentials. Never reuse a
global `fab-mock-token`.

```go
type Connection struct {
    ServiceName string            `json:"service"`
    ResourceID  string            `json:"resource"`
    URL         string            `json:"url"`
    Hosts       []string          `json:"hosts"`
    Credentials map[string]string `json:"credentials"`
    Revision    uint64            `json:"revision"`
}
```

Credentials are returned only after the whole environment becomes ready. They
are written with restrictive filesystem permissions and never logged.

### Determinism

Every service gets an environment-scoped clock and ID generator. Catalog
scenarios must produce deterministic IDs and timestamps. Mutations may advance
IDs deterministically from the loaded baseline.

Do not call `time.Now` or global randomness directly in resource behavior.

---

## 12. Environment supervisor

Detached development environments require a process that outlives the initial
CLI call. `fab run` uses the same supervisor but owns it for the duration of a
child command.

### Responsibilities

- validate environment and every scenario before mutation;
- allocate an environment ID and state directory;
- start independent services with bounded concurrency;
- retain service listeners;
- expose an internal control API over a Unix socket;
- publish connections only after aggregate readiness;
- aggregate logs and request traces;
- coordinate reset, load, dump, and capture;
- start and stop the optional proxy;
- clean up partial startup;
- destroy the environment and revoke its credentials.

### Control API

Use HTTP+JSON over a Unix domain socket locally. Keep it private and
versioned. Illustrative endpoints:

```text
GET    /v1/status
GET    /v1/services
GET    /v1/connections
POST   /v1/services/{service}/reset
GET    /v1/services/{service}/scenario
PUT    /v1/services/{service}/scenario
POST   /v1/services/{service}/capture
POST   /v1/environment/reset
POST   /v1/shutdown
```

Provider API traffic never goes through this control API.

### Startup lifecycle

```text
parse
  -> resolve resources and scenarios
  -> validate everything
  -> plan paths, listeners, credentials, and proxy routes
  -> start services with bounded concurrency
  -> run per-service health checks
  -> start optional proxy
  -> atomically publish connection manifest
  -> ready
```

Do not publish partial environment variables or connections while startup is
still in progress.

### Environment identity

Use an opaque environment instance ID distinct from the human manifest name.
Parallel CI jobs may start the same named manifest without sharing state,
ports, credentials, or CA material.

---

## 13. Application access

### Connection manifest

The supervisor emits a stable machine-readable manifest:

```json
{
  "environment": "env_01J...",
  "name": "duolingo-development",
  "services": {
    "billing": {
      "resource": "stripe",
      "url": "http://127.0.0.1:43121",
      "hosts": ["api.stripe.com"],
      "credentials": {
        "token": "fab_stripe_..."
      },
      "revision": 1
    }
  }
}
```

CLI renderers:

```text
fab environment connections <id> --format json
fab environment connections <id> --format dotenv
fab environment connections <id> --format shell
```

Informational text goes to stderr; stdout contains only the selected payload.

### Direct endpoint mode

Direct access is the default and must work in every environment:

```bash
fab environment up ./fabricate/dev.yaml --detach
fab environment connections dev --format dotenv > .env.fabricate
npm test
fab environment down dev
```

An environment may map connection properties to application variables:

```yaml
services:
  billing:
    resource: stripe
    scenario: stripe.duolingo.v1
    expose:
      url: STRIPE_API_URL
      token: STRIPE_API_KEY
```

Resource SDK documentation and tests contain the exact official SDK
construction:

```js
const stripe = new Stripe(process.env.STRIPE_API_KEY, {
  host: process.env.STRIPE_API_HOST,
  port: Number(process.env.STRIPE_API_PORT),
  protocol: "http",
})
```

Do not add a Fabricate language SDK solely to wrap provider SDK construction.

### `fab run`

Preferred development and CI command:

```bash
fab run --environment ./fabricate/ci.yaml -- npm test
```

Lifecycle:

1. start a private supervisor;
2. wait for aggregate readiness;
3. inject connection variables into the child process;
4. run the command;
5. capture configured logs and diffs;
6. stop proxy and services;
7. return the child's exit code unless environment teardown fails more
   severely.

Flags:

```text
--proxy                  enable transparent proxying
--artifacts <dir>        write logs, request trace, and final state diffs
--keep                   keep environment after the child exits, for debugging
--name <name>            human label only; runtime ID remains unique
```

Parallel invocations are isolated.

---

## 14. Transparent HTTPS proxy

### Purpose

Proxy mode lets an application continue requesting normal provider hosts:

```text
official SDK -> https://api.stripe.com -> Fabricate proxy -> billing service
```

It is useful when constructor-level endpoint replacement is unavailable or
when testing unchanged production client configuration.

### Routing

Every resource descriptor declares its provider hosts. The supervisor builds a
host-to-service table:

```text
api.stripe.com          -> billing
api.github.com          -> source
uploads.github.com      -> source
gmail.googleapis.com    -> support-mail
```

Unknown hosts are rejected. The proxy must not fall back to the public
internet.

If two service instances claim the same provider host, startup fails unless
the environment explicitly selects the proxy target:

```yaml
proxy:
  enabled: true
  hosts:
    api.stripe.com: primary-billing
```

The other instance remains accessible through its direct endpoint.

### TLS

Generate one ephemeral CA per environment instance. Store its private key with
restrictive permissions under the environment state directory. Never install
it into the machine's global trust store.

For `fab run`, inject process-scoped trust variables appropriate to the child:

```text
HTTPS_PROXY=http://127.0.0.1:<proxy-port>
HTTP_PROXY=http://127.0.0.1:<proxy-port>
NO_PROXY=localhost,127.0.0.1
NODE_EXTRA_CA_CERTS=<environment-ca.pem>
SSL_CERT_FILE=<environment-ca.pem>
```

Document language-specific trust variables only when tested.

### Authentication safety

- provider requests never leave the machine through the proxy;
- the proxy rejects unknown destinations;
- exact non-provider hosts may be explicitly declared as passthrough when the
  wrapped application needs a live dependency such as its model provider;
- the proxy strips untrusted internal forwarding headers;
- it accepts only the environment's synthetic credentials or an explicitly
  documented placeholder;
- it never logs authorization headers or cookies;
- it must not silently accept and forward a real production credential;
- webhooks default to a recording sink or explicit local target.

### Request rewriting

Preserve method, path, query, content type, body, and provider host. Forward to
the selected direct service listener with trusted metadata describing the
external scheme and host. The resource constructs absolute response URLs from
that request context.

Avoid general response-body string replacement. Absolute provider URLs should
be created correctly by resource behavior rather than patched after the fact.

---

## 15. CLI contract

Target commands:

```text
fab environment validate <manifest>
fab environment up <manifest> [--detach] [--proxy]
fab environment status <environment>
fab environment services <environment>
fab environment connections <environment> --format json|dotenv|shell
fab environment reset <environment> [service...]
fab environment logs <environment> [--service name]
fab environment down <environment>

fab scenario list [resource]
fab scenario show <scenario>
fab scenario new <resource> <name> --from <scenario>
fab scenario validate <scenario-or-path>
fab scenario diff <environment> <service>
fab scenario load <environment> <service> <scenario-or-path>
fab scenario capture <environment> <service> --as <new-id> [--output path]

fab run --environment <manifest> [--proxy] [--artifacts dir] -- <command...>
```

HTTP resources must not require users to type:

- `httpmock`;
- `MOCK_SERVICE`;
- `engine: httpmock`;
- `mock-fixture`;
- `emulate-config`;
- `emulate-postseed`.

Existing HTTP profile commands may be removed rather than aliased. Infra
profiles remain outside this scope.

---

## 16. Testing contract

Every official HTTP resource must pass the following levels. A resource is not
accepted into the registry merely because handlers compile.

### A. Generation and contract tests: mandatory

- OpenAPI validates.
- Every operation has a unique stable `operationId`.
- Strict server code generates and compiles.
- Generated output is clean under `make generate-check`.
- The resource implementation satisfies the generated strict interface.
- Request validation rejects structurally invalid requests.
- Unknown routes fail loudly with provider-appropriate behavior.
- The embedded compiled spec digest matches the descriptor.

### B. Scenario tests: mandatory for every catalog scenario

- common envelope validation succeeds;
- resource JSON Schema validation succeeds;
- semantic references and invariants succeed;
- invalid state is rejected before a listener starts;
- load into an empty DB succeeds;
- dump and reload preserve canonical state;
- reset restores the exact baseline after mutations;
- capture produces a standalone, reloadable scenario;
- canonical digest is deterministic;
- secrets and runtime-only data are rejected or excluded.

### C. Provider behavior fixtures: mandatory

For the resource's supported operations, tests must cover:

- reading seeded scenario state;
- at least one write;
- a later read proving the write persisted;
- provider-shaped not-found and validation errors;
- pagination and filtering where declared;
- one meaningful non-happy-path transition;
- deterministic IDs and time;
- no state leakage between two service instances;
- reset after in-flight behavior completes.

Use the shared generated handler and middleware through `httptest`; do not
invoke handler functions directly as the only test.

### D. HTTP engine conformance: mandatory

- starts two different resources in one process;
- starts two instances of the same resource with isolated state;
- retains independently allocated listeners;
- returns service-scoped credentials;
- aggregate startup rolls back on failure;
- direct connection manifests are machine-readable;
- reset reconstructs the server and clears hidden ephemeral state;
- graceful shutdown drains requests and closes all DB handles and workers;
- race-enabled tests cover create/request/reset/destroy interactions.

### E. Official SDK direct conformance: mandatory when an SDK exists

Use the provider's official Node SDK against the direct Fabricate endpoint.
The minimum test is:

```text
construct official SDK with Fabricate URL and synthetic credential
  -> read seeded scenario state
  -> perform a real SDK write
  -> read again and prove persistence
  -> exercise one provider-shaped error
```

Examples:

- Gmail: `googleapis`;
- GitHub: `@octokit/rest`;
- Stripe: `stripe`.

Pin SDK major versions. An SDK major bump is a compatibility change and must
run the full resource suite.

When no suitable official SDK exists, add a small handwritten JavaScript HTTP
test client and record the exception in `SDKDescriptor.Exception`. Do not
generate and publish a Fabricate JavaScript SDK as a substitute.

SDK tests must fail if prerequisites are missing in CI. A skip is not a pass.

### F. Official SDK proxy conformance: required before claiming proxy support

Run the official SDK with its normal vendor host through `fab run --proxy`.
Do not set a resource base URL in this test. Prove:

- vendor host routing reaches the correct service;
- TLS trust is process-scoped;
- synthetic authentication works;
- no request reaches the public internet;
- absolute URLs and redirects remain usable;
- read/write/read behavior matches direct mode.

### Maturity reported by the catalog

```text
contract-conformant
fixture-conformant
sdk-direct-conformant
sdk-proxy-conformant
```

Do not print a maturity level that CI does not enforce.

---

## 17. CI contract

Fast job:

```text
gofmt check
go vet ./...
go test -race ./...
make generate-check
```

Node SDK job:

```text
install pinned e2e/sdk dependencies
build fab
run direct official SDK suites
run proxy official SDK suites marked supported
assert no external network destinations were reached
```

End-to-end `fab run` job:

```bash
fab run \
  --environment ./e2e/environments/http-reference.yaml \
  --artifacts ./test-results/fabricate \
  -- npm test
```

Artifacts:

```text
test-results/fabricate/
  environment.json
  requests.ndjson
  logs/
  state-diff/
```

No Docker is required for the reference HTTP API test path. Optional Docker
isolation tests may exist, but they must run the same Go behavior and scenarios.

---

## 18. Implementation phases

Each phase must leave one working vertical slice. Do not create all interfaces
and postpone real behavior until the end.

### Phase 0: foundation and code-generation spike

Deliverables:

- one Go module target layout;
- pinned OpenAPI generator tooling;
- pinned OpenAPI validation dependencies using patched releases;
- minimal `httpresource` interfaces;
- deterministic `make generate` and `make generate-check`;
- a tiny internal test spec proving strict stdlib server generation,
  request validation, embedded spec access, and custom Go behavior;
- ADR for SQLite driver and single-module build;
- no production resource migration yet.

Exit criteria:

- generated code compiles from a clean checkout;
- generation is byte-for-byte deterministic;
- invalid requests are rejected before behavior runs;
- security validation has an explicit authentication function;
- no client generation is enabled.

### Phase 1: Gmail reference resource

Rewrite Gmail on the new path. Old Gmail code may be mined for behavior and
tests, but do not wrap `mock.Service`.

Deliverables:

- curated Gmail OpenAPI document containing the supported profile, label,
  message, thread, modify, send, and trash operations;
- generated strict Go server and models;
- new Gmail server implementing the generated interface;
- resource-specific scenario JSON Schema;
- `minimal.v1` and `acme-corp.v1` scenarios;
- SQLite schema and scenario codec;
- deterministic IDs and clock;
- mandatory generation, scenario, and provider fixture tests;
- `googleapis` direct SDK test using `acme-corp.v1`;
- no Docker requirement.

Minimum SDK behavior:

```text
list unread messages
  -> fetch one seeded message/thread
  -> modify labels or send a reply
  -> read again and prove persistence
```

Exit criteria:

- Gmail uses no `mock.Service` or custom path router;
- its scenario loads, dumps, captures, and resets;
- `googleapis` direct test passes;
- two Gmail instances have isolated state.

### Phase 2: environment supervisor and direct access

Deliverables:

- environment manifest parser and strict validation;
- scenario resolver for embedded catalog and project-local paths;
- HTTP engine with retained per-service listeners;
- supervisor and Unix-socket control API;
- detached `environment up/down/status/services/connections` commands;
- `fab run` without proxy;
- JSON, dotenv, and shell connection output;
- aggregate readiness and partial-start rollback;
- request logs and service status;
- two-service reference environment.

Exit criteria:

- `fab run --environment ... -- node test.mjs` starts Gmail, injects its
  direct endpoint, runs the child, and tears everything down;
- parallel runs do not share ports, state, or credentials;
- stdout remains machine-parseable.

### Phase 3: scenario authoring loop

Deliverables:

- `scenario list/show/new/validate/diff/load/capture`;
- replacement load using a temporary baseline;
- reset through the supervisor;
- canonical JSON and digests;
- atomic capture writes and overwrite refusal;
- secret scanning;
- state revision reporting.

Exit criteria:

- a developer copies `gmail.acme-corp.v1`, edits it, validates it,
  starts it, mutates it through `googleapis`, captures a new version, resets,
  and observes the original baseline;
- no runtime scenario inheritance exists.

### Phase 4: transparent proxy

Deliverables:

- host routing from resource descriptors and environment overrides;
- per-environment ephemeral CA;
- CONNECT/TLS proxy with no public fallback;
- process-scoped trust injection for Node;
- request metadata preserving external scheme and host;
- redacted trace logging;
- unknown-host and ambiguous-host failures;
- Gmail official SDK proxy test.

Exit criteria:

- `googleapis` runs against its normal vendor host through
  `fab run --proxy` without setting a Gmail base URL;
- packet/request assertions prove no Google endpoint was contacted;
- direct mode still works unchanged.

### Phase 5: GitHub clean-slate resource

Rewrite GitHub as a Go HTTP resource using a curated GitHub OpenAPI subset and
scenario state. Do not retain ordered post-seed HTTP replay.

Deliverables:

- GitHub OpenAPI, generated server, models, and embedded spec;
- canonical scenarios containing users, orgs, repos, labels, milestones,
  issues, comments, and supported relationships directly;
- provider-shaped URLs generated from request context;
- Octokit direct and proxy conformance;
- fixture and reset/capture tests;
- deletion of the Emulate runtime and proxy.

The Emulate runtime, image/release path, seed types, HTTP profiles, and
compatibility documentation have already been deleted. Reintroduction starts
at `resources/github`; no legacy adapter or ordered post-seed replay is allowed.

Exit criteria:

- Octokit direct and proxy suites pass on the Go resource;
- no production build installs or launches Node/Emulate;
- no response-body proxy rewrite exists.

### Phase 6: Stripe clean-slate resource

Stripe proves a new resource can be added without touching the HTTP engine.

Deliverables:

- curated Stripe OpenAPI subset;
- generated strict server and models;
- custom Go behavior for customers, products, prices, payment intents,
  subscriptions, refunds, and the explicitly selected initial surface;
- `minimal.v1`, `duolingo.v1`, and `netflix.v1` scenarios;
- Stripe ID generation, relationships, and state transitions;
- official `stripe` direct and proxy tests;
- no engine, registry, or CLI special cases.

Exit criteria:

- adding Stripe changes only resource registration plus resource-owned files;
- its scenarios pass the generic state conformance suite;
- the official SDK passes read/write/read in direct and proxy modes.

### Phase 7: remaining HTTP resource reintroduction

Reintroduce selected former services one at a time only when they meet the new
contract. A resource is not carried forward merely for catalog count. The old
engine, custom router, module, service registry, HTTP profiles, seed type,
image, scaffolding, and compatibility documentation are already deleted.

Exit criteria:

- one official resource registry exists;
- every reintroduced HTTP resource owns compiled OpenAPI and scenarios;
- no old HTTP engine or service framework returns to the build graph.

---

## 19. First implementation worklist

The first agent should execute this order rather than starting with broad
renames:

1. Add the target packages with only the interfaces required by a tiny test
   resource.
2. Pin `oapi-codegen` and OpenAPI validation tooling.
3. Add deterministic generation and CI dirty-checking.
4. Prove strict generated stdlib server construction and request validation.
5. Implement scenario envelope parsing, strict unknown-field rejection,
   canonical JSON, and digests.
6. Implement a temporary-directory state manager with baseline/live SQLite.
7. Write state manager tests for load, reset, failure cleanup, and concurrent
   access before migrating Gmail.
8. Write the curated Gmail OpenAPI subset.
9. Generate Gmail Go artifacts.
10. Implement Gmail from the generated interface, using the old implementation
    only as behavioral evidence.
11. Add Gmail resource scenario codec and reference scenarios.
12. Port or rewrite valuable Gmail fixture assertions through the generated
    handler and shared middleware.
13. Add the official `googleapis` direct conformance test.
14. Only after Gmail is green, implement supervisor/environment CLI work.

Do not start GitHub, proxying, or broad catalog migration before the Gmail
vertical slice proves the generated contract and state lifecycle.

---

## 20. Definition of done for a resource

A resource may be registered as official only when all applicable items pass:

- [ ] Curated OpenAPI source exists.
- [ ] Upstream source/revision/license is documented when applicable.
- [ ] Every operation has a stable unique `operationId`.
- [ ] Generated strict server and models are committed.
- [ ] Generated embedded/normalized spec is committed.
- [ ] `make generate-check` is clean.
- [ ] Resource implements the generated strict interface.
- [ ] No direct path registration exists in resource code.
- [ ] Scenario JSON Schema exists.
- [ ] At least `minimal.v1` and one realistic scenario exist.
- [ ] Every catalog scenario validates, loads, dumps, and resets.
- [ ] Read/write/read provider fixture tests pass.
- [ ] Provider-shaped error tests pass.
- [ ] Two instances prove state isolation.
- [ ] Deterministic clock and ID tests pass.
- [ ] Official Node SDK direct test passes when an SDK exists.
- [ ] An explicit documented exception exists when no suitable SDK exists.
- [ ] Proxy SDK test passes before proxy support is advertised.
- [ ] No test silently skips in CI.
- [ ] Resource documentation shows direct SDK configuration.
- [ ] Request logs redact credentials and sensitive fields.
- [ ] Graceful close has no leaked goroutines or DB handles.

---

## 21. Footguns the implementation must prevent

1. **Self-consistent wrong contracts.** Generated Go code can agree with a
   wrong OpenAPI document. Official SDK tests are the independent fidelity
   check.
2. **Publishing an oversized vendor spec.** Only declare operations that work.
3. **Runtime route duplication.** Generated routing is the only provider-route
   declaration.
4. **No-op authentication validation.** OpenAPI security declarations do not
   automatically enforce credentials; configure authentication explicitly.
5. **Package-global mutable state.** It leaks across service instances and
   reset.
6. **One SQLite DB for every service.** It creates table collisions and reset
   coupling. Use one DB pair per service instance.
7. **Port probing races.** Bind port zero and retain the listener.
8. **Path-prefix mounting by default.** It breaks SDK root paths, OAuth,
   redirects, and signed URLs. Use separate listeners.
9. **Response-body URL rewriting.** Generate correct URLs from trusted request
   context.
10. **Ambiguous proxy hosts.** Require an explicit mapping when two instances
    use the same provider host.
11. **Proxy public fallback.** Unknown hosts must fail closed.
12. **Global CA installation.** Trust the ephemeral CA only in the launched
    process.
13. **Real credentials in scenarios or logs.** Secret-scan scenarios and redact
    request data.
14. **Hidden seed hooks.** Scenario data must explain the baseline completely.
15. **Scenario inheritance chains.** V1 copies and materializes instead.
16. **Copying live SQLite without checkpoint/close.** Reset and baseline copies
    must use a proven safe sequence.
17. **Publishing partial connections.** Wait for aggregate readiness.
18. **SDK test skips.** Missing prerequisites in CI are failures.
19. **Keeping old HTTP code beside the new stack indefinitely.** Each migrated
    vertical slice ends with deletion of its obsolete implementation.
20. **Treating same-process adapters as untrusted.** Official compiled resources
    are trusted code; future community resources require a process boundary.

---

## 22. Final acceptance scenario

The refactor is successful when this works from a clean checkout without
Docker:

```bash
make generate-check
make test

fab scenario new stripe duolingo-custom --from stripe.duolingo.v1
fab scenario validate ./fabricate/scenarios/stripe/duolingo-custom.v1.json

fab run \
  --environment ./fabricate/environments/duolingo-ci.yaml \
  --artifacts ./test-results/fabricate \
  -- npm test
```

The test process uses official provider SDKs against direct Fabricate
endpoints, observes scenario state, performs writes, and reads the mutations.
Afterward, proxy conformance runs the same official SDKs on their normal vendor
hosts through:

```bash
fab run --environment ./fabricate/environments/duolingo-ci.yaml --proxy -- npm test
```

No request reaches a real provider. Reset restores the exact scenario
baseline. Capture writes a new standalone scenario. The supervisor tears down
all listeners, DB handles, credentials, and CA material. No Node or Emulate
runtime is part of Fabricate itself.

At that point, adding another ordinary HTTP resource means adding one resource
package, its curated OpenAPI contract, generated Go artifacts, scenarios,
behavior, registry entry, fixture tests, and official SDK tests. It does not
require changes to the HTTP engine, proxy, supervisor, CLI lifecycle, or state
manager.

---

## 23. Primary implementation references

Use primary project documentation when finalizing generator flags and pinned
versions:

- OpenAPI specification: <https://spec.openapis.org/oas/v3.0.3>
- `oapi-codegen`: <https://github.com/oapi-codegen/oapi-codegen>
- `oapi-codegen` configuration:
  <https://github.com/oapi-codegen/oapi-codegen/blob/main/docs/configuration.md>
- `kin-openapi`: <https://github.com/getkin/kin-openapi>

At implementation time, check current security advisories before pinning an
OpenAPI validation dependency. Keep Fabricate's synthetic authentication
enforcement explicit and independently tested even when request validation
middleware also interprets OpenAPI security declarations.
