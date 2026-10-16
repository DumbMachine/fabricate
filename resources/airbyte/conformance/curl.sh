#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_AIRBYTE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_AIRBYTE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_AIRBYTE_URL:-}" ]; then
    echo "FAB_AIRBYTE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_AIRBYTE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.airbyte.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

sources=$(read_json "$base/v1/sources")
echo "$sources" | jq -e '[.data[].sourceId] | sort == ["source_razorpay","source_shiprocket","source_shopify"]' >/dev/null

connections=$(read_json "$base/v1/connections")
echo "$connections" | jq -e '[.data[].connectionId] | sort == ["conn_razorpay","conn_shopify_goods"]' >/dev/null

jobs=$(read_json "$base/v1/jobs")
echo "$jobs" | jq -e '.data | length == 2' >/dev/null
echo "$jobs" | jq -e '[.data[] | select(.jobId=="job_10482" and .status=="succeeded" and .connectionId=="conn_shopify_goods")] | length == 1' >/dev/null
echo "$jobs" | jq -e '[.data[] | select(.jobId=="job_fail_ndr" and .status=="failed" and .sourceId=="source_shiprocket" and (.failureReason | test("shipment 10483")))] | length == 1' >/dev/null

created=$(read_json -X POST "$base/v1/jobs" -d '{"connectionId":"conn_shopify_goods","jobType":"sync"}')
echo "$created" | jq -e '.status == "running" and .jobType == "sync" and .connectionId == "conn_shopify_goods"' >/dev/null
job_id=$(echo "$created" | jq -r '.jobId')
test -n "$job_id"
test "$job_id" != "job_10482"
test "$job_id" != "job_fail_ndr"

got=$(read_json "$base/v1/jobs/$job_id")
echo "$got" | jq -e --arg id "$job_id" '.jobId == $id and .status == "running" and .connectionId == "conn_shopify_goods"' >/dev/null

after=$(read_json "$base/v1/jobs")
echo "$after" | jq -e --arg id "$job_id" '[.data[].jobId] | index($id)' >/dev/null
echo "$after" | jq -e '.data | length == 3' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 3,
    "messagesBefore": 2,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Airbyte API v1",
        "environment": {
            "label": "Acme Airbyte",
            "manifest": "environments/acme-airbyte.yaml",
            "messages": 2,
        },
        "integration": "airbyte",
        "modes": {},
        "operationLabels": {
            "read": "List jobs",
            "write": "Trigger a sync",
            "persistence": "Confirm the new job",
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
