# Support desk case file (Python)

This is the multi-service example. Acme Support Desk seeds the same incident
in four providers. The app builds one case file for `INV-4812`:

- Gmail search for the invoice
- Intercom conversation `101`
- Asana task `task-double-charge`
- HubSpot deal `301`

That join is the point of Fabricate environments: one company, not four
unrelated mocks.

No packages to install.

## Run it

```bash
fab run acme-support-desk -- python3 casefile.py
```

From the repository root:

```bash
./scripts/fab-dev run acme-support-desk -- python3 examples/support-desk-python/casefile.py
```

You should see Dana Whitfield in mail and chat, an open refund task, and a
stalled Northwind deal still on `appointmentscheduled`.

## Keep production hostnames

```bash
fab run acme-support-desk --proxy -- python3 casefile.py
```

One wrapper starts all four services and routes `gmail.googleapis.com`,
`api.intercom.io`, `app.asana.com`, and `api.hubapi.com` locally.
