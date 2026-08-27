#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_FIGMA_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_FIGMA_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_FIGMA_URL:-}" ]; then
    echo "FAB_FIGMA_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_FIGMA_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.figma.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

listed=$(read_json "$base/v1/teams/team_acme/webhooks")
echo "$listed" | jq -e '.. | strings | select(. == "https://acme.example/hooks/files")' >/dev/null

got=$(read_json "$base/v1/webhooks/wh_files")
echo "$got" | jq -e '.. | strings | select(. == "https://acme.example/hooks/files")' >/dev/null

created=$(read_json -X POST "$base/v1/webhooks" -d '{"event_type":"FILE_UPDATE","team_id":"team_acme","endpoint":"https://acme.example/hooks/figma"}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id" && test "$created_id" != "null"

persisted=$(read_json "$base/v1/webhooks/$created_id")
echo "$persisted" | jq -e '.. | strings | select(. == "https://acme.example/hooks/figma")' >/dev/null

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
        "api": "Figma API",
        "environment": {
            "label": "Acme Figma",
            "manifest": "environments/acme-figma.yaml",
            "messages": 3,
        },
        "integration": "figma",
        "modes": {},
        "operationLabels": {"read": "List webhooks", "write": "Create a webhook", "persistence": "Confirm persistence"},
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
