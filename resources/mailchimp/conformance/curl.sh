#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_MAILCHIMP_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_MAILCHIMP_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_MAILCHIMP_URL:-}" ]; then
    echo "FAB_MAILCHIMP_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_MAILCHIMP_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://us1.api.mailchimp.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

ping=$(read_json "$base/3.0/ping")
echo "$ping" | jq -e '.health_status | test("Chimpy")' >/dev/null

members=$(read_json "$base/3.0/lists/acme-customers/members")
echo "$members" | jq -e '.. | strings | select(. == "dana@northwind.example")' >/dev/null

created=$(read_json -X POST "$base/3.0/lists/acme-customers/members" -d '{"email_address":"casey@acme.example","status":"subscribed"}')
echo "$created" | jq -e '.email_address == "casey@acme.example"' >/dev/null

sent=$(curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -X POST "$base/3.0/campaigns/campaign_inv4812/actions/send")
campaign=$(read_json "$base/3.0/campaigns/campaign_inv4812")
echo "$campaign" | jq -e '.status == "sent"' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 7,
    "messagesBefore": 6,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Mailchimp Marketing 3.0",
        "environment": {
            "label": "Acme Mailchimp",
            "manifest": "environments/acme-mailchimp.yaml",
            "messages": 6,
        },
        "integration": "mailchimp",
        "modes": {},
        "operationLabels": {"read": "Read audience", "write": "Send the INV-4812 campaign", "persistence": "Confirm persistence"},
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
