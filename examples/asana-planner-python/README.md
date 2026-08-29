# Asana planner (Python)

A work-board client, the closest thing here to a calendar app while Fabricate
ships Asana and not Google Calendar. It prints the seeded **Checkout
reliability** project: open tasks, done tasks, owner, and notes.

No packages to install.

## Run it

```bash
fab run acme-asana -- python3 planner.py
```

From the repository root:

```bash
./scripts/fab-dev run acme-asana -- python3 examples/asana-planner-python/planner.py
```

You should see the INV-4812 refund, the Contoso SSO loop, the blocked Fernworks
CSV export, and a completed webhook key rotation.

## Keep Asana's real hostname

```bash
fab run acme-asana --proxy -- python3 planner.py
```

Calls go to `https://app.asana.com/api/1.0/...`. Fabricate intercepts
`app.asana.com` for this process.

The same app also runs under `acme-support-desk` (the board service is named
`board`, so it reads `FAB_BOARD_URL`).
