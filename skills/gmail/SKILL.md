---
name: fabricate-gmail
description: Run the compiled Gmail API v1 Acme Corp scenario through Fabricate's transparent HTTPS proxy and inspect calls.
allowed-tools: Bash(fab:*), Bash(fab-dev:*)
---

# Gmail with Fabricate

Wrap the application without changing its Google API endpoint:

```bash
fab-dev run \
  --environment /Users/dumbmachine/github.com/dumbmachine/fabricate/environments/acme-gmail.yaml \
  --proxy \
  -- <application command>
```

The scenario is `gmail.acme-corp.v1`: twelve handcrafted Acme Corp emails in a
stateful SQLite mailbox. Fabricate handles normal Gmail hosts and the Google
OAuth token endpoint locally. Reads, sends, label changes, and subsequent reads
share live state for the run.

The wrapper prints a request-log path. Inspect it later with:

```bash
fab-dev logs acme-gmail --cat
```

Authorization, cookies, OAuth tokens, and secret-shaped payload fields are
redacted. Do not use the removed `fab create gmail -p support-inbox` workflow.
