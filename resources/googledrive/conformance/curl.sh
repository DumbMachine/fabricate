#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_GOOGLEDRIVE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_GOOGLEDRIVE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_GOOGLEDRIVE_URL:-}" ]; then
    echo "FAB_GOOGLEDRIVE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_GOOGLEDRIVE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://www.googleapis.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

file=$(read_json "$base/drive/v3/files/file-razorpay-settlement-2026-08-25")
echo "$file" | jq -e '.name == "razorpay-settlement-2026-08-25.csv"' >/dev/null
echo "$file" | jq -e '.description | contains("pay_Acme10482") and contains("pay_Acme10483") and contains("pay_Acme10484") and contains("rfnd_Acme10484")' >/dev/null

listed=$(read_json "$base/drive/v3/files?pageSize=100")
echo "$listed" | jq -e '.files | length == 13' >/dev/null
echo "$listed" | jq -e '[.files[].name] | index("INV-4812-northwind.pdf")' >/dev/null

created=$(read_json -X POST "$base/drive/v3/files" -d '{"name":"acme-drive-note.txt","mimeType":"text/plain","parents":["folder-finance"],"description":"Created during conformance"}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id" && test "$created_id" != "null"

again=$(read_json "$base/drive/v3/files/$created_id")
echo "$again" | jq -e '.name == "acme-drive-note.txt"' >/dev/null
echo "$again" | jq -e '.parents == ["folder-finance"]' >/dev/null

listed_after=$(read_json "$base/drive/v3/files?pageSize=100")
echo "$listed_after" | jq -e '.files | length == 14' >/dev/null
echo "$listed_after" | jq -e --arg id "$created_id" '[.files[].id] | index($id)' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 14,
    "messagesBefore": 13,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Google Drive API v3",
        "environment": {
            "label": "Acme Google Drive",
            "manifest": "environments/acme-googledrive.yaml",
            "messages": 13,
        },
        "integration": "googledrive",
        "modes": {},
        "operationLabels": {
            "read": "Read Drive file",
            "write": "Create file",
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
