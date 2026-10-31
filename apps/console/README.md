# Fabricate console

Local web UI served by `fab serve`. The TanStack Start app is built as a
static client and copied into `console/ui`, which the Go binary embeds.

```bash
pnpm --filter @fabricate/console build
```

During UI development, run `fab serve` and `pnpm --filter @fabricate/console dev`.
The dev server proxies `/api` to `127.0.0.1:4173`.

## Interaction model

The console follows Access/Krucible's compact workspace chrome: a quiet sidebar,
48px identity header, keyboard-navigable tabs, restrained buttons, flat tables,
and focused connection/inspection views. Resource drawers and close confirmations
use Base UI's modal focus management. Service credentials are hidden by default;
copying agent context includes them only when explicitly selected.

The default `fab serve` development mode can remain open on loopback. When an
operator token is configured, the same UI signs in through `/api/auth/session`.
Never put an operator token in a Vite variable; use the backend secret/file.

```sh
pnpm --filter @fabricate/console test
pnpm --filter @fabricate/console typecheck
pnpm --filter @fabricate/console build
```

For UI development the default backend is `http://127.0.0.1:4173`. Override it
with `FAB_CONSOLE_DEV_API`; API, health, discovery and service paths are proxied.
For authenticated dev, advertise the Vite origin with the backend's `--public-url`.
Build output is synchronized into `console/ui` for Go embedding. See
[`deploy/console`](../../deploy/console/README.md) for the container/HTTPS recipe.
