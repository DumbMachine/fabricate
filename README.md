# fabricate

Real, seeded, throwaway resources for testing agents and apps — one command, credentials out.

```
fab create postgres -p stripe-payments
# → postgresql://postgres:Nd1aStFzdjcT4vCERuGjYpGH@localhost:62935/payments

fab create linear -p sprint-board
# → http://localhost:59320   (a stateful Linear API with a sprint mid-flight)
```

`fab` provisions two kinds of fixtures from declarative **profiles**:

- **Container engines** (via [testcontainers-go](https://github.com/testcontainers/testcontainers-go)): Postgres, MySQL, MongoDB, Redis, Prometheus, SSH hosts, k3s Kubernetes, a Moto-backed fake AWS.
- **Stateful HTTP-API mocks** (the `httpmock` engine): real HTTP servers for services like Linear, Gmail, Google Play, Vercel, Cloudflare — backed by SQLite, so **writes actually take effect**. Reply to an email and the thread grows; create an issue and it gets the next `ENG-<n>`; the next read reflects it. It behaves like the real API, not a canned response.

## Why this exists

If you're building an agent (or testing an app) that talks to databases, inboxes, and issue trackers, you need something real to talk to — but production is off-limits and live sandboxes are slow, rate-limited, and shared. Recorded HTTP fixtures break the moment your code does a write and expects to read it back.

`fabricate` gives you the same *kinds* of resources with realistic state already inside: a payments database with two seeded reconciliation bugs, a support inbox with an angry follow-up waiting, a sprint board with an urgent unassigned issue. Spin one up, point your code at the credentials, throw it away.

## Install

Requires Go 1.25+ and a running Docker daemon (Docker Desktop, OrbStack, or Colima all work).

```bash
git clone https://github.com/dumbmachine/fabricate
cd fabricate
make install          # → ~/bin/fab
make images           # builds the local images for the httpmock + github engines
```

## Example 1: a seeded database (testcontainers-backed)

`stripe-payments` is Stripe-shaped payments data — customers, payment intents, charges, refunds, a balance ledger — with two bugs planted in it:

```bash
fab create postgres -p stripe-payments
eval "$(fab creds stripe-payments --env)"    # sets DATABASE_URL, FAB_HOST, ...

psql "$DATABASE_URL" <<'SQL'
-- Bug #1: a refund missing from the ledger.
SELECT r.id, r.amount_cents
  FROM refunds r
  LEFT JOIN balance_transactions bt
    ON bt.source_type = 'refund' AND bt.source_id = r.id
 WHERE bt.id IS NULL;
SQL

fab destroy stripe-payments
```

Other seeded databases: `pagila` (DVD rental), `finance-ledger` (unbalanced journals to find), `saas-analytics` (a BI warehouse with 250k events), `orders-slow` (MySQL with a missing index), `neobank-ledger` (MongoDB with structuring patterns to detect). Run `fab profiles` for the full catalog.

## Example 2: a stateful API mock (httpmock-backed)

`sprint-board` is a mock Linear workspace: one team, six workflow states, a sprint mid-flight with an urgent unassigned bug. It answers GraphQL on `/graphql`:

```bash
fab create linear -p sprint-board
eval "$(fab creds sprint-board --env)"       # sets FAB_URL, FAB_PASSWORD

# What's urgent and unassigned?
curl -s "$FAB_URL/graphql" -d '{
  "query": "query($f: IssueFilter) { issues(filter: $f) { nodes { identifier title } } }",
  "variables": {"f": {"assignee": {"null": true}, "priority": {"lte": 1}}}
}'
# → {"data":{"issues":{"nodes":[{"identifier":"ENG-101","title":"Checkout API returns 500 above ~100 rps"}]}}}

# Take it: assign + move to In Progress. This is a real write.
curl -s "$FAB_URL/graphql" -d '{
  "query": "mutation($id: String!, $in: IssueUpdateInput!) { issueUpdate(id: $id, input: $in) { success } }",
  "variables": {"id": "ENG-101", "in": {"assigneeId": "usr-val", "stateId": "st-progress"}}
}'
```

And `support-inbox` is a Gmail-API mailbox with a support backlog to triage:

```bash
fab create gmail -p support-inbox
eval "$(fab creds support-inbox --env)"

curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread"          # the backlog
curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge"      # read the angry thread
# messages.send appends a reply to the thread; modify clears UNREAD —
# and the next is:unread search really shrinks.
```

Because state lives in SQLite inside the container, the full agent loop — *read, decide, write, re-read* — works against the mock exactly like it would against the real service.

## Everyday commands

```bash
fab engines                  # every engine + the seed types it accepts
fab profiles                 # the profile catalog (built-in + yours)
fab profiles show pagila     # what's inside one profile
fab ls                       # live instances
fab creds <name> --url       # reprint credentials any time
fab wait <name>              # block until the port listens (CI)
fab destroy <name>           # stop + remove; --all nukes everything
```

Every command takes `-o {json,table,env,url}` — `json` is the default on a pipe, so it's agent-friendly out of the box.

## Bring your own profiles

A profile is a directory: a `profile.yaml` plus seed files that run at boot (SQL for Postgres/MySQL, JS for Mongo, redis-cli lines, a JSON fixture for httpmock services, ...).

```bash
fab profiles init postgres my-app    # scaffolds ~/.config/fab/profiles/postgres/my-app/
fab profiles schema                  # the commented profile.yaml reference
fab create postgres -p my-app
```

User profiles shadow built-ins with the same name; set `FAB_PROFILES_DIR` to relocate the directory.

Organizations can also ship a **private profile catalog** as code: embed a directory in your own small wrapper binary and register it with `profile.RegisterCatalog` — your private profiles appear alongside the built-ins. See [docs/private-catalogs.md](docs/private-catalogs.md).

## Adding a mock service

New HTTP mocks (a Stripe, a Slack, a Jira...) are a single Go package in [`mockd/services/`](mockd/services/): declare SQLite tables, a fixture loader, and Express-style handlers, then add one registry line. No engine changes. See [docs/adding-a-mock-service.md](docs/adding-a-mock-service.md).

## Development

```bash
make check     # fmt + vet + unit tests, no Docker, seconds
make e2e       # real containers through the real CLI (needs Docker + make images)
make verify    # both — run before calling anything done
```

[CLAUDE.md](CLAUDE.md) documents the repo layout and the verification loops in detail (it doubles as the guide for AI coding agents working on this repo).

## License

MIT
