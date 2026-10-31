# Initial console verification

Verified locally on 2026-10-31. No public deployment was performed.

- `make check` passed across the Go repository. Focused `go test ./console ./cli`
  passed after adding provider-response isolation assertions.
- Console typecheck, three request-wrapper tests, and production build passed.
  Two consecutive native UI builds produced identical embedded asset contents.
- The multi-stage Docker build ran UI typecheck/tests/build before compiling Go.
  Compose configuration and the optional Caddy configuration validated.
- The built image was run through Compose on a loopback port with operator
  authentication, UID 10001, a read-only root filesystem, dropped capabilities,
  a mounted secret, and a persistent request-log volume. Its health check passed.
- Browser checks exercised invalid and valid sign-in, reload persistence,
  keyboard tab navigation, resource drawer dismissal, dataset inspection,
  sandbox creation, Services / Connect / Activity, protected credential display,
  API base URL copying, and copying a bare OpenAPI document into another input.
  Close cancellation preserved the sandbox; confirmed close produced independent
  session and service 404 responses. Sign-out returned to the sign-in screen.
- The final container was checked in the browser, including its connection page,
  resource catalog, and deployment connection check. A 390 x 844 viewport had no
  document-wide horizontal overflow; mobile navigation and deployment layout
  were inspected. The viewport override was reset. No browser warnings or errors
  were reported for this final container session.
- A two-service composition was created through the control API. A provider
  mutation was read back from one service while its comparison stayed unchanged.
  Reusing a service token against the other service returned 401. Control cookies
  did not reach the provider trace; provider responses had isolation headers and
  trace credentials were redacted.

Operator and service credentials, API receipts, and screenshots are kept outside
this repository. This verifies a local operator deployment, not production TLS,
public DNS, multi-tenant SaaS operation, durable sandbox state, or load capacity.


## Mounted storage follow-up

The mounted-storage implementation supersedes the initial ephemeral-state limit
above. Verification on 2026-10-31 included:

- Full `make check`, the real CLI `make e2e` suite, console typecheck/request tests
  and production UI build, and a multi-stage Docker build.
- Three provider services (two Asana instances and Gmail) recovered on the same
  mounted volume with unchanged sandbox identity, externally advertised URLs,
  service tokens, creation time and persisted provider mutations.
- The isolated Docker verifier passed before shutdown, after SIGKILL (exit 137)
  followed by start, after container removal/replacement with the same volumes,
  and after a graceful restart. It independently read the mutated data through
  the original service URLs and tokens at every stage.
- A comparison service remained unchanged, cross-service token reuse returned
  401, exported state retained the mutation, and a deleted sandbox and its
  provider route remained 404 after each restart. The trace continued.
- Unit tests cover exclusive ownership, missing databases/registry, changed
  origin/contracts, interrupted creation/deletion, rejected unsupported resource
  capabilities and failed registry writes. A concurrency regression checks that
  shutdown retains the storage lock while creation is still in flight.
- Focused `go test -race ./console -run '^TestMounted'` passed. The HTTP state
  and environment packages also passed race checks. The broader console race
  run was stopped because loading the complete provider catalog made it slow;
  the mounted tests now use a focused Asana/Gmail registry.
- A second Linux container attempting the live volume failed its ownership lock;
  the original non-root, read-only container remained healthy.
- Browser verification showed the recovered deployment's persistent-storage
  status. Private credentials, receipts and screenshots were excluded from Git.

The repeatable black-box verifier is `deploy/console/verify-durability.py`.
These checks establish restart recovery with retained healthy storage. They do
not establish recovery from disk loss, multi-writer failover, backup RPO, public
TLS/DNS operation or automatic schema migration.


## Scoped MCP and lifecycle follow-up

Verification on 2026-10-31 for the MCP/lifecycle changes included:

- `make check` passed after the final Go changes; `make e2e` passed the CLI
  integration suite. Console typecheck, request-wrapper tests and production
  build passed, as did the multi-stage Docker build.
- Official Go MCP SDK client tests exercise real Streamable HTTP initialization,
  schemas, tools, current/legacy protocol negotiation, notifications, expected
  unsupported transport methods, unauthorized requests and rejected origins.
- Positive creation/mutation/readback works with independent service instances.
  Project tests cover cross-project reads/state/logs/deletes, credential expiry
  and revocation, operator-only administration, quotas and policy updates.
  Unsupported provider operations, external URLs and invalid custom state fail.
- Lifecycle tests cover concurrent same-key starts, mismatched requests,
  interrupted creation/recovery, retry at capacity, closed-key tombstones,
  durable renewal, expiry routing/cleanup and retained credential digests.
  Focused race checks passed for MCP, mounted recovery, lifecycle, compact
  contracts and concurrent renewal/read access. Compact projected OpenAPI was
  parsed and validated with the real OpenAPI validator.
- A fresh Codex CLI process, in an empty directory without checkout context, was
  registered against the actual local MCP deployment with a project credential.
  It completed 23 MCP calls using only Fabricate: discovery, typed contracts,
  two-service creation, same-key replay/status, provider mutation and independent
  readback, unchanged comparison, unsupported method, logs/state and renewal.
  The transcript contains no shell/file tools or other MCP servers.
- The first Codex run's `approval_policy="never"` blocked the conservatively
  destructive provider tool before dispatch. A dedicated read-only `service_read`
  tool was added. The authorized mutation then passed using per-action automatic
  approval review, without disabling the filesystem sandbox or changing mutation
  annotations. Two invalid JSON-string request bodies failed before the agent
  supplied the correct structured object; only one successful write was made.
- The existing version-1 mounted deployment was backed up while stopped, then
  upgraded to registry version 2. Existing service URLs/tokens and complete
  exported state were compared before/after replacement and remained identical.
- `deploy/console/verify-mcp.py` passed against the final image, before and after
  SIGKILL (exit 137) and container restart. The same project credential, service
  URLs/tokens, mutation, untouched comparison, creation key and request status
  survived. Repeated close succeeded; retrying the closed key returned 410 and
  sandbox lookup returned 404. Its temporary sandbox was removed.
- After that final forced restart, independent HTTP reads also confirmed that
  the Codex-created sandbox retained its mutation with its comparison unchanged,
  and that the older pre-MCP sandbox retained its original persisted mutation.
- Browser verification covered the Deployment / API access page: the actual MCP
  URL, copy action, Codex registration command and project credential guidance.
  Operator browser login survived the container replacement. Private tokens,
  transcripts, receipts, backups and screenshots are excluded from this repo.

These checks establish local deployment and agent self-service behavior. Public
TLS/DNS, OAuth login, hosted tenant accounts, capacity/load limits and disk-loss
restore remain outside this verification. Access connection handoff is unchanged.
The hosted reusable dataset registry is deferred to GitHub issue #20.


## External customer failure-path follow-up

Additional verification on 2026-10-31 covered lifecycle failures discovered
while consuming Fabricate from Access as an independent vendor:

- Expired unpublished creation records release their database files and quota
  without a deployment restart. Replaying an expired unpublished key returns
  410. Nonexpired requests in another project remain intact.
- Cancellation by creation key is durable even before a delayed request reaches
  admission. It closes live and unpublished sandboxes, is project-scoped and
  idempotent, and cancelled keys remain closed after restart. HTTP discovery and
  the actual SDK MCP tools expose the same cancellation handler.
- A fault injected into registry deletion caused close to fail visibly while
  revoking provider routing and removing the retiring sandbox from ready lists.
  The cleanup sweep completed deletion after the fault was removed. This was
  exercised with separate Asana and Gmail instances.
- Unknown control API routes return 404 instead of the console HTML. Snapshot
  creation now occurs under the same lock as expiry renewal.
- Full `make check`, real CLI `make e2e`, and focused race checks for expiry,
  cancellation, failed deletion, renewal and shutdown passed. The hardened Docker
  image built successfully, including console checks.

Access's customer verifier uses only public HTTP APIs and scoped credentials;
its build/runtime does not import or start Fabricate. Its private receipts and
provider snapshots remain outside this repository. The hosted dataset registry
remains deferred under issue #20.


The final hardened image also passed the live MCP verifier after SIGKILL/restart,
including cancellation by the original creation key and a 410 on attempted
resurrection. Access's independent HTTP customer loop passed two fresh large
publisher worlds (138 ad sets, 1,411 daily rows, 3,605 affiliate actions per audit),
exact state equality through reads/renewal, negative auth/scope/contract checks,
and repeated verified teardown. A real committed creation response was dropped;
Access recovered the identical sandbox and installed its workspace once. Existing
Access connections completed another audit after the final Fabricate restart,
and the real Access UI completed a further read-only audit through both resource
instances. All detailed receipts and captures remain in private customer storage.
