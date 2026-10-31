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
