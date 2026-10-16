#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_GAINSIGHT_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_GAINSIGHT_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_GAINSIGHT_URL:-}" ]; then
    echo "FAB_GAINSIGHT_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_GAINSIGHT_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme.gainsightcloud.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

companies=$(read_json -X POST "$base/v1/data/objects/query/Company" -d '{"select":["Name","Gsid","Health__gc"],"limit":100,"offset":0}')
echo "$companies" | jq -e '.data | length == 4' >/dev/null
echo "$companies" | jq -e '[.data[] | select(.Name=="Northwind Traders").Health__gc] == ["Red"]' >/dev/null
echo "$companies" | jq -e '[.data[] | select(.Name=="TinyShop").Health__gc] == ["Yellow"]' >/dev/null
echo "$companies" | jq -e '[.data[] | select(.Name=="Fernworks").Health__gc] == ["Green"]' >/dev/null
echo "$companies" | jq -e '[.data[] | select(.Name=="Contoso").Health__gc] == ["Red"]' >/dev/null

ctas=$(read_json -X POST "$base/v2/cockpit/cta/list" -d '{"select":["Name","Comments"],"pageSize":100,"pageNumber":1}')
echo "$ctas" | jq -e '[.data[].Comments] | join(" ") | test("INV-4812") and test("INV-1188") and test("30-day") and test("INV-2207") and test("contoso-eu")' >/dev/null

created=$(read_json -X POST "$base/v1/ant/es/activity" -d '{"records":[{"ExternalId":"ext-conformance-4812","ContextName":"CTA","ContextId":"cta-inv-4812","GsCompanyId":"company-northwind","Author":"sam@acme.example","TypeName":"Update","Subject":"Finance note on INV-4812","Notes":"Duplicate charge INV-4812 is still open."}]}')
echo "$created" | jq -e '.result == true' >/dev/null
echo "$created" | jq -e '.data.success["ext-conformance-4812"][0].activityId | length > 0' >/dev/null

persisted=$(read_json -X POST "$base/v1/data/objects/query/activity_timeline" -d '{"select":["ExternalId","Notes","ContextId"],"where":{"conditions":[{"name":"ExternalId","alias":"A","value":["ext-conformance-4812"],"operator":"EQ"}],"expression":"A"}}')
echo "$persisted" | jq -e '.data.records | length == 1' >/dev/null
echo "$persisted" | jq -e '.data.records[0].Notes | test("INV-4812")' >/dev/null
echo "$persisted" | jq -e '.data.records[0].ContextId == "cta-inv-4812"' >/dev/null

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
        "api": "Gainsight NXT v1",
        "environment": {
            "label": "Acme Gainsight",
            "manifest": "environments/acme-gainsight.yaml",
            "messages": 4,
        },
        "integration": "gainsight",
        "modes": {},
        "operationLabels": {
            "read": "Read companies and CTAs",
            "write": "Create timeline activity",
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
