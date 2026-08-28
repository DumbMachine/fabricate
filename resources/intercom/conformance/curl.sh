#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_INTERCOM_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_INTERCOM_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_INTERCOM_URL:-}" ]; then
    echo "FAB_INTERCOM_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_INTERCOM_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.intercom.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json" -H "Accept: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

me=$(read_json "$base/me")
echo "$me" | jq -e '.email == "val@acme.example"' >/dev/null

conversations=$(read_json "$base/conversations")
echo "$conversations" | jq -e '[.conversations[].id] | index("101")' >/dev/null

replied=$(read_json -X POST "$base/conversations/101/reply" -d '{"type":"admin","message_type":"comment","body":"Refund issued as rf_4812."}')
echo "$replied" | jq -e '.conversation_parts.conversation_parts | map(.body) | any(test("Refund issued"))' >/dev/null

retrieved=$(read_json "$base/conversations/101")
echo "$retrieved" | jq -e '.conversation_parts.conversation_parts | map(.body) | any(test("Refund issued"))' >/dev/null
echo "$retrieved" | jq -e '.conversation_parts.total_count == 2' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 4,
    "messagesBefore": 3,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Intercom API 2.16",
        "environment": {
            "label": "Acme Intercom",
            "manifest": "environments/acme-intercom.yaml",
            "messages": 3,
        },
        "integration": "intercom",
        "modes": {},
        "operationLabels": {
            "read": "Read inbox",
            "write": "Reply to billing conversation",
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
