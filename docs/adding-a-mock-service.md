# Adding a mock service

A mock service is one Go package under `mockd/services/` plus one
registry line and one profile. No engine changes, no CLI changes.
**Every API-like service lives here** — Gmail, Stripe, GitHub, Slack,
Linear — on the same HTTP / OpenAPI engine and `mock.Service` iface.
Do not add a new `engine/` package or wrap an existing HTTP mock
(vercel-labs emulate, Prism, etc.) as a second runtime.

Use `services/gmail` as the REST template and `services/linear` as the
GraphQL template. GitHub's current emulate engine is a stopgap; a
real GitHub service is this same package shape, not an emulate
wrapper.

## 1. The service package

```go
// Package payments is a stateful mock of <the API subset you need>.
package payments

import (
    "database/sql"
    "github.com/dumbmachine/fabricate/mockd/mock"
)

func New() *mock.Service {
    s := mock.NewService("payments")
    s.Tables = []mock.Table{
        {Name: "charges", DDL: "id TEXT PRIMARY KEY, amount INTEGER, status TEXT"},
    }
    s.Seed = seed // func(db *sql.DB, fixture []byte) error — parse JSON, insert rows

    s.GET("/v1/charges", listCharges)          // {param} segments are captured
    s.GET("/v1/charges/{id}", getCharge)       // into c.Params
    s.POST("/v1/charges/{id}:capture", capture) // AIP-style :verb suffixes work
    return s
}
```

Handlers receive `*mock.Ctx`: `DB` (the live SQLite), `Params`,
`Query`, `Body`, and helpers `Bind` (JSON in), `JSON` (respond),
`GErr` (Google-style error envelope). Return errors only for
"already wrote the response, log this" cases; API errors are
`c.GErr(404, "NOT_FOUND", "...")` + `return nil`-style via the helper.

Design rules that keep mocks useful:

- **Make writes real.** The point of SQLite-backed mocking is that
  `POST` mutates rows the next `GET` returns. If a write is fire-and-
  forget, the mock teaches clients the wrong lessons.
- **Mirror the real API's envelopes** (field names, pagination shape,
  error format) for the subset you implement. Clients — especially
  agent tool-use code — are written against docs of the real service.
- **Reject what you don't implement loudly** (404 with a clear message,
  or a GraphQL error) rather than returning empty success.
- For GraphQL APIs, don't build an executor: dispatch on the query
  text (`strings.Contains(query, "issueCreate")`) and read variables
  from the request, the way `services/linear` and `services/railway`
  do. Document the supported operations in the package comment.

## 2. Register it

Add the import + one line to the `registry` map in `mockd/main.go`:

```go
"payments": payments.New,
```

## 3. Tests (before the profile)

The whole service is unit-testable without Docker:

```go
svc := New()
svc.Init(":memory:", []byte(fixtureJSON))
rec := httptest.NewRecorder()
svc.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/charges", nil))
```

Cover the stateful loop, not just reads: create/modify something, then
assert the follow-up read reflects it. Run with `make check`.

## 4. The profile

```
profiles/payments/demo-account/
├── profile.yaml    # engine: httpmock, env.MOCK_SERVICE: payments,
│                   # seed: [{type: mock-fixture, file: seed.json}]
└── seed.json       # the fixture your Seed func parses
```

Add `all:payments` to the `//go:embed` directive in
`profiles/embed.go`. Seed something with a story in it — see
[authoring-profiles.md](authoring-profiles.md).

## 5. Verify end to end

```bash
make check                 # unit loop
make images                # rebuild fabricate/httpmock:local with your service
make install
fab create payments -p demo-account
eval "$(fab creds demo-account --env)"
curl -s "$FAB_URL/v1/charges"
fab destroy demo-account
```

If the service has an official client library, add an SDK-conformance
test: `e2e/sdk/<service>.test.mjs` driving the vendor's **first-party JS
SDK** (Stripe: npm `stripe` with `host`/`port`/`protocol`; GitHub:
`@octokit/rest` with `baseUrl`; Google: `googleapis` with `rootUrl`).
Copy `gmail.test.mjs` or `github.test.mjs`. Add the dep to
`e2e/sdk/package.json` and run `make sdk-e2e`. This is the contract that
stops a stateful-but-non-compliant mock from shipping. See
[sdk-conformance.md](sdk-conformance.md). GraphQL generated clients
(Linear `@linear/sdk`) are not the bar until the mock speaks the real
schema.
