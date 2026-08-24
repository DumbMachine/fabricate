# fabricate

**A registry for live, stateful environments.** Instead of hand-rolling fixtures, pull a ready-made one from the catalog — a seeded database, a cache, a real API mock — spin it up in one command, point your code at the credentials, and throw it away.

```
fab create postgres -p stripe-payments
# → postgresql://postgres:Nd1aStFzdjcT4vCERuGjYpGH@localhost:62935/payments

fab create gmail -p support-inbox
# → http://localhost:59320   (a stateful Gmail API with a support backlog)
```

Each entry in the catalog is a declarative **profile** (`fab profiles` lists them). Two kinds of environment back them:

- **Container engines** (via [testcontainers-go](https://github.com/testcontainers/testcontainers-go)): Postgres, MySQL, MongoDB, Redis, Prometheus, SSH hosts, k3s Kubernetes, a Moto-backed fake AWS.
- **Stateful HTTP-API mocks** (the `httpmock` engine): real HTTP servers for REST APIs like Gmail, Google Play, Vercel, Cloudflare — backed by SQLite, so **writes actually take effect**. Reply to an email and the thread grows; the next read reflects it.

The catalog is **open**: built-ins ship in the box, you can install more from any GitHub repo (`fab profiles add owner/repo`), and [contributions are welcome](#contributing). We aim for every mock to be **drop-in compatible with the service's official client SDK** — not just stateful but wire-compatible, verified end to end (Gmail/Play against `googleapis`, GitHub against `@octokit/rest`; Stripe against npm `stripe` when that mock ships). GraphQL mocks (Linear, Railway) are **experimental** — they answer hand-written queries but don't yet satisfy the generated GraphQL SDKs.

## Why this exists

If you're building an agent (or testing an app) that talks to databases, inboxes, and issue trackers, you need something real to talk to. Standing those environments up yourself is the hard part — fiddly to seed, a chore to keep current, and painful to reproduce — and the moment you want a *different* scenario, or ten of them running at once, you're back to hand-rolling fixtures. Recorded HTTP fixtures don't cut it either: they break the instant your code does a write and expects to read it back.

`fabricate` is that environment, on demand. One command spins up a real, seeded engineering resource — a Postgres/MySQL/Mongo database, a Redis cache, an SSH host, a Kubernetes cluster, or a stateful HTTP-API mock — with realistic state already inside (a payments database with two seeded reconciliation bugs, a support inbox with an angry follow-up waiting). Point your code at the credentials, exercise the full read/write loop, throw it away. Because each one is just a container, everything you already do with containers still works — snapshot one to pin a scenario, run N in parallel to cover more of them at once.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/DumbMachine/fabricate/main/install.sh | sh
```

That drops the `fab` binary on your PATH — nothing else to configure. The only runtime requirement is a running Docker daemon (Docker Desktop, OrbStack, or Colima all work); every engine's container image is pulled automatically on first use, including fabricate's own `httpmock` and `emulate` images.

```bash
fab create gmail -p support-inbox      # first run pulls the image, then you're in
```

<details>
<summary><b>Build from source</b></summary>

Requires Go 1.25+ and Docker.

```bash
git clone https://github.com/dumbmachine/fabricate
cd fabricate
make install          # → ~/bin/fab
make images           # build the httpmock + emulate images locally
```

A source build defaults to the local `fabricate/httpmock:local` / `fabricate/emulate:local` images (what `make images` builds), so unlike the released binary it doesn't auto-pull them. Set `FAB_HTTPMOCK_IMAGE` / `FAB_EMULATE_IMAGE` to point at a published copy instead. Publishing details: [docs/releasing.md](docs/releasing.md).

</details>

## Example 1: A seeded postgres

`stripe-payments` is Stripe-shaped payments data — customers, payment intents, charges, refunds, a balance ledger — with two bugs planted in it:

```bash
$ fab create postgres -p stripe-payments

fab: starting postgres instance "stripe-payments" from profile "stripe-payments" on target docker...
fab: provisioning container (timeout 5m0s)...
fab: ready in 4.789s
name        stripe-payments
engine      postgres
target      docker
profile     stripe-payments
image       postgres:16-alpine
id          deab95cc88e6
host        localhost
port        63084
username    payments
password    uWBp5ZhiF7LwrIiDZLzGEDVH
database    payments
url         postgresql://payments:uWBp5ZhiF7LwrIiDZLzGEDVH@localhost:63084/payments?sslmode=disable
created_at  2026-07-09T04:01:04Z

$ eval "$(fab creds stripe-payments --env)"    # sets DATABASE_URL, FAB_HOST, ...

$ psql "$DATABASE_URL" <<'SQL'
-- Bug #1: a refund missing from the ledger.
SELECT r.id, r.amount_cents
  FROM refunds r
  LEFT JOIN balance_transactions bt
    ON bt.source_type = 'refund' AND bt.source_id = r.id
 WHERE bt.id IS NULL;
SQL

      id       | amount_cents
---------------+--------------
 re_0000000017 |          223
(1 row)


$ fab destroy stripe-payments

fab: destroyed stripe-payments
```

Other seeded databases: `pagila` (DVD rental), `finance-ledger` (unbalanced journals to find), `saas-analytics` (a BI warehouse with 250k events), `orders-slow` (MySQL with a missing index), `neobank-ledger` (MongoDB with structuring patterns to detect). Run `fab profiles` for the full catalog.

## Example 2: Gmail

> **GraphQL mocks (e.g. Linear) are under active development.** They answer
> hand-written GraphQL but are not yet compatible with the official
> generated SDKs (`@linear/sdk`), which assume the full connection /
> `pageInfo` / relation-resolution contract. Treat the GraphQL engine as
> experimental until this note is removed. The REST mock below (Gmail) is
> the stable, worked example.

`support-inbox` is a Gmail-API mailbox with a support backlog to triage. Here's the statefulness in one sitting — watch the numbers change as the writes land:

```bash
$ fab create gmail -p support-inbox
fab: starting httpmock instance "support-inbox" from profile "support-inbox" on target docker...
fab: provisioning container (timeout 5m0s)...
fab: ready in 603ms
name        support-inbox
engine      httpmock
target      docker
profile     support-inbox
image       fabricate/httpmock:local
id          b8f134934b6d
host        localhost
port        63205
username
password    fab-gmail-token
database
url         http://localhost:63205
created_at  2026-07-09T04:05:38Z

$ eval "$(fab creds support-inbox --env)"

# The backlog: 5 unread, including an angry double-charge thread of 2 messages.
$ curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread" | jq .resultSizeEstimate
# 5
$ curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge" | jq '.messages | length'
# 2

# Reply (messages.send takes base64url RFC 822, like the real API):
RAW=$(printf 'To: dana@northwind.io\nSubject: Re: Charged twice for March invoice\n\nRefund for the duplicate charge is on its way — 3-5 business days.' \
      | base64 | tr '+/' '-_' | tr -d '=\n')
curl -s -X POST "$FAB_URL/gmail/v1/users/me/messages/send" \
     -d "{\"raw\": \"$RAW\", \"threadId\": \"thr-doublecharge\"}"
# {"id":"msg-sent-0011","labelIds":["SENT"],"threadId":"thr-doublecharge"}

# The write took — the thread grew, and the last message is yours:
curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge" \
  | jq '{messages: (.messages | length), last_from: (.messages[-1].payload.headers[] | select(.name == "From").value)}'
# {
#   "messages": 3,
#   "last_from": "support@acme.dev"
# }

# Mark the follow-up handled — UNREAD is gone from its labels...
curl -s -X POST "$FAB_URL/gmail/v1/users/me/messages/msg-0002/modify" \
     -d '{"removeLabelIds": ["UNREAD"]}' | jq .labelIds
# ["IMPORTANT", "INBOX", "Label_billing"]

# ...and the backlog really shrank: 5 → 4.
curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread" | jq .resultSizeEstimate
# 4
```

Because state lives in SQLite inside the container, the full agent loop — *read, decide, write, re-read* — works against the mock exactly like it would against the real service. (Every output above is captured from a real run, not mocked-up.)

## A whole company in one incident

[`examples/rideshare/`](examples/rideshare/) replicates a small rideshare's entire surface — Postgres core, Redis dispatch cache, Gmail support inbox, Linear ops board, and Kubernetes manifests with one **intentionally broken** Deployment — all seeded with the *same* incident, cross-referenced down to individual trip IDs. `./up.sh` and start investigating like on-call would.

## Agent skills

The [`skills/`](skills/) directory ships [agent skills](https://github.com/vercel-labs/skills) for fab itself, each flagship mock (linear, gmail, google-play), and [contributing](skills/contributing/SKILL.md) (how to add a service to the catalog):

```bash
npx skills add dumbmachine/fabricate
```

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

A profile is a directory: a `profile.yaml` plus seed files that run at boot (SQL for Postgres/MySQL, JS for Mongo, redis-cli lines, a JSON fixture for httpmock services, ...). Check the `/examples` folder.

```bash
fab profiles schema                            # the commented profile.yaml reference
fab profiles show support-inbox --files        # a working example's seed files (names + sizes)
fab profiles show support-inbox --file seed.json   # one file, head-capped — safe for agents
fab profiles init linear my-board --from sprint-board   # start from the working example
fab create linear -p my-board
```

User profiles shadow built-ins with the same name; set `FAB_PROFILES_DIR` to relocate the directory.

### Template packs

Any GitHub repo (or local directory) laid out like the built-in catalog — `<slug>/<name>/profile.yaml` — is an installable profile pack, degit-style (one tarball, no git, no history):

```bash
fab profiles add acme/fab-profiles --list            # discover
fab profiles add acme/fab-profiles --profile checkout-db
fab profiles add acme/fab-profiles#v1.2.0 --all      # pin a tag; GITHUB_TOKEN for private repos
```

Installed profiles land in your user dir and immediately work with `fab create`. This makes profile packs shareable the way `npx skills add owner/repo` shares agent skills.

Organizations can also ship a **private profile catalog** as code: embed a directory in your own small wrapper binary and register it with `profile.RegisterCatalog` — your private profiles appear alongside the built-ins. See [docs/private-catalogs.md](docs/private-catalogs.md).

## Extending it

Three extension points, cheapest first — pick the first row that fits:

| You want | Write | Guide |
|---|---|---|
| Your own data/config in a container fab already runs | a **profile** (YAML + seed files, no code) | [docs/authoring-profiles.md](docs/authoring-profiles.md) |
| A stateful mock of an HTTP API (Stripe, Slack, Jira, an internal service) | a **mockd service** (one Go package + one registry line) | [docs/adding-a-mock-service.md](docs/adding-a-mock-service.md) |
| New infrastructure software entirely (Kafka, MinIO, Elasticsearch...) | an **engine** (Info/Create/Destroy) | [docs/adding-an-engine.md](docs/adding-an-engine.md) |

## Contributing

The catalog grows by contribution — **new services and profiles are welcome**. The fastest way in (for a human or a coding agent) is the [`fabricate-contributing`](skills/contributing/SKILL.md) skill, which maps out exactly what to add and where: the service package under `mockd/services/`, the two registry lines, the profile, the tests, and the verify loops.

The bar for a mock is **compatibility with the service's official client SDK**, not just statefulness — a real integration should be able to point its SDK at the mock and work, writes included. New REST services ship an SDK-conformance test under [`e2e/sdk/`](e2e/sdk/) that drives the vendor's own client against the mock; `make sdk-e2e` runs them, and CI keeps them green. (GraphQL is still experimental — see above.)

## Development

```bash
make check     # fmt + vet + unit tests, no Docker, seconds
make e2e       # real containers through the real CLI (needs Docker + make images)
make verify    # both — run before calling anything done
```

## License

MIT
