#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_OUTREACH_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_OUTREACH_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_OUTREACH_URL:-}" ]; then
    echo "FAB_OUTREACH_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_OUTREACH_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.outreach.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/vnd.api+json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

sequences=$(read_json "$base/api/v2/sequences")
echo "$sequences" | jq -e '.data | length == 2' >/dev/null
echo "$sequences" | jq -e '[.data[].attributes.name] | index("Northwind expansion")' >/dev/null
echo "$sequences" | jq -e '[.data[].attributes.name] | index("Helix Bio trial")' >/dev/null

prospects=$(read_json "$base/api/v2/prospects")
echo "$prospects" | jq -e '[.data[].attributes.emails[]] | index("dana@northwind.example")' >/dev/null
echo "$prospects" | jq -e '[.data[].attributes.emails[]] | index("jules@northwind.example")' >/dev/null
echo "$prospects" | jq -e '[.data[].attributes.emails[]] | index("noah@helixbio.example")' >/dev/null

tasks_before=$(read_json "$base/api/v2/tasks")
echo "$tasks_before" | jq -e '.data | length == 1' >/dev/null
echo "$tasks_before" | jq -e '.data[0].attributes.action == "call"' >/dev/null
echo "$tasks_before" | jq -e '.data[0].attributes.completed == false' >/dev/null
echo "$tasks_before" | jq -e '.data[0].attributes.note | test("INV-4812")' >/dev/null

completed=$(read_json -X PATCH "$base/api/v2/tasks/1" -d '{"data":{"type":"task","id":"1","attributes":{"completed":true}}}')
echo "$completed" | jq -e '.data.attributes.completed == true' >/dev/null
echo "$completed" | jq -e '.data.attributes.state == "complete"' >/dev/null

task=$(read_json "$base/api/v2/tasks/1")
echo "$task" | jq -e '.data.attributes.completed == true' >/dev/null
echo "$task" | jq -e '.data.attributes.state == "complete"' >/dev/null
echo "$task" | jq -e '.data.attributes.note | test("INV-4812")' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 1,
    "messagesBefore": 1,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Outreach API v2",
        "environment": {
            "label": "Acme Outreach",
            "manifest": "environments/acme-outreach.yaml",
            "messages": 1,
        },
        "integration": "outreach",
        "modes": {},
        "operationLabels": {
            "read": "List sequences, prospects, and tasks",
            "write": "Complete call task",
            "persistence": "Confirm task completed",
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
