#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_CHARGEBEE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_CHARGEBEE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_CHARGEBEE_URL:-}" ]; then
    echo "FAB_CHARGEBEE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_CHARGEBEE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme.chargebee.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" "$@"
}

subs=$(read_json "$base/api/v2/subscriptions")
echo "$subs" | jq -e '[.list[].subscription.id] | contains(["sub_northwind_checkout", "sub_fernworks_platform", "sub_tinyshop_pro"])' >/dev/null
echo "$subs" | jq -e '[.list[] | select(.subscription.id == "sub_tinyshop_pro") | .subscription.status] == ["non_renewing"]' >/dev/null

invoices=$(read_json "$base/api/v2/invoices")
echo "$invoices" | jq -e '[.list[].invoice.id] | contains(["INV-4812", "INV-2207", "INV-1188"])' >/dev/null
echo "$invoices" | jq -e '.list[] | select(.invoice.id == "INV-4812") | .invoice.total == 124000 and .invoice.currency_code == "USD" and ([.invoice.linked_payments[].txn_id] | contains(["pay_saas_4812", "pay_saas_4812b"]))' >/dev/null
echo "$invoices" | jq -e '.list[] | select(.invoice.id == "INV-2207") | .invoice.status == "paid" and .invoice.total == 890000' >/dev/null
echo "$invoices" | jq -e '.list[] | select(.invoice.id == "INV-1188") | .invoice.status == "paid" and .invoice.total == 118800' >/dev/null
echo "$invoices" | jq -e 'tostring | contains("10482") | not' >/dev/null

cancelled=$(read_json "$base/api/v2/subscriptions/sub_northwind_checkout/cancel_for_items" \
  --data-urlencode cancel_option=end_of_term \
  --data-urlencode 'cancel_reason_code=duplicate charge')
echo "$cancelled" | jq -e '.subscription.status == "non_renewing"' >/dev/null
echo "$cancelled" | jq -e '.subscription.cancel_reason_code == "duplicate charge"' >/dev/null

comment=$(read_json "$base/api/v2/comments" \
  --data-urlencode entity_type=subscription \
  --data-urlencode entity_id=sub_northwind_checkout \
  --data-urlencode 'notes=Duplicate charges pay_saas_4812 and pay_saas_4812b on INV-4812.')
echo "$comment" | jq -e '.comment.notes | test("pay_saas_4812b")' >/dev/null

again=$(read_json "$base/api/v2/subscriptions/sub_northwind_checkout")
echo "$again" | jq -e '.subscription.status == "non_renewing"' >/dev/null

comments=$(read_json -G "$base/api/v2/comments" \
  --data-urlencode entity_type=subscription \
  --data-urlencode entity_id=sub_northwind_checkout)
echo "$comments" | jq -e '[.list[].comment.notes] | any(test("pay_saas_4812b"))' >/dev/null
echo "$comments" | jq -e '.list | length == 1' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 2,
    "messagesBefore": 1,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Chargebee API v2",
        "environment": {
            "label": "Acme Chargebee",
            "manifest": "environments/acme-chargebee.yaml",
            "messages": 3,
        },
        "integration": "chargebee",
        "modes": {},
        "operationLabels": {
            "read": "List subscriptions and invoices",
            "write": "Cancel subscription and comment",
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
