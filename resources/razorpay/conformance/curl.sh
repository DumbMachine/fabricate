#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_RAZORPAY_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_RAZORPAY_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_RAZORPAY_URL:-}" ]; then
    echo "FAB_RAZORPAY_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_RAZORPAY_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.razorpay.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

payments=$(read_json "$base/v1/payments")
echo "$payments" | jq -e '.count == 5' >/dev/null
echo "$payments" | jq -e '[.items[].id] | index("pay_Acme10482")' >/dev/null
echo "$payments" | jq -e '[.items[] | select(.id == "pay_Acme10482") | .amount] == [309700]' >/dev/null
echo "$payments" | jq -e '[.items[] | select(.id == "pay_Acme10482") | .method] == ["upi"]' >/dev/null
echo "$payments" | jq -e '[.items[] | select(.id == "pay_Acme10482") | .notes.shop] == ["acme-goods"]' >/dev/null
echo "$payments" | jq -e '[.items[] | select(.id == "pay_Acme10482") | .notes.order_id] == ["10482"]' >/dev/null

refunded=$(read_json "$base/v1/payments/pay_Acme10484")
echo "$refunded" | jq -e '.status == "refunded" and .amount_refunded == 149900 and .refund_status == "full"' >/dev/null

existing=$(read_json "$base/v1/refunds/rfnd_Acme10484")
echo "$existing" | jq -e '.id == "rfnd_Acme10484" and .payment_id == "pay_Acme10484" and .amount == 149900' >/dev/null

created=$(read_json -X POST "$base/v1/payments/pay_Acme10483/refund" -d '{"amount":49900}')
echo "$created" | jq -e '.entity == "refund" and .payment_id == "pay_Acme10483" and .amount == 49900 and .status == "processed"' >/dev/null
refund_id=$(echo "$created" | jq -r '.id')
test -n "$refund_id" && test "$refund_id" != "null"

persisted=$(read_json "$base/v1/refunds/$refund_id")
echo "$persisted" | jq -e --arg id "$refund_id" '.id == $id and .amount == 49900' >/dev/null

payment=$(read_json "$base/v1/payments/pay_Acme10483")
echo "$payment" | jq -e '.status == "refunded" and .refund_status == "full" and .amount_refunded == 49900' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 7,
    "messagesBefore": 6,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Razorpay API v1",
        "environment": {
            "label": "Acme Razorpay",
            "manifest": "environments/acme-razorpay.yaml",
            "messages": 6,
        },
        "integration": "razorpay",
        "modes": {},
        "operationLabels": {
            "read": "Read captured payments",
            "write": "Refund a captured payment",
            "persistence": "Confirm the refund",
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
