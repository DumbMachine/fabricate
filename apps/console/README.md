# Fabricate console

Local web UI served by `fab serve`. The TanStack Start app is built as a
static client and copied into `console/ui`, which the Go binary embeds.

```bash
pnpm --filter @fabricate/console build
```

During UI development, run `fab serve` and `pnpm --filter @fabricate/console dev`.
The dev server proxies `/api` to `127.0.0.1:4173`.
