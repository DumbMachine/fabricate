#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_GA4_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_GA4_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_GA4_URL:-}" ]; then
    echo "FAB_GA4_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_GA4_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://analyticsdata.googleapis.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

report=$(read_json -X POST "$base/v1beta/properties/309712345:runReport" -d '{"dateRanges":[{"startDate":"2026-08-01","endDate":"2026-08-26"}],"dimensions":[{"name":"transactionId"},{"name":"sessionSource"},{"name":"sessionMedium"},{"name":"sessionCampaignName"},{"name":"eventName"}],"metrics":[{"name":"eventCount"},{"name":"purchaseRevenue"},{"name":"itemRefundAmount"}]}')
echo "$report" | jq -e '
  .kind == "analyticsData#runReport" and .rowCount == 3 and .metadata.currencyCode == "INR"
  and ([.rows[] | select(.dimensionValues[0].value == "10482")] | .[0].dimensionValues[1].value) == "whatsapp"
  and ([.rows[] | select(.dimensionValues[0].value == "10482")] | .[0].dimensionValues[2].value) == "gupshup"
  and ([.rows[] | select(.dimensionValues[0].value == "10482")] | .[0].dimensionValues[3].value) == "monsoon-tee"
  and ([.rows[] | select(.dimensionValues[0].value == "10483")] | .[0].dimensionValues[1].value) == "google"
  and ([.rows[] | select(.dimensionValues[0].value == "10483")] | .[0].dimensionValues[2].value) == "cpc"
  and ([.rows[] | select(.dimensionValues[0].value == "10484")] | .[0].dimensionValues[4].value) == "refund"
' >/dev/null

created=$(read_json -X POST "$base/v1beta/properties/309712345/audienceExports" -d '{"audience":"properties/309712345/audiences/purchasers","dimensions":[{"dimensionName":"transactionId"}]}')
echo "$created" | jq -e '.done == true and (.response.name | startswith("properties/309712345/audienceExports/")) and .response.state == "ACTIVE"' >/dev/null
name=$(echo "$created" | jq -r '.response.name')

got=$(read_json "$base/v1beta/$name")
echo "$got" | jq -e '.audience == "properties/309712345/audiences/purchasers" and .state == "ACTIVE"' >/dev/null

listed=$(read_json "$base/v1beta/properties/309712345/audienceExports")
echo "$listed" | jq -e --arg name "$name" '([.audienceExports[].name] | index($name)) != null' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 1,
    "messagesBefore": 0,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Google Analytics Data API v1beta",
        "environment": {
            "label": "Acme Google Analytics 4",
            "manifest": "environments/acme-ga4.yaml",
            "messages": 3,
        },
        "integration": "ga4",
        "modes": {},
        "operationLabels": {
            "read": "Run Acme Goods report",
            "write": "Create audience export",
            "persistence": "Confirm audience export",
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
