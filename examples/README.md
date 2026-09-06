# Example apps

Small apps that talk to Gmail, Asana, and HubSpot the way a real mail client,
planner, or CRM integration would. Fabricate provides the APIs and the seeded
Acme data. The apps are ordinary Python, Node, and Rust programs.

## What you need

- The `fab` CLI ([install](https://fabricate.dmach.in/docs/getting-started)), or
  `fab-dev` from this checkout (`make install-dev`, or run `./scripts/fab-dev`)
- Python 3.12+ for the Python apps (stdlib only)
- Node 22+ for the Node apps
- Rust 1.83+ for the Rust app

## Pick an app

| App | What it does | Environment |
| --- | --- | --- |
| [gmail-inbox-python](gmail-inbox-python) | List the support inbox | `acme-gmail` |
| [gmail-inbox-node](gmail-inbox-node) | Same, using Google's official `googleapis` SDK | `acme-gmail` |
| [gmail-inbox-rust](gmail-inbox-rust) | Same, using `reqwest` | `acme-gmail` |
| [asana-planner-python](asana-planner-python) | Print the Checkout reliability board | `acme-asana` |
| [hubspot-pipeline-node](hubspot-pipeline-node) | Print open deals in the Acme CRM | `acme-hubspot` |
| [support-desk-python](support-desk-python) | One case file for `INV-4812` across mail, chat, board, and CRM | `acme-support-desk` |

Each folder has its own README with the exact command.

## Direct URL or `--proxy`?

You do not configure the app. Wrap it:

```bash
fab run acme-gmail -- python3 examples/gmail-inbox-python/inbox.py
fab run acme-gmail --proxy -- python3 examples/gmail-inbox-python/inbox.py
```

- **Direct** (default): Fabricate injects `FAB_*_URL` and `FAB_*_TOKEN`. The app
  calls that local URL and sends the token.
- **`--proxy`**: the app keeps the provider's normal hostname
  (`gmail.googleapis.com`, `app.asana.com`, `api.hubapi.com`, …). Fabricate
  intercepts those hosts for this process only.

If `HTTPS_PROXY` is set, the apps use production hostnames. Otherwise they use
the injected `FAB_*_URL`.

## From this checkout

```bash
./scripts/fab-dev run acme-gmail -- python3 examples/gmail-inbox-python/inbox.py
```

`fab-dev` rebuilds the CLI from the current source. Public docs and released
installers use `fab`.
