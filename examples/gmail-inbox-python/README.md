# Gmail inbox (Python)

A tiny mail client. It prints the seeded Acme support inbox: address, size,
then the latest messages with subject, sender, and snippet.

No packages to install. Python 3.12+ is enough.

## Run it

From this folder, with Fabricate on your PATH:

```bash
fab run acme-gmail -- python3 inbox.py
```

From the repository root with the contributor CLI:

```bash
./scripts/fab-dev run acme-gmail -- python3 examples/gmail-inbox-python/inbox.py
```

You should see `support@acme.example`, 28 messages, and threads about
`INV-4812`, Contoso SSO, refunds, and CSV export. Gmail hides spam/trash on a
default list, so the visible estimate is 27.

Search like a mail app:

```bash
fab run acme-gmail -- python3 inbox.py INV-4812
```

## Keep Gmail's real hostname

If your client already talks to `gmail.googleapis.com`, add `--proxy`. This app
detects `HTTPS_PROXY` and switches by itself:

```bash
fab run acme-gmail --proxy -- python3 inbox.py
```

## What Fabricate injects

| Mode | Base URL | Auth |
| --- | --- | --- |
| Direct | `$FAB_SUPPORT_MAIL_URL/gmail/v1` | `Authorization: Bearer $FAB_SUPPORT_MAIL_TOKEN` |
| `--proxy` | `https://gmail.googleapis.com/gmail/v1` | omitted; the proxy supplies the token |
