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

Container engines (postgres, redis, ...) pull their images from Docker Hub on first use; only the `httpmock` and `github` engines run fabricate-built images, which `make images` builds locally. Published copies (and the `FAB_HTTPMOCK_IMAGE` / `FAB_EMULATE_IMAGE` overrides to use them) are described in [docs/releasing.md](docs/releasing.md).

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

And `support-inbox` is a Gmail-API mailbox with a support backlog to triage. Here's the statefulness in one sitting — watch the numbers change as the writes land:

```bash
fab create gmail -p support-inbox
eval "$(fab creds support-inbox --env)"

# The backlog: 5 unread, including an angry double-charge thread of 2 messages.
curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread" | jq .resultSizeEstimate
# 5
curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-doublecharge" | jq '.messages | length'
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

The [`skills/`](skills/) directory ships [agent skills](https://github.com/vercel-labs/skills) for fab itself and each flagship mock (linear, gmail, google-play):

```bash
npx skills add dumbmachine/fabricate            # install into Claude Code / Cursor / ...
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

A profile is a directory: a `profile.yaml` plus seed files that run at boot (SQL for Postgres/MySQL, JS for Mongo, redis-cli lines, a JSON fixture for httpmock services, ...). The CLI teaches the whole authoring loop — no source reading required:

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

## Development

```bash
make check     # fmt + vet + unit tests, no Docker, seconds
make e2e       # real containers through the real CLI (needs Docker + make images)
make verify    # both — run before calling anything done
```

## License

MIT
