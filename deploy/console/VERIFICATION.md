# Console verification

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
