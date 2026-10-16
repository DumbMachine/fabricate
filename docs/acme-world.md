# Acme world

Shared scenario facts for Fabricate resources. Every populated scenario in
this set is one company, **Acme**, seen from a different API. Use the
identifiers below exactly so an environment can join Shopify, Shiprocket,
Razorpay, Zoho, Slack, and the rest without translation.

The frozen instant for tests is **2026-08-26T12:00:00Z**. Handlers take time
from `httpresource.ServerDependencies.Clock`, never `time.Now()`.

## How the businesses fit together

Acme (`acme.example`) is a Bengaluru company with two lines:

1. **Acme App**, a checkout SaaS product already seeded in Gmail, HubSpot,
   Intercom, and Asana. Money is USD. The open incident is duplicate invoice
   **INV-4812** ($1,240) for Northwind Traders.
2. **Acme Goods**, Acme's own India D2C shop, plus an agency practice that
   operates three client shops. Shop money is INR (amounts below are rupees;
   Razorpay amounts are paise, which is rupees times 100).

Do not merge those monies. `INV-4812` is the SaaS duplicate. `rfnd_Acme10484`
is the shop refund. Both are visible to finance, and they are different.

## People

| Key | Name | Email | Phone | Where they show up |
| --- | --- | --- | --- | --- |
| val | Val Ortega | val@acme.example | +91-80-4123-0101 | Acme operator, Okta admin, Slack owner, Shopify staff |
| aisha | Aisha Rahman | aisha@acme.example | +91-80-4123-0102 | Finance. Zoho Books owner. Approves POs |
| ravi | Ravi Mehta | ravi@acme.example | +91-80-4123-0103 | Logistics. Shiprocket and warehouse BLR-1 |
| nina | Nina Kapoor | nina@acme.example | +91-80-4123-0104 | HR. BambooHR |
| leo | Leo Park | leo@acme.example | +91-80-4123-0105 | Recruiting. Greenhouse |
| sam | Sam Adeyemi | sam@acme.example | +91-80-4123-0106 | Support. Jira Service Management and Slack |
| iris | Iris Berg | iris@acme.example | +91-80-4123-0107 | Legal. DocuSign and Ironclad |
| jordan | Jordan Hale | jordan.hale@acme.example | +91-80-4123-0190 | Accepted offer, not started. Staged in Okta |
| dana | Dana Whitfield | dana@northwind.example | +91-99000-48120 | Northwind buyer. SaaS billing contact and shop customer on the refunded order |
| jules | Jules Okonkwo | jules@northwind.example | +91-99000-48121 | Northwind ops. Client shop contact |
| marco | Marco Silva | marco@tinyshop.example | +91-98111-22008 | TinyShop owner. Shop customer on the NDR order. SaaS cancellation requester |
| priya | Priya Nair | priya@fernworks.example | +91-98450-11223 | Fernworks. Shop customer on the in-transit order. SaaS expansion |
| mei | Mei Chen | mei.chen@contoso.example | +1-206-555-0148 | Contoso IT. SAML login loop, org slug `contoso-eu` |
| leila | Leila Haddad | leila@fabrikam.example | +1-425-555-0172 | Fabrikam compliance. SCIM export request `req_7Q4K2` |
| sofia | Sofia Marin | sofia@brightpath.example | +1-415-555-0194 | Brightpath. API key rotation, closed |
| owen | Owen Brooks | owen@adatum.example | +1-312-555-0160 | Adatum. Domain TXT `acme-domain-verification=adatum-7f31` still pending |
| noah | Noah Patel | noah@helixbio.example | +1-617-555-0133 | Helix Bio sales trial |
| anita | Anita Desai | anita.desai@consumer.example | +91-98200-10491 | Consumer on the TinyShop client order |

Company domains and the existing HubSpot company ids stay as they are:
Acme `201` `acme.example`, Northwind Traders `202` `northwind.example`,
Contoso `203` `contoso.example`, Fernworks `204` `fernworks.example`,
Fabrikam `205` `fabrikam.example`, Brightpath `206` `brightpath.example`,
Adatum `207` `adatum.example`, Helix Bio `208` `helixbio.example`,
TinyShop `209` `tinyshop.example`.

## Shops

| Domain | Name | Who operates it |
| --- | --- | --- |
| acme-goods.myshopify.com | Acme Goods | Acme, in-house |
| northwind-market.myshopify.com | Northwind Market | Acme agency for Northwind |
| tinyshop.myshopify.com | TinyShop | Acme agency for TinyShop |
| fernworks-studio.myshopify.com | Fernworks Studio | Acme agency for Fernworks |

Warehouse **BLR-1** is Acme's Bengaluru location at 18 Industrial Layout,
Bengaluru, KA 560058, IN. Pickup for Acme Goods and Northwind Market ships
from BLR-1.

## Catalog

Acme Goods:

| SKU | Title | INR | Paise | On hand at BLR-1 |
| --- | --- | --- | --- | --- |
| AG-MUG-01 | Acme Ceramic Mug | 799 | 79900 | 120 |
| AG-TEE-02 | Acme Field Tee | 1499 | 149900 | 48 |
| AG-NOTE-03 | Acme Field Notebook | 499 | 49900 | 200 |

Client shops:

| SKU | Shop | Title | INR | Paise |
| --- | --- | --- | --- | --- |
| NW-KETTLE-01 | Northwind Market | Northwind Travel Kettle | 2499 | 249900 |
| NW-BLANKET-02 | Northwind Market | Northwind Wool Throw | 3999 | 399900 |
| TS-CANDLE-01 | TinyShop | TinyShop Fig Candle | 899 | 89900 |
| FW-PRINT-01 | Fernworks Studio | Fernworks Linen Print | 1899 | 189900 |

## Orders and the records that must match

### 10482 — in transit

Acme Goods order **10482**, placed 2026-08-25T09:14:00Z. Customer Priya Nair,
`priya@fernworks.example`, `+91-98450-11223`. Ship to 42 Residency Road,
Bengaluru, KA 560025, IN. Lines: 1 × AG-TEE-02 and 2 × AG-MUG-01. Merchandise
total **₹3097** (309700 paise).

- Razorpay order `order_Acme10482`, payment `pay_Acme10482`, captured, UPI, 309700 paise, INR.
- Shiprocket AWB `SR10482AWB`, courier Delhivery, status `in_transit`, channel Acme Goods.
- Zoho Books invoice `INV-10482`, INR, paid.
- Zoho Inventory sales order `SO-10482`.
- Gupshup message `msg_10482` delivered to `+919845011223`: order is in transit, AWB `SR10482AWB`.
- GA4 purchase, transaction id `10482`, source `whatsapp`, medium `gupshup`, campaign `monsoon-tee`.
- impact.com action for order `10482`, partner `partner_northwind`, payout ₹150 pending.
- Slack `#fulfillment` message from Ravi cites `#10482` and `SR10482AWB`.
- Drive file `razorpay-settlement-2026-08-25.csv` includes `pay_Acme10482`.

### 10483 — NDR

Acme Goods order **10483**, placed 2026-08-24T16:40:00Z. Customer Marco Silva,
`marco@tinyshop.example`, `+91-98111-22008`. Ship to 7 Linking Road, Mumbai,
MH 400050, IN. Line: 1 × AG-NOTE-03. Total **₹499** (49900 paise).

- Razorpay payment `pay_Acme10483`, captured, card, 49900 paise. No refund.
- Shiprocket AWB `SR10483AWB`, courier Bluedart, status `ndr`, reason `Customer not available`.
- Zoho Books invoice `INV-10483`, INR, paid. No credit note.
- Gupshup `msg_10483` to `+919811122008` asking for a delivery window. Status failed.
- Jira Service Management request `ITSM-10483`, summary `NDR on Acme Goods #10483`.
- GA4 purchase `10483`, source `google`, medium `cpc`.
- Airbyte job `job_fail_ndr` failed while syncing Shiprocket; the error names shipment `10483`.
- Slack `#fulfillment` message from Sam cites `#10483`.

### 10484 — shop refund

Acme Goods order **10484**, placed 2026-08-22T11:05:00Z. Customer Dana
Whitfield, `dana@northwind.example`, `+91-99000-48120`. Ship to 18 Harbor
Road, Pune, MH 411001, IN. Line: 1 × AG-TEE-02. Total **₹1499** (149900 paise).

- Razorpay payment `pay_Acme10484` captured, then full refund `rfnd_Acme10484`.
- Shipment cancelled. Shiprocket return `RET-10484`.
- Zoho Books invoice `INV-10484` void, credit note `CN-10484`.
- Gupshup `msg_10484` tells Dana the refund `rfnd_Acme10484` was issued.
- Slack `#finance` message from Aisha distinguishes `rfnd_Acme10484` from SaaS invoice `INV-4812`.

### 10490 — agency, Northwind Market

Northwind Market order **10490**, placed 2026-08-23T08:20:00Z. Customer Jules
Okonkwo. Line: 1 × NW-KETTLE-01. Total **₹2499** (249900 paise). Payment
`pay_NW10490` captured. AWB `SR10490AWB`, Delhivery, status `delivered`.
Zoho invoice `INV-10490`. This order exists only in the agency Shopify
scenario and in the logistics, payments, and books scenarios.

### 10491 — agency, TinyShop

TinyShop order **10491**, placed 2026-08-21T13:00:00Z. Customer Anita Desai.
Line: 1 × TS-CANDLE-01. Total **₹899** (89900 paise). Payment `pay_TS10491`
captured. No shipment exception. Include it in the agency Shopify scenario.

### SaaS money, unchanged from the existing Acme resources

- `INV-4812`, Northwind Traders, **1240 USD**, status sent. Two payments,
  `pay_saas_4812` and `pay_saas_4812b`, are the duplicate charge. HubSpot deal
  `301`. Asana task `task-double-charge`. Chargebee subscription
  `sub_northwind_checkout`, customer `cus_northwind`.
- `INV-2207`, Fernworks, **8900 USD**, paid. Chargebee `sub_fernworks_platform`,
  customer `cus_fernworks`.
- `INV-1188`, TinyShop annual Pro, **1188 USD**. Marco asked to cancel under
  the 30-day guarantee. Chargebee `sub_tinyshop_pro`, customer `cus_tinyshop`,
  status `non_renewing`. Not refunded yet.
- Contoso SAML loop, org slug `contoso-eu`, 380 users. JSM request `ITSM-SSO`.
- Fernworks CSV export bug. Canny post and the existing Asana task
  `task-csv-export`.
- Fabrikam audit gap `req_7Q4K2`.
- Adatum TXT `adatum-7f31`.
- Helix Bio trial for Noah Patel.
- Brightpath key rotation is closed.

## Files that more than one resource names

Google Drive folder `Finance/August 2026`:

- `INV-4812-northwind.pdf`
- `razorpay-settlement-2026-08-25.csv` (contains `pay_Acme10482`, `pay_Acme10483`, `pay_Acme10484`, `rfnd_Acme10484`)
- `shiprocket-ndr-10483.pdf`

Folder `Legal`:

- `Fernworks-MSA.pdf` — DocuSign envelope `env-fernworks-msa`, Ironclad workflow `wf-fernworks-msa`
- `TinyShop-cancellation.pdf` — envelope `env-tinyshop-refund`, workflow `wf-tinyshop-cancel`

Folder `HR`:

- `offer-letter-jordan-hale.pdf` — envelope `env-offer-jordan`, Greenhouse offer `offer-jordan`

Folder `Compliance`:

- `access-review-2026-08.pdf` — Vanta evidence for the Shopify admin access test
- `409a-2026.pdf` — Carta valuation

## Per-resource scenario contract

Resource ids, scenario ids, hosts, and path prefixes are fixed. `minimal.v1`
is an empty but valid state for the same collections. The populated scenario
is the one listed. Shopify also has the agency scenario.

| Resource id | Populated scenario id | Provider host | HostPrefixes | Version |
| --- | --- | --- | --- | --- |
| shopify | shopify.acme-goods.v1 and shopify.acme-agency.v1 | the four `*.myshopify.com` hosts above | none | 2024-10 |
| shiprocket | shiprocket.acme-shipments.v1 | apiv2.shiprocket.in | none | v1 |
| razorpay | razorpay.acme-payments.v1 | api.razorpay.com | none | v1 |
| zohobooks | zohobooks.acme-ledger.v1 | www.zohoapis.com | `/books/` | v3 |
| googledrive | googledrive.acme-drive.v1 | www.googleapis.com | `/drive/` | v3 |
| slack | slack.acme-workspace.v1 | slack.com | `/api/` | v2 |
| okta | okta.acme-org.v1 | acme.okta.com | none | 2024-07 |
| docusign | docusign.acme-envelopes.v1 | demo.docusign.net | `/restapi/` | v2.1 |
| bamboohr | bamboohr.acme-people.v1 | acme.bamboohr.com | none | v1 |
| greenhouse | greenhouse.acme-pipeline.v1 | harvest.greenhouse.io | none | v1 |
| ga4 | ga4.acme-properties.v1 | analyticsdata.googleapis.com | none | v1beta |
| gupshup | gupshup.acme-whatsapp.v1 | api.gupshup.io | none | v1 |
| airbyte | airbyte.acme-syncs.v1 | api.airbyte.com | none | v1 |
| redshift | redshift.acme-warehouse.v1 | redshift-data.us-east-1.amazonaws.com | none | 2012-12-01 |
| gainsight | gainsight.acme-success.v1 | acme.gainsightcloud.com | none | v1 |
| jsm | jsm.acme-desk.v1 | acme.atlassian.net | `/rest/servicedeskapi/` | 3 |
| vanta | vanta.acme-compliance.v1 | api.vanta.com | none | v1 |
| ironclad | ironclad.acme-contracts.v1 | na1.ironcladapp.com | none | v1 |
| outreach | outreach.acme-sequences.v1 | api.outreach.io | none | v2 |
| chargebee | chargebee.acme-billing.v1 | acme.chargebee.com | none | v2 |
| canny | canny.acme-feedback.v1 | canny.io | `/api/v1/` | v1 |
| statuspage | statuspage.acme-status.v1 | api.statuspage.io | none | v1 |
| zohoinventory | zohoinventory.acme-stock.v1 | www.zohoapis.com | `/inventory/` | v1 |
| impact | impact.acme-partners.v1 | api.impact.com | none | v1 |
| carta | carta.acme-captable.v1 | api.carta.com | none | v1 |

Also add `<id>.minimal.v1` for every resource.

### What each populated scenario must contain

- **shopify** `acme-goods`: one shop, three products, orders 10482, 10483, 10484 with the customers, SKUs, and totals above. `acme-agency`: all four shops, the client SKUs, and orders 10482–10484 plus 10490 and 10491. Dispatch on the `Host` header when it is a known shop domain; otherwise serve `currentShop` (Acme Goods) so a direct URL still works. Accept `Authorization: Bearer` and `X-Shopify-Access-Token`.
- **shiprocket**: shipments for 10482, 10483, 10490 and return `RET-10484`. Create-shipment and a tracking read must round-trip. NDR list includes 10483.
- **razorpay**: payments `pay_Acme10482`, `pay_Acme10483`, `pay_Acme10484`, refund `rfnd_Acme10484`, plus `pay_NW10490` and `pay_TS10491`. Notes carry `shop` and `order_id`. A refund create against a captured payment persists.
- **zohobooks**: contacts for the people and companies above. Invoices `INV-10482`, `INV-10483`, `INV-10484`, `INV-10490`, `INV-4812`, `INV-2207`, `INV-1188`. Credit note `CN-10484`. Customer payments include the duplicate pair on `INV-4812`.
- **googledrive**: the folders and files named above. Metadata is enough; store a short text body. Copy, list, get, and create must work. Paths on the wire are `/drive/v3/...`. Copy shapes from `specs/google-drive/openapi.json` rather than inventing field names. Set `HostPrefixes` so Gmail keeps `/gmail/` on `www.googleapis.com`.
- **slack**: users for the Acme employees. Channels `fulfillment`, `finance`, `support`, `incidents`, `hiring`. Messages must quote the ids in the orders section (`#10482`, `SR10482AWB`, `#10483`, `rfnd_Acme10484`, `INV-4812`, `contoso-eu`). `chat.postMessage` persists and `conversations.history` returns it. Web API methods are `/api/<method>` on `slack.com`. Start from `specs/slack/openapi.json`.
- **okta**: active users for the Acme employees, Jordan `STAGED`, and `old.contractor@acme.example` `DEPROVISIONED`. Groups Finance (Aisha, Val) and Support (Sam, Val). Application "Acme Checkout" whose description says the SAML ACS for `contoso-eu` is misconfigured. Application "Shopify Admin" assigned to Val and Ravi. Start from `specs/okta/openapi.yaml` and curate a small surface (list/get/create users, list groups, list apps).
- **docusign**: account `acct-acme`. Envelopes `env-fernworks-msa` (completed, Priya and Iris), `env-tinyshop-refund` (sent, waiting on Marco), `env-offer-jordan` (delivered, Jordan). Creating an envelope persists.
- **bamboohr**: employees for the Acme staff plus Jordan with status Onboarding and start date 2026-09-08. Departments Finance, People, Support, Legal, Logistics, Engineering. Ravi has time off 2026-08-28 through 2026-08-29, "warehouse visit".
- **greenhouse**: job `job-support` "Support Engineer". Candidate Jordan Hale, application `app-jordan`, stage Offer, offer `offer-jordan`. Candidate Sasha Iqbal `sasha.iqbal@example.com`, application `app-sasha`, stage On-site. A scorecard or note write persists.
- **ga4**: properties `Acme Goods` `309712345` and `Acme App` `309700001`. A run-report style read returns purchase rows for transaction ids 10482, 10483, and 10484 (10484 is a refund event).
- **gupshup**: WhatsApp app "Acme Goods". Messages `msg_10482`, `msg_10483`, `msg_10484` as specified, plus an inbound from Priya `msg_10482_in` ("thanks, where is the tee?") referencing 10482. Sending a message persists.
- **airbyte**: sources `source_shopify`, `source_razorpay`, `source_shiprocket`; destination `dest_redshift`; connections `conn_shopify_goods` and `conn_razorpay`. Job `job_10482` succeeded. Job `job_fail_ndr` failed and names shipment 10483. Triggering a sync creates a job.
- **redshift**: cluster `acme-warehouse`, database `analytics`, schema `commerce`. Data API `ExecuteStatement`, `DescribeStatement`, `GetStatementResult`. Seed tables `orders`, `payments`, `shipments` with rows for 10482, 10483, 10484, and a `saas_invoices` row for `INV-4812`. `SELECT` returns those rows. The real API is AWS JSON; accept `Authorization: Bearer` because the proxy injects a bearer token. Document that in `UPSTREAM.md`.
- **gainsight**: companies Northwind (red, CTA on `INV-4812`), TinyShop (yellow, CTA on the 30-day refund, `INV-1188`), Fernworks (green, `INV-2207`), Contoso (red, CTA `contoso-eu` SAML). A timeline or task write persists.
- **jsm**: service desk project `SUP`. Requests `ITSM-4812` (duplicate `INV-4812`, reporter Dana), `ITSM-10483` (NDR, reporter Sam), `ITSM-SSO` (Contoso, reporter Mei), `ITSM-1188` (TinyShop cancellation, reporter Marco). A comment persists.
- **vanta**: vendor Shiprocket in review, vendor Razorpay approved. Failing test "Access review for Shopify admin" citing Drive file `access-review-2026-08.pdf` and the deprovisioned contractor. A document upload or test note persists.
- **ironclad**: workflows `wf-fernworks-msa` completed, `wf-tinyshop-cancel` in review, `wf-delhivery-msa` waiting on Iris. Records name the DocuSign envelope ids where those exist.
- **outreach**: sequence "Northwind expansion" with Dana and Jules, and a call task whose note cites `INV-4812`. Sequence "Helix Bio trial" with Noah. Completing a task persists.
- **chargebee**: customers and subscriptions named in the SaaS money section. Invoices reference `INV-4812`, `INV-2207`, and `INV-1188`. Do not put INR shop orders here.
- **canny**: board "Acme App". Post from Priya "CSV export downloads an empty file". Post from Dana "Refund duplicate charge" citing `INV-4812`, status in progress, vote from Jules. Creating a post persists.
- **statuspage**: page `status.acme.example`, page id `page-acme`. Component Checkout degraded. Incident "Checkout double-charge on INV-4812" investigating. Component Order tracking operational, with a resolved incident "NDR notifications delayed" citing 10483.
- **zohoinventory**: items for the Acme Goods SKUs, warehouse `BLR-1`, sales orders `SO-10482` and `SO-10483`, purchase order `PO-7721` to vendor "Clay & Co" for AG-MUG-01, status pending approval by Aisha.
- **impact**: program "Acme Goods affiliates". Partner `partner_northwind` "Northwind Creators". Action for order `10482`, payout 15000 paise, pending. Listing actions by order id returns it.
- **carta**: issuer Acme. Stakeholders Val Ortega (option grant) and Harbor Capital (preferred). Valuation document name `409a-2026.pdf`. Carta's production API is approval-gated; curate the published stakeholder and certificate reads from public docs and say so in `UPSTREAM.md`.

## Implementation rules

Follow `resources/gmail` for a curated contract: a small `openapi.yaml` whose
paths are the full public paths a client requests, strict generation, SQLite
load/dump, and tests. `resources/asana` shows subsetting a larger vendor spec
with `include-operation-ids` when you store an official document unmodified.

- Register nothing. Do not edit `resources/all`, `catalog.ts`, `meta.ts`, CI,
  `httpresource`, `environment`, or any other resource.
- Do not commit, push, or change git state.
- Write only `resources/<id>/**`, `environments/acme-<id>.yaml` (one service,
  populated scenario), `packages/docs-content/resources/integrations/<id>.mdx`,
  and one `packages/docs-content/resources/_examples/<id>-read.json` spec.
  Shopify replaces the existing planned `shopify.mdx`.
- Copy the Asana integration page order and the Gmail example spec. Commands
  must be real one-paste `curl` examples against the seeded ids. No placeholder
  tokens in the page.
- `conformance/curl.sh` takes `direct` or `proxy`, checks a read, a write, and
  a following read, and writes the compatibility report shape used by
  `resources/asana/conformance/curl.sh`.
- Tests cover schema validation, load/dump canonical round trip, unknown
  scenario fields rejected, unauthorized requests, and read/write/read.
- Auth is the synthetic bearer token from secret key `token`. The proxy
  overwrites `Authorization` with `Bearer <token>`, so bearer must work even
  when the real vendor uses basic auth or a custom header.
- `additionalProperties: false` on the scenario state. Dump must reproduce the
  loaded document, including key order independence via `scenario.CanonicalJSON`.
- Keep the operation count small, about 8 to 16, covering list, get, and at
  least one create or update for the category's main records.
- `go generate ./resources/<id>/` and `go test ./resources/<id>/` must pass.
  Run `gofmt -w` on that directory only. Do not run repo-wide `make`,
  `go test ./...`, `make conformance`, or `make docs-examples`.
- `UPSTREAM.md` records the official doc URL, retrieval date, and that the
  stored contract is the curated surface. Do not import an unverified
  third-party OpenAPI dump.
- Never log or seed live secrets. Ids in this document are synthetic.

Single-service environment:

```yaml
apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata:
  name: acme-<id>
services:
  <id>:
    resource: <id>
    scenario: <id>.<populated>.v1
```

Shopify's single-service environment uses `shopify.acme-goods.v1`. The agency
scenario is selected by composed environments, not by that file.

## Composed environments

These manifests start several resources from the scenarios above. One Shopify
service covers every shop domain. Zoho Books (`/books/`) and Zoho Inventory
(`/inventory/`) share `www.zohoapis.com`. Gmail (`/gmail/`) and Google Drive
(`/drive/`) share `www.googleapis.com`.

- `acme-goods-shop`: in-house Acme Goods. Shopify `shopify.acme-goods.v1` plus
  Razorpay, Shiprocket, Zoho Books, Zoho Inventory, Gupshup, Slack, Drive, GA4,
  Redshift, and Airbyte. Order `10482` is the join key.
- `acme-commerce-agency`: the same operator running four shops. Shopify
  `shopify.acme-agency.v1` plus Razorpay, Shiprocket, Zoho Books, Gupshup,
  Slack, and impact.com. Host selects the shop: `10482` on Acme Goods, `10490`
  on Northwind Market, `10491` on TinyShop.
- `acme-company-ops`: Acme App people and the SaaS incidents. Gmail, Intercom,
  Asana, HubSpot, Okta, BambooHR, Chargebee, Zoho Books, Gainsight, Jira
  Service Management, Slack, and Drive. `INV-4812` is the join key, and it
  stays distinct from shop refund `10484`.
