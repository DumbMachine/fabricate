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
paths, including `/api/`, `/s/`, and SPA deep links, to the console. If you already
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

Operator token rotation takes effect on process restart and invalidates existing
browser sessions. Save or close active sandboxes before a restart.

Sandbox state is ephemeral. Closing a sandbox or restarting the process discards
its data; the request-log volume is retained. This is an operator deployment,
not a multi-tenant SaaS account system. Durable sessions, tenant isolation,
quotas/TTL and dataset publishing remain separate work.

Stopping the Compose project retains named volumes. Export results before
restarting a deployment that has active sandboxes. Configure log retention for
your environment. The UI's Activity view explicitly identifies a truncated recent
trace; it is not an exhaustive history endpoint.
