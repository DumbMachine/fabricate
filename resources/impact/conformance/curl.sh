#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_IMPACT_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_IMPACT_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_IMPACT_URL:-}" ]; then
    echo "FAB_IMPACT_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_IMPACT_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.impact.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Accept: application/json" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

partners=$(read_json "$base/Advertisers/acct-acme/MediaPartners")
echo "$partners" | jq -e '.Partners | length == 1' >/dev/null
echo "$partners" | jq -e '.Partners[0].Id == "partner_northwind"' >/dev/null
echo "$partners" | jq -e '.Partners[0].Name == "Northwind Creators"' >/dev/null

actions=$(read_json "$base/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=10482")
echo "$actions" | jq -e '.Actions | length == 1' >/dev/null
echo "$actions" | jq -e '.Actions[0].Oid == "10482"' >/dev/null
echo "$actions" | jq -e '.Actions[0].State == "PENDING"' >/dev/null
echo "$actions" | jq -e '.Actions[0].Payout == 150' >/dev/null
echo "$actions" | jq -e '.Actions[0].Currency == "INR"' >/dev/null
echo "$actions" | jq -e '.Actions[0].MediaPartnerId == "partner_northwind"' >/dev/null

one=$(read_json "$base/Advertisers/acct-acme/Actions/10482")
echo "$one" | jq -e '.Id == "act_10482"' >/dev/null
echo "$one" | jq -e '.CampaignName == "Acme Goods affiliates"' >/dev/null

created=$(read_json -X POST "$base/Advertisers/acct-acme/Conversions" -d '{"CampaignId":1001,"OrderId":"ord-conformance","MediaPartnerId":"partner_northwind","CurrencyCode":"INR","Amount":100,"Payout":1}')
echo "$created" | jq -e '.Status == "QUEUED"' >/dev/null

again=$(read_json "$base/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=ord-conformance")
echo "$again" | jq -e '.Actions | length == 1' >/dev/null
echo "$again" | jq -e '.Actions[0].State == "PENDING"' >/dev/null
echo "$again" | jq -e '.Actions[0].Payout == 1' >/dev/null

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
        "api": "impact.com Brand API v14",
        "environment": {
            "label": "Acme impact.com",
            "manifest": "environments/acme-impact.yaml",
            "messages": 1,
        },
        "integration": "impact",
        "modes": {},
        "operationLabels": {
            "read": "Read order 10482 action",
            "write": "Submit a conversion",
            "persistence": "Confirm the new action is listed",
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
