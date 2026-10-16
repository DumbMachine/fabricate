#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_ZOHOINVENTORY_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_ZOHOINVENTORY_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_ZOHOINVENTORY_URL:-}" ]; then
    echo "FAB_ZOHOINVENTORY_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_ZOHOINVENTORY_URL%/}
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

items=$(read_json "$base/inventory/v1/items?$org")
echo "$items" | jq -e '.items | length == 3' >/dev/null
echo "$items" | jq -e '[.items[] | {sku, stock_on_hand}] | sort_by(.sku) == [{"sku":"AG-MUG-01","stock_on_hand":120},{"sku":"AG-NOTE-03","stock_on_hand":200},{"sku":"AG-TEE-02","stock_on_hand":48}]' >/dev/null

orders=$(read_json "$base/inventory/v1/salesorders?$org")
echo "$orders" | jq -e '[.salesorders[].salesorder_number] | sort == ["SO-10482","SO-10483"]' >/dev/null
echo "$orders" | jq -e '[.salesorders[] | select(.salesorder_number == "SO-10482") | .reference_number] == ["10482"]' >/dev/null
echo "$orders" | jq -e '[.salesorders[] | select(.salesorder_number == "SO-10483") | .reference_number] == ["10483"]' >/dev/null

order=$(read_json "$base/inventory/v1/salesorders/so-10482?$org")
echo "$order" | jq -e '.salesorder.total == 3097' >/dev/null
echo "$order" | jq -e '.salesorder.shipping_address.address == "42 Residency Road"' >/dev/null
echo "$order" | jq -e '[.salesorder.contact_persons_associated[].contact_person_email] | index("priya@fernworks.example") != null' >/dev/null

warehouse=$(read_json "$base/inventory/v1/settings/warehouses?$org")
echo "$warehouse" | jq -e '.warehouses[0].warehouse_name == "BLR-1" and .warehouses[0].address == "18 Industrial Layout" and .warehouses[0].zip == "560058"' >/dev/null

before=$(read_json "$base/inventory/v1/purchaseorders?$org")
echo "$before" | jq -e '.purchaseorders | length == 1' >/dev/null
echo "$before" | jq -e '.purchaseorders[0].purchaseorder_number == "PO-7721" and .purchaseorders[0].status == "pending_approval" and .purchaseorders[0].vendor_name == "Clay & Co"' >/dev/null
echo "$before" | jq -e '[.purchaseorders[0].custom_fields[] | select(.label == "Approver email") | .value] == ["aisha@acme.example"]' >/dev/null

approved=$(read_json -X POST "$base/inventory/v1/purchaseorders/po-7721/approve?$org")
echo "$approved" | jq -e '.code == 0 and .message == "success"' >/dev/null

after=$(read_json "$base/inventory/v1/purchaseorders/po-7721?$org")
echo "$after" | jq -e '.purchase_order.status == "approved"' >/dev/null
echo "$after" | jq -e '[.purchase_order.line_items[].sku] == ["AG-MUG-01"]' >/dev/null
echo "$after" | jq -e '[.purchase_order.custom_fields[] | select(.label == "Approver email") | .value] == ["aisha@acme.example"]' >/dev/null
echo "$after" | jq -e '[.purchase_order.comments[].operation_type] == ["approved"]' >/dev/null
echo "$after" | jq -e '[.purchase_order.comments[].commented_by] == ["Aisha Rahman"]' >/dev/null

still=$(read_json "$base/inventory/v1/items?$org")
echo "$still" | jq -e '[.items[] | select(.sku == "AG-MUG-01") | .stock_on_hand] == [120]' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 3,
    "messagesBefore": 3,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Zoho Inventory API v1",
        "environment": {
            "label": "Acme Zoho Inventory",
            "manifest": "environments/acme-zohoinventory.yaml",
            "messages": 3,
        },
        "integration": "zohoinventory",
        "modes": {},
        "operationLabels": {
            "read": "List items, sales orders, and purchase orders",
            "write": "Approve purchase order",
            "persistence": "Confirm approval persisted",
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
