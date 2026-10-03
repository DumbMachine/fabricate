#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_SLACK_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_SLACK_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_SLACK_URL:-}" ]; then
    echo "FAB_SLACK_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_SLACK_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://slack.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

auth_test=$(read_json "$base/api/auth.test")
echo "$auth_test" | jq -e '.ok == true and .user == "val" and .user_id == "U-val" and .team_id == "T-acme"' >/dev/null

users=$(read_json "$base/api/users.list")
echo "$users" | jq -e '[.members[].profile.email] | sort == ["aisha@acme.example","iris@acme.example","leo@acme.example","nina@acme.example","ravi@acme.example","sam@acme.example","val@acme.example"]' >/dev/null

channels=$(read_json "$base/api/conversations.list")
echo "$channels" | jq -e '[.channels[].id] | sort == ["C-finance","C-fulfillment","C-hiring","C-incidents","C-support"]' >/dev/null

history=$(read_json "$base/api/conversations.history?channel=C-fulfillment")
echo "$history" | jq -e '.messages | length == 2' >/dev/null
echo "$history" | jq -e '[.messages[].text] | any(contains("#10482") and contains("SR10482AWB"))' >/dev/null
echo "$history" | jq -e '[.messages[].text] | any(contains("#10483"))' >/dev/null
echo "$history" | jq -e '[.messages[] | select(.user == "U-ravi") | .text] | any(contains("#10482"))' >/dev/null
echo "$history" | jq -e '[.messages[] | select(.user == "U-sam") | .text] | any(contains("#10483"))' >/dev/null

finance=$(read_json "$base/api/conversations.history?channel=C-finance")
echo "$finance" | jq -e '.messages[0].user == "U-aisha"' >/dev/null
echo "$finance" | jq -e '.messages[0].text | contains("rfnd_Acme10484") and contains("INV-4812") and contains("different")' >/dev/null

incidents=$(read_json "$base/api/conversations.history?channel=C-incidents")
echo "$incidents" | jq -e '[.messages[].text] | any(contains("contoso-eu"))' >/dev/null

posted=$(read_json -X POST "$base/api/chat.postMessage" -H "Content-Type: application/json" \
  -d '{"channel":"C-fulfillment","text":"Dock check for #10482, AWB SR10482AWB."}')
echo "$posted" | jq -e '.ok == true and .channel == "C-fulfillment"' >/dev/null
posted_ts=$(echo "$posted" | jq -r '.ts')
test -n "$posted_ts"

after=$(read_json "$base/api/conversations.history?channel=C-fulfillment")
echo "$after" | jq -e '.messages | length == 3' >/dev/null
echo "$after" | jq -e --arg ts "$posted_ts" '[.messages[].ts] | index($ts) != null' >/dev/null
echo "$after" | jq -e '[.messages[].text] | any(contains("Dock check"))' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 3,
    "messagesBefore": 2,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Slack Web API v2",
        "environment": {
            "label": "Acme Slack",
            "manifest": "environments/acme-slack.yaml",
            "messages": 2,
        },
        "integration": "slack",
        "modes": {},
        "operationLabels": {
            "read": "Read fulfillment history",
            "write": "Post a channel message",
            "persistence": "Confirm history contains it",
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
