#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_ZOHOBOOKS_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_ZOHOBOOKS_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_ZOHOBOOKS_URL:-}" ]; then
    echo "FAB_ZOHOBOOKS_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_ZOHOBOOKS_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://www.zohoapis.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")
org="organization_id=org-acme"

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

orgs=$(read_json "$base/books/v3/organizations")
echo "$orgs" | jq -e '.organizations[0].organization_id == "org-acme"' >/dev/null

invoices=$(read_json "$base/books/v3/invoices?$org")
echo "$invoices" | jq -e '[.invoices[].invoice_number] | index("INV-4812")' >/dev/null
echo "$invoices" | jq -e '[.invoices[].invoice_number] | index("INV-10482")' >/dev/null
echo "$invoices" | jq -e '[.invoices[] | select(.invoice_number == "INV-4812") | .status] == ["sent"]' >/dev/null
echo "$invoices" | jq -e '[.invoices[] | select(.invoice_number == "INV-10484") | .status] == ["void"]' >/dev/null
echo "$invoices" | jq -e '[.invoices[] | select(.invoice_number == "INV-1188") | .balance] == [1188]' >/dev/null

duplicate=$(read_json "$base/books/v3/invoices/INV-4812?$org")
echo "$duplicate" | jq -e '.invoice.total == 1240 and .invoice.currency_code == "USD"' >/dev/null
echo "$duplicate" | jq -e '.invoice.notes | test("rfnd_Acme10484") | not' >/dev/null

payments=$(read_json "$base/books/v3/customerpayments?$org&reference_number=INV-4812")
echo "$payments" | jq -e '[.customer_payments[].payment_id] | sort == ["pay_saas_4812", "pay_saas_4812b"]' >/dev/null

note=$(read_json "$base/books/v3/creditnotes/CN-10484?$org")
echo "$note" | jq -e '.creditnote.reference_number == "rfnd_Acme10484"' >/dev/null
echo "$note" | jq -e '.creditnote.invoice_id == "INV-10484"' >/dev/null

created=$(read_json -X POST "$base/books/v3/invoices?$org" -d '{"customer_id":"contact-acme","invoice_number":"INV-CONF","currency_code":"INR","date":"2026-08-26","line_items":[{"name":"Conformance check","rate":10,"quantity":1}]}')
echo "$created" | jq -e '.invoice.invoice_id == "INV-CONF" and .invoice.total == 10' >/dev/null

again=$(read_json "$base/books/v3/invoices/INV-CONF?$org")
echo "$again" | jq -e '.invoice.invoice_number == "INV-CONF"' >/dev/null

paid=$(read_json -X POST "$base/books/v3/customerpayments?$org" -d '{"customer_id":"contact-acme","payment_mode":"cash","amount":10,"date":"2026-08-26","reference_number":"pay_conf","description":"Conformance payment","invoices":[{"invoice_id":"INV-CONF","amount_applied":10}]}')
payment_id=$(echo "$paid" | jq -r '.payment.payment_id')
test -n "$payment_id" && test "$payment_id" != "null"

fetched=$(read_json "$base/books/v3/customerpayments/${payment_id}?$org")
echo "$fetched" | jq -e '.payment.amount == 10 and .payment.invoices[0].invoice_id == "INV-CONF"' >/dev/null

settled=$(read_json "$base/books/v3/invoices/INV-CONF?$org")
echo "$settled" | jq -e '.invoice.status == "paid" and .invoice.balance == 0' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 8,
    "messagesBefore": 7,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Zoho Books API v3",
        "environment": {
            "label": "Acme Zoho Books",
            "manifest": "environments/acme-zohobooks.yaml",
            "messages": 7,
        },
        "integration": "zohobooks",
        "modes": {},
        "operationLabels": {
            "read": "Read the Acme ledger",
            "write": "Create an invoice and customer payment",
            "persistence": "Confirm persistence",
        },
        "verification": {
            "kind": "curl",
            "label": "HTTP client",
            "client": os.environ.get("FAB_COMPATIBILITY_CLIENT", "curl"),
            "title": "HTTP API verification",
        },
    }
    try:
        with open(path, encoding="utf-8") as fh:
            report = json.load(fh)
    except FileNotFoundError:
        pass
    report["modes"][mode] = mode_report
    report["testedAt"] = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"
    report["testedCommit"] = os.environ.get("FAB_COMPATIBILITY_COMMIT", "unknown")
    client = os.environ.get("FAB_COMPATIBILITY_CLIENT")
    if client:
        report.setdefault("verification", {})["client"] = client
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(report, fh, indent=2)
        fh.write("\n")
print(json.dumps({"mode": mode, **mode_report}))
PY
