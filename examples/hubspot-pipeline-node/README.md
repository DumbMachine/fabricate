# HubSpot pipeline (Node)

A CRM client. It lists Acme's seeded deals: name, amount, stage, and invoice
id when present. Zero npm dependencies — Node 22+'s `http`/`https` is enough.

## Run it

```bash
fab run acme-hubspot -- node pipeline.mjs
```

From the repository root:

```bash
./scripts/fab-dev run acme-hubspot -- node examples/hubspot-pipeline-node/pipeline.mjs
```

You should see Northwind's stalled `INV-4812` expansion, TinyShop annual Pro,
Helix Bio's trial, Fernworks, and Brightpath.

## Keep HubSpot's real hostname

```bash
fab run acme-hubspot --proxy -- node pipeline.mjs
```

Calls go to `https://api.hubapi.com/crm/v3/objects/deals`. The app uses
`HTTPS_PROXY` and trusts `SSL_CERT_FILE` through Node's default TLS store
(`NODE_EXTRA_CA_CERTS` is injected by Fabricate).
