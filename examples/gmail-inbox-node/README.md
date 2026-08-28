# Gmail inbox (Node)

The same mail client as the Python example, using Google's official
[`googleapis`](https://www.npmjs.com/package/googleapis) SDK. In `--proxy`
mode the SDK keeps `gmail.googleapis.com` and refreshes against
`oauth2.googleapis.com` — Fabricate answers both locally.

## Install

```bash
npm install
```

## Run it

```bash
fab run acme-gmail -- node inbox.mjs
```

From the repository root:

```bash
npm --prefix examples/gmail-inbox-node install
./scripts/fab-dev run acme-gmail -- node examples/gmail-inbox-node/inbox.mjs
```

Search:

```bash
fab run acme-gmail -- node inbox.mjs INV-4812
```

## Keep Gmail's real hostname

```bash
fab run acme-gmail --proxy -- node inbox.mjs
```

The process must honor `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS`. `googleapis`
does. Do not send `$FAB_SUPPORT_MAIL_TOKEN` on the Gmail hostname; the proxy
replaces `Authorization`.
