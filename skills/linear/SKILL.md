---
name: fabricate-linear
description: Run a stateful local mock of the Linear GraphQL API with fabricate (fab create linear). Use when developing or testing an agent/app that reads a Linear board, creates/updates issues, or comments — without touching a real Linear workspace.
allowed-tools: Bash(fab:*), Bash(curl:*)
---

# Linear mock (fabricate)

A SQLite-backed Linear API subset on `POST /graphql`. Mutations really
mutate: `issueCreate` allocates the next `ENG-<n>` identifier and the
next `issues` query returns it.

## Start

```bash
fab create linear -p sprint-board     # built-in: ENG team mid-sprint
eval "$(fab creds sprint-board --env)"   # FAB_URL, FAB_PASSWORD
```

`sprint-board` seeds 3 users, 6 workflow states, 8 issues (one urgent
and unassigned: ENG-101), and comments. Custom board:
`fab profiles init linear my-board --from sprint-board`, edit
`seed.json`, `fab create linear -p my-board`.

## Supported operations

Queries: `viewer`, `teams`, `users`, `workflowStates`,
`issue(id:)` (accepts the opaque id or `"ENG-104"`),
`issues(filter:, first:)`.

Filter subset (ANDed, via variables):
`state.name.eq`, `state.type.eq`, `team.key.eq`,
`assignee.null`, `assignee.email.eq`, `priority.eq|lte|gte`.

Mutations: `issueCreate(input:)`, `issueUpdate(id:, input:)`,
`commentCreate(input:)` (posts as the fixture's viewer).

Anything else returns a GraphQL error — the mock answers what real
clients send, it is not a full executor. Auth is not validated.

The mock dispatches on the query text and reads variables **by name**,
not by resolving the GraphQL argument mapping. So the variables must be
named exactly `filter`, `id`, and `input` — a query written as
`issues(filter: $f)` with a `$f` variable silently gets no filter (it
returns everything); a mutation with `input: $in` fails with
`"id and input are required"`. Name them `$filter`/`$id`/`$input`.

## Examples

```bash
# Urgent and unassigned? (variable MUST be named `filter`)
curl -s "$FAB_URL/graphql" -d '{
  "query": "query($filter: IssueFilter) { issues(filter: $filter) { nodes { identifier title } } }",
  "variables": {"filter": {"assignee": {"null": true}, "priority": {"lte": 1}}}
}'

# Take it: assign + move (stateIds come from a workflowStates query).
curl -s "$FAB_URL/graphql" -d '{
  "query": "mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success issue { state { name } } } }",
  "variables": {"id": "ENG-101", "input": {"assigneeId": "usr-val", "stateId": "st-progress"}}
}'

# Create a follow-up; response carries the fresh identifier.
curl -s "$FAB_URL/graphql" -d '{
  "query": "mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { identifier } } }",
  "variables": {"input": {"teamId": "team-eng", "title": "Add pool metrics", "priority": 2}}
}'
```

## Fixture shape (seed.json)

```bash
fab profiles show sprint-board --file seed.json --head 40
```

Top-level keys: `viewer`, `teams`, `users`, `workflowStates`
(id/teamId/name/type/position), `issues`
(id/teamId/number/title/description/priority/stateId/assigneeId/createdAt),
`comments`. Priority follows Linear: 0 none, 1 urgent, 2 high,
3 normal, 4 low.
