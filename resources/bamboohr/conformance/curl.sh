#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_BAMBOOHR_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_BAMBOOHR_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_BAMBOOHR_URL:-}" ]; then
    echo "FAB_BAMBOOHR_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_BAMBOOHR_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme.bamboohr.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

employees=$(read_json "$base/api/v1/employees?fields=workEmail,departmentName,employmentStatus,hireDate")
echo "$employees" | jq -e '.meta.total == 8' >/dev/null
echo "$employees" | jq -e '[.data[].employeeId] | index("103")' >/dev/null
echo "$employees" | jq -e '[.data[] | select(.employeeId == "190") | .employmentStatus] == ["Onboarding"]' >/dev/null
echo "$employees" | jq -e '[.data[] | select(.employeeId == "190") | .hireDate] == ["2026-09-08"]' >/dev/null

jordan=$(read_json "$base/api/v1/employees/190?fields=firstName,lastName,department,employmentStatus,hireDate")
echo "$jordan" | jq -e '.firstName == "Jordan" and .lastName == "Hale"' >/dev/null
echo "$jordan" | jq -e '.department == "Support"' >/dev/null

before=$(read_json "$base/api/v1/time_off/requests?start=2026-08-01&end=2026-08-31")
echo "$before" | jq -e 'length == 1' >/dev/null
echo "$before" | jq -e '.[0].employeeId == "103"' >/dev/null
echo "$before" | jq -e '.[0].notes.employee == "warehouse visit"' >/dev/null
echo "$before" | jq -e '.[0].start == "2026-08-28" and .[0].end == "2026-08-29"' >/dev/null

created=$(read_json -X PUT "$base/api/v1/employees/104/time_off/request" -d '{"status":"requested","start":"2026-09-03","end":"2026-09-03","timeOffTypeId":"1","notes":[{"from":"employee","note":"people ops coverage"}]}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id"
echo "$created" | jq -e '.notes.employee == "people ops coverage"' >/dev/null

after=$(read_json "$base/api/v1/time_off/requests?start=2026-08-01&end=2026-09-30")
echo "$after" | jq -e 'length == 2' >/dev/null
echo "$after" | jq -e --arg id "$created_id" '[.[].id] | index($id)' >/dev/null
echo "$after" | jq -e '[.[].notes.employee] | index("people ops coverage")' >/dev/null

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
        "api": "BambooHR API v1",
        "environment": {
            "label": "Acme BambooHR",
            "manifest": "environments/acme-bamboohr.yaml",
            "messages": 8,
        },
        "integration": "bamboohr",
        "modes": {},
        "operationLabels": {
            "read": "List employees and time off",
            "write": "Create time off request",
            "persistence": "Confirm the request is listed",
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
