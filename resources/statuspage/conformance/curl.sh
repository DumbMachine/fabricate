#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_STATUSPAGE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_STATUSPAGE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_STATUSPAGE_URL:-}" ]; then
    echo "FAB_STATUSPAGE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_STATUSPAGE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.statuspage.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

components=$(read_json "$base/v1/pages/page-acme/components")
echo "$components" | jq -e 'length == 2' >/dev/null
echo "$components" | jq -e '[.[] | select(.name == "Checkout") | .status] == ["degraded_performance"]' >/dev/null
echo "$components" | jq -e '[.[] | select(.name == "Order tracking") | .status] == ["operational"]' >/dev/null

incidents_before=$(read_json "$base/v1/pages/page-acme/incidents")
echo "$incidents_before" | jq -e 'length == 2' >/dev/null
echo "$incidents_before" | jq -e '[.[] | select(.name == "Checkout double-charge on INV-4812") | .status] == ["investigating"]' >/dev/null
echo "$incidents_before" | jq -e '[.[] | select(.name == "NDR notifications delayed") | .status] == ["resolved"]' >/dev/null

resolved=$(read_json "$base/v1/pages/page-acme/incidents/inc-ndr-10483")
echo "$resolved" | jq -e '.status == "resolved"' >/dev/null
echo "$resolved" | jq -e '[.incident_updates[].body] | any(test("10483"))' >/dev/null

created=$(read_json -X POST "$base/v1/pages/page-acme/incidents" -d '{"incident":{"name":"Checkout webhook timeouts","status":"identified","body":"Checkout webhooks are timing out.","component_ids":["comp-checkout"]}}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id"
echo "$created" | jq -e '.status == "identified"' >/dev/null
echo "$created" | jq -e '.incident_updates[0].body | test("timing out")' >/dev/null

incidents_after=$(read_json "$base/v1/pages/page-acme/incidents")
echo "$incidents_after" | jq -e --arg id "$created_id" '[.[].id] | index($id)' >/dev/null
echo "$incidents_after" | jq -e 'length == 3' >/dev/null

confirmed=$(read_json "$base/v1/pages/page-acme/incidents/$created_id")
echo "$confirmed" | jq -e --arg id "$created_id" '.id == $id and .name == "Checkout webhook timeouts"' >/dev/null

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
        "api": "Statuspage API v1",
        "environment": {
            "label": "Acme Statuspage",
            "manifest": "environments/acme-statuspage.yaml",
            "messages": 2,
        },
        "integration": "statuspage",
        "modes": {},
        "operationLabels": {
            "read": "List components and incidents",
            "write": "Create incident",
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
