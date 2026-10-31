# Deploy the Fabricate console

One container serves the console, control API and sandbox service URLs. The
console uses the same API as external clients. The public marketing/docs site
has a separate Cloudflare deployment; it does not host this stateful service.

## Start locally

From the repository root, create an operator secret and review the settings:

```sh
mkdir -p deploy/console/secrets
chmod 700 deploy/console/secrets
openssl rand -base64 48 > deploy/console/secrets/operator-token
chmod 444 deploy/console/secrets/operator-token
cp deploy/console/.env.example deploy/console/.env

docker compose --env-file deploy/console/.env -f deploy/console/compose.yaml up --build -d
```

The secret is readable by the non-root container user. Its parent directory is
private on the host. Both the secret directory and `.env` are ignored by Git and
excluded from the image build. A secret-manager file can be supplied through
`FAB_CONSOLE_TOKEN_PATH` instead. Never put an operator token in the Dockerfile,
build arguments, a frontend environment variable or a URL.

Open `http://localhost:4173` and enter the operator token. It is exchanged for an
eight-hour HttpOnly, SameSite=Strict browser cookie scoped to the control API and
bound to the advertised origin. HTTPS deployments also set Secure. The frontend
does not save the operator token in localStorage/sessionStorage. Sign out from
Deployment. API callers continue to use `Authorization: Bearer …`.

## Host it

Use a dedicated hostname at the root path. Set `FABRICATE_DOMAIN` to that domain
and `FAB_CONSOLE_PUBLIC_URL` to its HTTPS origin. Point DNS at the host, then run:

```sh
docker compose --env-file deploy/console/.env -f deploy/console/compose.yaml --profile hosted up --build -d
```

The optional Caddy ingress terminates HTTPS on ports 80/443 and forwards all
paths, including `/mcp`, `/api/`, `/s/`, and SPA deep links, to the console. If you already
have ingress, keep the hosted profile off and forward to the loopback-published
port. Set the public URL accurately: it controls advertised service URLs, secure
cookies and browser origin checks. Do not enable permissive CORS as a workaround.

The container runs as UID 10001 with a read-only root, no Linux capabilities,
no Docker socket, a bounded temporary filesystem and a named request-log volume.
The image builds the UI from locked source before embedding it in the Go binary.
A stale checked-in UI cannot silently become the container's deployed UI.

## Check the deployment

```sh
docker compose --env-file deploy/console/.env -f deploy/console/compose.yaml ps
curl --fail http://localhost:4173/healthz
```

Use the Deployment page to check the control API and advertised origin. A useful
smoke test is: sign in, browse the resource catalog, explore a dataset, start a
sandbox, inspect Services / Connect / Activity, copy the API base URL and bare OpenAPI
document into a client, call a service with its token,
close it through the confirmation dialog, reload and sign out. Verify that an
unauthenticated `/api/catalog` request returns 401 and that bearer API calls work.

The health check does not start a sandbox or pretend to verify every provider.
The console supports the curated operations published by each resource contract.
For hosted Access use direct service URLs and service tokens. Transparent proxy
mode additionally needs a reachable proxy and CA distribution; a server-local
proxy URL or file path is not a remote client connection.

## Lifecycle and data

Compose enables `FAB_STATE_DIR=/var/lib/fabricate/state` on the named
`sandbox_state` volume. It stores the session registry, baseline and live SQLite
databases, and service credentials. Sandbox IDs, externally advertised service
URLs, service tokens, creation times and mutated data survive process restarts
and replacement containers using the same volume and public URL. Request logs
use their own retained volume and continue under the same run identity.

Outside Compose, opt in explicitly:

```sh
fab serve --state-dir .fabricate-state --public-url http://localhost:4173
```

`FAB_STATE_DIR` provides the same setting. Without it, `fab serve` and `fab run`
remain disposable. Mounted mode requires a stable public URL and direct service
routing; process-scoped transparent proxy URLs/CA files are not supported.
Changing the advertised public URL is rejected when opening an existing store.

The entire storage directory must be on a local filesystem or attached block
volume with working SQLite locking and sync semantics. Mount the directory,
not individual `.db` files: WAL/journal files must remain beside their databases.
One process owns a store, enforced by an exclusive nonblocking filesystem lock.
Do not share a writable store between workers over NFS/SMB. Keep volume deletion
separate from compute replacement; `docker compose down -v` deletes the data.

Startup reopens existing databases without replaying seed data. It fails before
serving traffic if a ready sandbox has missing files, a corrupt live database or incompatible
resource contracts. Keep the previous image and back up the whole store before
upgrading resource contracts: automatic schema migration is not implemented.
Creation requests and keys are persisted before provisioning. Interrupted
keyed creation resumes using the same sandbox identity; incomplete private
initialization is rebuilt before publication. Interrupted deletion is completed. Normal shutdown preserves all ready sandboxes. The Close
sandbox action remains an explicit destructive deletion, including on disk.

The registry is a secret-bearing file with mode 0600 inside a mode 0700 store;
service credentials are retained there, not encrypted by Fabricate. Protect
host/volume and backup access and use infrastructure encryption at rest as needed.
Operator token rotation takes effect on restart and invalidates browser sessions;
it does not rotate sandbox service tokens. Keep the operator secret mounted.

This protects committed transactions against process/container restarts when
the same healthy storage is retained. It is not a backup or multi-node failover
system. Disk loss requires separate SQLite-aware backups and a tested restore
procedure. Use SQLite's backup API or a coordinated quiesced backup, not an
uncoordinated copy of live database files. Registry and service snapshots must
belong to the same consistent sandbox set. Full multi-tenant accounts,
snapshot/fork APIs and provider-schema migrations remain separate work.

Configure log retention for your environment. The UI's Activity view identifies
a truncated recent trace; it is not an exhaustive history endpoint.

## Repeat the container recovery check

Build the image, then run the isolated verifier with a private directory outside
this checkout:

```sh
docker build -t fabricate-console:durable -f deploy/console/Dockerfile .
python3 deploy/console/verify-durability.py --work-dir /tmp/fabricate-durability-check
```

The verifier uses a fresh operator secret, a loopback port and its own named
volumes. It creates a composition with two independent Asana services and Gmail,
mutates provider state, checks recovery after SIGKILL, replaces the container
using the same volumes, and checks graceful restart. It compares service URLs
and tokens exactly and verifies data, isolation, export and non-resurrection of
a deleted sandbox. It never restarts an existing deployment. Use `--port` to
choose an unused port and `--keep` to retain its final sandbox for inspection.
Choose a fresh work directory for each run. Credentials and receipts are written
only to that private directory.


## MCP and scoped agents

The same deployment serves Streamable HTTP MCP at `/mcp`. Configure a project
credential for agents; keep the operator credential for administration. See
[agent access](../../docs/mcp.md) for real tool names, policy and client setup.
The reverse proxy must forward MCP POST bodies and normal authorization headers.
No separate MCP process, database, provider implementation or public port is used.

`FAB_MAX_SANDBOXES` sets the global capacity (default 64, maximum 4096). Project
policies impose their own quotas and default/maximum TTL. The operator UI's
default project retains sandboxes until explicit close. Agent project sandboxes
expire under their policy unless renewed. Capacity is a count limit, not a
promise that every dataset size fits the configured memory/disk budget.

Registry version 1 is upgraded transactionally to version 2 for projects, hashed
control credentials and durable creation keys. Back up the complete stopped
store before upgrading; the older binary cannot open a version-2 registry.
Provider databases are not migrated or reseeded by this registry upgrade.


### Repeat the scoped MCP recovery check

Issue a project credential using the operator setup above and supply it to the
verifier process as `FABRICATE_MCP_TOKEN`. Use a fresh private directory outside
this checkout; its receipt contains sandbox service credentials.

```sh
python3 deploy/console/verify-mcp.py \
  --url http://127.0.0.1:4173 \
  --work-dir /tmp/fabricate-mcp-check
```

This creates two independent synthetic services through MCP, retries the same
creation key, mutates and reads one service, checks its untouched comparison,
renews its lifetime, checks unsupported operations and reads logs/state. It
closes its sandbox and checks that the closed creation key cannot resurrect it.
Add `--keep` to retain the resulting sandbox for inspection.

For an isolated local test deployment only, add `--restart-container NAME` to
SIGKILL and restart that container between verification passes. The verifier
checks the container's configured public origin before stopping it. This option
interrupts every sandbox on that deployment, so use a dedicated test deployment.
It verifies project authentication and exact service URLs/tokens, data and
creation-key recovery after restart. It does not test disk-loss recovery.


`FAB_MAX_SERVICES_PER_SANDBOX` / `--max-services-per-sandbox` bounds service
fan-out in each newly created sandbox (default 16, configurable 1–256). It covers
catalog environments and custom compositions. Oversized requests fail before
provider validation or storage publication, without consuming a creation key.
Existing ready worlds and ready-key replays are retained when admission limits
change. The configured bound appears in Deployment and HTTP/MCP discovery.
This complements the sandbox-count quota; size both for host memory/disk and
the selected datasets. Raising limits does not allocate additional capacity.
