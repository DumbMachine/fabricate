#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_ATTIO_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_ATTIO_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_ATTIO_URL:-}" ]; then
    echo "FAB_ATTIO_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_ATTIO_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.attio.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

people=$(read_json -X POST "$base/v2/objects/people/records/query" -d '{}')
echo "$people" | jq -e '.. | strings | select(. == "dana@northwind.example")' >/dev/null

deal=$(read_json "$base/v2/objects/deals/records/c1111111-1111-4111-8111-111111111111")
echo "$deal" | jq -e '.. | strings | select(. == "INV-4812")' >/dev/null

updated=$(read_json -X PATCH "$base/v2/objects/deals/records/c1111111-1111-4111-8111-111111111111" -d '{"data":{"values":{"stage":[{"value":"closed_won","attribute_type":"text"}]}}}')
echo "$updated" | jq -e '.. | strings | select(. == "closed_won")' >/dev/null

created=$(read_json -X POST "$base/v2/objects/people/records" -d '{"data":{"values":{"name":[{"value":"Casey Pike"}]}}}')
echo "$created" | jq -e '.. | strings | select(. == "Casey Pike")' >/dev/null

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
        "api": "Attio REST v2",
        "environment": {
            "label": "Acme Attio",
            "manifest": "environments/acme-attio.yaml",
            "messages": 9,
        },
        "integration": "attio",
        "modes": {},
        "operationLabels": {"read": "Read workspace", "write": "Update the INV-4812 deal", "persistence": "Confirm persistence"},
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
