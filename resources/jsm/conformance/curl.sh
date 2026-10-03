#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_JSM_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_JSM_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_JSM_URL:-}" ]; then
    echo "FAB_JSM_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_JSM_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme.atlassian.net"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

desk=$(read_json "$base/rest/servicedeskapi/servicedesk/SUP")
echo "$desk" | jq -e '.id == "1" and .projectKey == "SUP"' >/dev/null

requests=$(read_json "$base/rest/servicedeskapi/request")
echo "$requests" | jq -e '.size == 4' >/dev/null
echo "$requests" | jq -e '[.values[].issueKey] | index("ITSM-4812") != null' >/dev/null
echo "$requests" | jq -e '[.values[].issueKey] | index("ITSM-10483") != null' >/dev/null
echo "$requests" | jq -e '[.values[].issueKey] | index("ITSM-SSO") != null' >/dev/null
echo "$requests" | jq -e '[.values[].issueKey] | index("ITSM-1188") != null' >/dev/null

sso=$(read_json "$base/rest/servicedeskapi/request/ITSM-SSO")
echo "$sso" | jq -e '[.requestFieldValues[].value] | any(test("contoso-eu"))' >/dev/null
echo "$sso" | jq -e '.reporter.emailAddress == "mei.chen@contoso.example"' >/dev/null

cancel=$(read_json "$base/rest/servicedeskapi/request/ITSM-1188")
echo "$cancel" | jq -e '.summary == "Cancel TinyShop annual Pro"' >/dev/null
echo "$cancel" | jq -e '[.requestFieldValues[].value] | any(test("INV-1188"))' >/dev/null

comment=$(read_json -X POST "$base/rest/servicedeskapi/request/ITSM-4812/comment" -d '{"body":"Refund queued for INV-4812.","public":true}')
echo "$comment" | jq -e '.public == true and (.body | test("Refund queued"))' >/dev/null

comments=$(read_json "$base/rest/servicedeskapi/request/ITSM-4812/comment")
echo "$comments" | jq -e '[.values[].body] | any(test("Refund queued"))' >/dev/null
echo "$comments" | jq -e '.size == 1' >/dev/null

created=$(read_json -X POST "$base/rest/servicedeskapi/request" -d '{"serviceDeskId":"1","requestTypeId":"1","requestFieldValues":{"summary":"Follow up Northwind","description":"Check INV-4812"}}')
created_key=$(echo "$created" | jq -r '.issueKey')
test -n "$created_key"
echo "$created" | jq -e '.reporter.emailAddress == "sam@acme.example"' >/dev/null

again=$(read_json "$base/rest/servicedeskapi/request")
echo "$again" | jq -e --arg key "$created_key" '[.values[].issueKey] | index($key) != null' >/dev/null
echo "$again" | jq -e '.size == 5' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 5,
    "messagesBefore": 4,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Jira Service Management REST API 3",
        "environment": {
            "label": "Acme Jira Service Management",
            "manifest": "environments/acme-jsm.yaml",
            "messages": 4,
        },
        "integration": "jsm",
        "modes": {},
        "operationLabels": {
            "read": "List customer requests",
            "write": "Create request comment",
            "persistence": "Confirm comment persisted",
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
