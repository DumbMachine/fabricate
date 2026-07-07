# Wheelio — a full rideshare stack, one incident everywhere

This example replicates a small company's whole surface with fab: a
transactional database, a dispatch cache, a support inbox, an ops
board, and a Kubernetes deployment — **and one coherent incident
threaded through all of them**, so an agent (or a workshop audience)
can investigate across resources the way real on-call does.

**The story.** On `2026-07-03 14:05 UTC`, CI deployed
`payments-webhook v2.4.1` — but the image tag was never pushed, so the
rollout is stuck in `ImagePullBackOff`. Payment captures stopped:

| Resource | What the incident looks like there |
|---|---|
| `postgres` `rideshare-core` | 14 trips completed but payments stuck `pending` (`v_unpaid_completed_trips`), per-driver payout gaps |
| `redis` `rideshare-dispatch` | live GEO driver positions, surge zones, and a `stats:payments:pending_alerts` counter climbing |
| `gmail` `rideshare-support` | 5 unread: driver payout complaints citing **TR-1042**/**TR-1054**, a rider stuck on "processing" for **TR-1049** |
| `linear` `rideshare-ops` | **OPS-301** (urgent, in progress) with a comment trail pointing at the k8s rollout; follow-ups OPS-302..304 |
| `k8s/rideshare.yaml` | the actually-broken `payments-webhook` Deployment (plus two healthy services) |

Every trip id, timestamp, and claim cross-references: the email
complaints are verifiable with SQL, the Linear comments quote the real
kubectl symptoms, and fixing the Deployment is the real fix.

## Up / down

```bash
cd examples/rideshare
./up.sh              # 4 fab instances (Docker; needs `make images` once)
./up.sh --k8s        # + apply k8s/ to your CURRENT kubectl context (use kind!)
./down.sh --k8s
```

Or piecemeal — the profiles are a normal template pack:

```bash
fab profiles add ./examples/rideshare --list
fab profiles add dumbmachine/fabricate/examples/rideshare --all   # straight from GitHub
fab create postgres -p rideshare-core
```

## The investigation

A suggested trail an agent can follow cold — each step's output points
at the next resource:

```bash
# 1. Support inbox: what are people angry about?
eval "$(fab creds rideshare-support --env)"
curl -s "$FAB_URL/gmail/v1/users/me/messages?q=is:unread+label:payouts" | jq .resultSizeEstimate
curl -s "$FAB_URL/gmail/v1/users/me/threads/thr-payout-drv11" | jq '.messages[].snippet'
# → payouts missing for TR-1042, TR-1054, since ~Jul 3

# 2. Verify in the core DB: is the money really missing?
eval "$(fab creds rideshare-core --env)"
psql "$DATABASE_URL" -c "SELECT count(*), min(completed_at) FROM v_unpaid_completed_trips;"
psql "$DATABASE_URL" -c "SELECT * FROM v_driver_payout_gap ORDER BY missing_cents DESC LIMIT 5;"
# → 14 unpaid trips, all completed AFTER 2026-07-03 14:05 UTC. Not user error.

# 3. Ops board: is this known?
eval "$(fab creds rideshare-ops --env)"
curl -s "$FAB_URL/graphql" -d '{"query":"query($id: String!){ issue(id: $id){ title state { name } comments { nodes { body } } } }","variables":{"id":"OPS-301"}}' | jq .data.issue
# → v2.4.1 rollout stuck, ImagePullBackOff, rollback vs fix-forward debate

# 4. The cluster (needs ./up.sh --k8s): confirm and fix.
kubectl -n rideshare get pods                       # payments-webhook: ImagePullBackOff
kubectl -n rideshare describe pod -l app=payments-webhook | tail -5
kubectl -n rideshare set image deploy/payments-webhook webhook=nginx:1.27-alpine   # "rollback"
kubectl -n rideshare rollout status deploy/payments-webhook                        # → Ready

# 5. Close the loop — these writes stick:
curl -s "$FAB_URL/graphql" -d '{"query":"mutation($id: String!, $in: IssueUpdateInput!){ issueUpdate(id: $id, input: $in){ success } }","variables":{"id":"OPS-301","in":{"stateId":"st-monitor"}}}'
RAW=$(printf 'To: driver11@example.net\nSubject: Re: Payout missing for trip TR-1042\n\nFound it — a bad deploy on our side stopped payout processing. Fixed; your TR-1042 and TR-1054 payouts land within 24h.' | base64 | tr '+/' '-_' | tr -d '=\n')
eval "$(fab creds rideshare-support --env)"
curl -s -X POST "$FAB_URL/gmail/v1/users/me/messages/send" -d "{\"raw\": \"$RAW\", \"threadId\": \"thr-payout-drv11\"}"
```

(Backfilling the 14 pending captures in SQL is OPS-302 — left as an
exercise, which is the point.)

## Layout

```
examples/rideshare/
├── profiles/            # a normal fab template pack (4 profiles)
│   ├── postgres/rideshare-core/       schema + deterministic seed + views
│   ├── redis/rideshare-dispatch/      GEO drivers, surge, queue
│   ├── gmail/rideshare-support/       the inbox fixture
│   └── linear/rideshare-ops/          the board fixture
├── k8s/rideshare.yaml   # 3 Deployments; payments-webhook intentionally broken
├── up.sh / down.sh
└── README.md
```

Copy this directory as the template for your own scenario: change the
story, keep the pattern — *seed every resource with the same incident
and make the trail cross-reference itself.*
