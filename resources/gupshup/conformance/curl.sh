#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_GUPSHUP_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_GUPSHUP_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_GUPSHUP_URL:-}" ]; then
    echo "FAB_GUPSHUP_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_GUPSHUP_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.gupshup.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

business=$(read_json "$base/wa/app/app_acme_goods/business")
echo "$business" | jq -e '.business.name == "Acme Goods"' >/dev/null

messages=$(read_json "$base/wa/api/v1/msg")
echo "$messages" | jq -e '.messages | length == 4' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482") | .destination][0] == "+919845011223"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482") | .status][0] == "delivered"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482") | .text][0] | contains("10482") and contains("SR10482AWB")' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10483") | .destination][0] == "+919811122008"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10483") | .status][0] == "failed"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10483") | .text][0] | contains("10483") and contains("delivery window")' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10484") | .destination][0] == "+919900048120"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10484") | .text][0] | contains("rfnd_Acme10484")' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482_in") | .text][0] == "thanks, where is the tee?"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482_in") | .senderName][0] == "Priya Nair"' >/dev/null
echo "$messages" | jq -e '[.messages[] | select(.messageId=="msg_10482_in") | .contextGsId][0] == "msg_10482"' >/dev/null

sent=$(curl "${curl_opts[@]}" "${auth[@]}" -H "Content-Type: application/x-www-form-urlencoded" \
  -X POST "$base/wa/api/v1/msg" \
  --data-urlencode "channel=whatsapp" \
  --data-urlencode "source=918041230101" \
  --data-urlencode "destination=+919811122008" \
  --data-urlencode "src.name=Acme Goods" \
  --data-urlencode 'message={"type":"text","text":"Please share a delivery window for order 10483."}')
echo "$sent" | jq -e '.status == "submitted"' >/dev/null
message_id=$(echo "$sent" | jq -r '.messageId')
test -n "$message_id"
test "$message_id" != "null"

one=$(read_json "$base/wa/api/v1/msg/$message_id")
echo "$one" | jq -e --arg id "$message_id" '.message.messageId == $id and (.message.text | contains("delivery window"))' >/dev/null

after=$(read_json "$base/wa/api/v1/msg")
echo "$after" | jq -e --arg id "$message_id" '.messages | length == 5 and any(.[]; .messageId == $id)' >/dev/null

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
        "api": "Gupshup WhatsApp API v1",
        "environment": {
            "label": "Acme Gupshup",
            "manifest": "environments/acme-gupshup.yaml",
            "messages": 4,
        },
        "integration": "gupshup",
        "modes": {},
        "operationLabels": {
            "read": "Read WhatsApp messages",
            "write": "Send a message",
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
