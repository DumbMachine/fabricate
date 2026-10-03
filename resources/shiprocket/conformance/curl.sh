#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_SHIPROCKET_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_SHIPROCKET_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_SHIPROCKET_URL:-}" ]; then
    echo "FAB_SHIPROCKET_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_SHIPROCKET_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://apiv2.shiprocket.in"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

tracked=$(read_json "$base/v1/external/courier/track/awb/SR10482AWB")
echo "$tracked" | jq -e '.tracking_data.shipment_track[0].awb_code == "SR10482AWB"' >/dev/null
echo "$tracked" | jq -e '.tracking_data.shipment_track[0].current_status == "in_transit"' >/dev/null
echo "$tracked" | jq -e '.tracking_data.shipment_track[0].courier_name == "Delhivery"' >/dev/null
echo "$tracked" | jq -e '.tracking_data.shipment_track[0].channel_name == "Acme Goods"' >/dev/null

orders_before=$(read_json "$base/v1/external/orders")
echo "$orders_before" | jq -e '.meta.pagination.total == 3' >/dev/null

ndr=$(read_json "$base/v1/external/ndr/all")
echo "$ndr" | jq -e '([.data[].channel_order_id] | index("10483")) != null' >/dev/null
echo "$ndr" | jq -e '([.data[].reason] | index("Customer not available")) != null' >/dev/null
echo "$ndr" | jq -e '([.data[].awb_code] | index("SR10483AWB")) != null' >/dev/null

returns=$(read_json "$base/v1/external/orders/processing/return")
echo "$returns" | jq -e '([.data[].id] | index("RET-10484")) != null' >/dev/null

couriers=$(read_json "$base/v1/external/courier/courierListWithCounts")
echo "$couriers" | jq -e '([.courier_data[].name] | index("Delhivery")) != null' >/dev/null
echo "$couriers" | jq -e '([.courier_data[].name] | index("Bluedart")) != null' >/dev/null

create_body='{
  "order_id": "conformance-10500",
  "order_date": "2026-08-26 12:00",
  "pickup_location": "BLR-1",
  "billing_customer_name": "Priya",
  "billing_last_name": "Nair",
  "billing_address": "42 Residency Road",
  "billing_city": "Bengaluru",
  "billing_pincode": "560025",
  "billing_state": "KA",
  "billing_country": "IN",
  "billing_email": "priya@fernworks.example",
  "billing_phone": "+91-98450-11223",
  "shipping_is_billing": true,
  "order_items": [{"name": "Acme Field Tee", "sku": "AG-TEE-02", "units": 1, "selling_price": 1499}],
  "payment_method": "Prepaid",
  "sub_total": 1499,
  "length": 10,
  "breadth": 10,
  "height": 5,
  "weight": 1
}'

created=$(read_json -X POST "$base/v1/external/orders/create/adhoc" -d "$create_body")
echo "$created" | jq -e '.channel_order_id == "conformance-10500"' >/dev/null
echo "$created" | jq -e '.status == "NEW"' >/dev/null
shipment_id=$(echo "$created" | jq -r '.shipment_id')
test -n "$shipment_id"

tracked_new=$(read_json "$base/v1/external/courier/track/shipment/$shipment_id")
echo "$tracked_new" | jq -e --arg id "$shipment_id" '.tracking_data.shipment_track[0].shipment_id == $id' >/dev/null
echo "$tracked_new" | jq -e '.tracking_data.shipment_track[0].channel_order_id == "conformance-10500"' >/dev/null

orders_after=$(read_json "$base/v1/external/orders?search=conformance-10500")
echo "$orders_after" | jq -e '.meta.pagination.total == 1' >/dev/null
echo "$orders_after" | jq -e '.data[0].channel_order_id == "conformance-10500"' >/dev/null

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
        "api": "Shiprocket v1",
        "environment": {
            "label": "Acme Shiprocket",
            "manifest": "environments/acme-shiprocket.yaml",
            "messages": 3,
        },
        "integration": "shiprocket",
        "modes": {},
        "operationLabels": {
            "read": "Track seeded shipment",
            "write": "Create shipment",
            "persistence": "Confirm tracking",
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
