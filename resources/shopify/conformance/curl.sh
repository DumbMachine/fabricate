#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_SHOPIFY_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_SHOPIFY_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_SHOPIFY_URL:-}" ]; then
    echo "FAB_SHOPIFY_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_SHOPIFY_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme-goods.myshopify.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

order=$(read_json "$base/admin/api/2024-10/orders/10482.json")
echo "$order" | jq -e '.order.email == "priya@fernworks.example"' >/dev/null
echo "$order" | jq -e '.order.total_price == "3097.00"' >/dev/null
echo "$order" | jq -e '[.order.line_items[] | select(.sku == "AG-TEE-02" and .quantity == 1)] | length == 1' >/dev/null
echo "$order" | jq -e '[.order.line_items[] | select(.sku == "AG-MUG-01" and .quantity == 2)] | length == 1' >/dev/null

before=$(read_json "$base/admin/api/2024-10/orders.json?status=any")
echo "$before" | jq -e '.orders | length == 3' >/dev/null

seeded=$(read_json "$base/admin/api/2024-10/orders/10484/refunds.json")
echo "$seeded" | jq -e '.refunds[0].transactions[0].authorization == "rfnd_Acme10484"' >/dev/null

created=$(read_json -X POST "$base/admin/api/2024-10/orders.json" -d '{"order":{"email":"priya@fernworks.example","line_items":[{"variant_id":2001,"quantity":1}]}}')
echo "$created" | jq -e '.order.total_price == "799.00"' >/dev/null
echo "$created" | jq -e '.order.line_items[0].sku == "AG-MUG-01"' >/dev/null
order_id=$(echo "$created" | jq -r '.order.id')
line_id=$(echo "$created" | jq -r '.order.line_items[0].id')
test -n "$order_id"
test -n "$line_id"

refund=$(read_json -X POST "$base/admin/api/2024-10/orders/${order_id}/refunds.json" -d "{\"refund\":{\"refund_line_items\":[{\"line_item_id\":${line_id},\"quantity\":1,\"restock_type\":\"no_restock\"}]}}")
echo "$refund" | jq -e '.refund.transactions[0].kind == "refund"' >/dev/null
echo "$refund" | jq -e '.refund.transactions[0].amount == "799.00"' >/dev/null

persisted=$(read_json "$base/admin/api/2024-10/orders/${order_id}.json")
echo "$persisted" | jq -e '.order.financial_status == "refunded"' >/dev/null
echo "$persisted" | jq -e '.order.current_total_price == "0.00"' >/dev/null
echo "$persisted" | jq -e '.order.email == "priya@fernworks.example"' >/dev/null

refunds=$(read_json "$base/admin/api/2024-10/orders/${order_id}/refunds.json")
echo "$refunds" | jq -e '.refunds | length == 1' >/dev/null
echo "$refunds" | jq -e '.refunds[0].refund_line_items[0].subtotal == "799.00"' >/dev/null

after=$(read_json "$base/admin/api/2024-10/orders.json?status=any")
echo "$after" | jq -e '.orders | length == 4' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 4,
    "messagesBefore": 3,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Shopify Admin REST 2024-10",
        "environment": {
            "label": "Acme Goods Shopify",
            "manifest": "environments/acme-shopify.yaml",
            "messages": 3,
        },
        "integration": "shopify",
        "modes": {},
        "operationLabels": {
            "read": "Read order 10482",
            "write": "Create order and refund",
            "persistence": "Confirm order and refund",
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
