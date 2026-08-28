#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_HUBSPOT_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_HUBSPOT_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_HUBSPOT_URL:-}" ]; then
    echo "FAB_HUBSPOT_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_HUBSPOT_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.hubapi.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

contacts=$(read_json "$base/crm/v3/objects/contacts")
echo "$contacts" | jq -e '[.results[].id] | index("101")' >/dev/null
echo "$contacts" | jq -e '[.results[].properties.email] | index("dana@northwind.example")' >/dev/null

deal=$(read_json "$base/crm/v3/objects/deals/301")
echo "$deal" | jq -e '.properties.dealstage == "appointmentscheduled"' >/dev/null
echo "$deal" | jq -e '.properties.invoice == "INV-4812"' >/dev/null

updated=$(read_json -X PATCH "$base/crm/v3/objects/deals/301" -d '{"properties":{"dealstage":"closedwon"}}')
echo "$updated" | jq -e '.properties.dealstage == "closedwon"' >/dev/null

created=$(read_json -X POST "$base/crm/v3/objects/tickets" -d '{"properties":{"subject":"Follow up with Northwind","hs_ticket_priority":"MEDIUM"}}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id"

persisted=$(read_json "$base/crm/v3/objects/deals/301")
echo "$persisted" | jq -e '.properties.dealstage == "closedwon"' >/dev/null

tickets=$(read_json "$base/crm/v3/objects/tickets")
echo "$tickets" | jq -e --arg id "$created_id" '[.results[].id] | index($id)' >/dev/null
echo "$tickets" | jq -e '.results | length >= 2' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 11,
    "messagesBefore": 10,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "HubSpot CRM v3",
        "environment": {
            "label": "Acme HubSpot",
            "manifest": "environments/acme-hubspot.yaml",
            "messages": 10,
        },
        "integration": "hubspot",
        "modes": {},
        "operationLabels": {
            "read": "Read pipeline",
            "write": "Close deal and open ticket",
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
