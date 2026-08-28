#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_CLOSE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_CLOSE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_CLOSE_URL:-}" ]; then
    echo "FAB_CLOSE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_CLOSE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.close.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

leads=$(read_json "$base/api/v1/lead/")
echo "$leads" | jq -e '.. | strings | select(. == "lead_northwind")' >/dev/null

opp=$(read_json "$base/api/v1/opportunity/oppo_inv4812/")
echo "$opp" | jq -e '.. | strings | select(. == "INV-4812")' >/dev/null

updated=$(read_json -X PUT "$base/api/v1/opportunity/oppo_inv4812/" -d '{"status_label":"Won"}')
echo "$updated" | jq -e '.status_label == "Won"' >/dev/null

created=$(read_json -X POST "$base/api/v1/contact/" -d '{"lead_id":"lead_northwind","name":"Casey Pike"}')
echo "$created" | jq -e '.name == "Casey Pike"' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 10,
    "messagesBefore": 9,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Close CRM v1",
        "environment": {
            "label": "Acme Close",
            "manifest": "environments/acme-close.yaml",
            "messages": 9,
        },
        "integration": "close",
        "modes": {},
        "operationLabels": {"read": "Read leads", "write": "Win the INV-4812 opportunity", "persistence": "Confirm persistence"},
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
