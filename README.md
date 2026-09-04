<p align="center">
  <a href="https://fabricate.dmach.in/">
    <img src="apps/landing/public/fabricate-wordmark.svg" alt="fabricate" width="420">
  </a>
</p>

<p align="center">
  <b>API sandboxes</b> for apps and agents
</p>

<p align="center">
  <a href="https://fabricate.dmach.in/">
    <img src="https://img.shields.io/badge/services-12-e8ff6a?labelColor=171812&style=flat-square" alt="12 services">
  </a>
</p>

<p align="center">
  <a href="https://fabricate.dmach.in/">Website</a>
  ·
  <a href="https://fabricate.dmach.in/docs">Docs</a>
  ·
  <a href="https://fabricate.dmach.in/docs/getting-started">Getting started</a>
</p>

Fabricate runs disposable, stateful provider APIs on your machine. Point a test,
workflow, or agent at Gmail, HubSpot, Intercom, and other shipped resources,
start from a known scenario, then tear the sandbox down when the command exits.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/DumbMachine/fabricate/main/install.sh | sh
```

The installer puts `fab` on your PATH. See
[Getting started](https://fabricate.dmach.in/docs/getting-started) if it prints
a PATH hint.

## Example

Run the seeded Acme Gmail inbox, list it, trash one message, and list again.
The default list omits spam and trash, so the estimate drops from 27 to 26:

```bash
fab run acme-gmail -- sh -c '
  auth="Authorization: Bearer $FAB_SUPPORT_MAIL_TOKEN"
  mail="$FAB_SUPPORT_MAIL_URL/gmail/v1/users/me"

  echo "== list =="
  curl -sS -H "$auth" "$mail/messages?maxResults=1"
  echo
  echo "== trash msg-0028 =="
  curl -sS -H "$auth" -X POST "$mail/messages/msg-0028/trash"
  echo
  echo "== list again =="
  curl -sS -H "$auth" "$mail/messages?maxResults=1"
  echo
'
```

```text
== list ==
{
  "messages": [
    {"id": "msg-0028", "threadId": "thr-refund"}
  ],
  "nextPageToken": "1",
  "resultSizeEstimate": 27
}

== trash msg-0028 ==
{
  "id": "msg-0028",
  "threadId": "thr-refund",
  "labelIds": ["Label_billing", "TRASH"],
  "snippet": "Received — we will watch for the refund on billing@tinyshop.example and confirm once it posts. Thanks for turning this a…"
}

== list again ==
{
  "messages": [
    {"id": "msg-0027", "threadId": "thr-sso-outage"}
  ],
  "nextPageToken": "1",
  "resultSizeEstimate": 26
}
```

The trash response also includes the full RFC 822 payload; the next run of
`fab run acme-gmail` starts from the same 28-message scenario again.

Use `--proxy` when the client must keep production hostnames. Fabricate injects
proxy and CA variables into that process only, and replaces `Authorization` on
routed hosts:

```bash
fab run acme-gmail --proxy -- npm test
```

Runnable sample apps (Python, Node, Rust) live in [`examples/`](examples/).

Inspect the catalog before you wrap anything:

```bash
fab environment list
fab resource inspect gmail
fab scenario list gmail
```

- [Getting started](https://fabricate.dmach.in/docs/getting-started)
- [CLI](https://fabricate.dmach.in/docs/cli)
- [Gmail](https://fabricate.dmach.in/docs/resources/integrations/gmail)
- [Agent and workflow QA](https://fabricate.dmach.in/docs/guides/agent-workflows)

## Development

```bash
make check
make generate-check
make docs-operations
make conformance
```

`make install-dev` installs `fab-dev`, a checkout-linked command for
contributors. Public docs and the installer use `fab`.

## License

MIT
