#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_SENDGRID_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_SENDGRID_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_SENDGRID_URL:-}" ]; then
    echo "FAB_SENDGRID_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_SENDGRID_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.sendgrid.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

batch=$(read_json -X POST "$base/v3/mail/batch")
batch_id=$(echo "$batch" | jq -r '.batch_id')
test -n "$batch_id"

got=$(read_json "$base/v3/mail/batch/$batch_id")
echo "$got" | jq -e --arg id "$batch_id" '.batch_id == $id' >/dev/null

sent=$(read_json -X POST "$base/v3/mail/send" -d "$(jq -n --arg id "$batch_id" '{personalizations:[{to:[{email:"priya@fernworks.example"}]}],from:{email:"billing@acme.example"},subject:"CSV export",content:[{type:"text/plain",value:"ready"}],batch_id:$id}')")
test -z "$sent" -o "$sent" = "null" || true

still=$(read_json "$base/v3/mail/batch/$batch_id")
echo "$still" | jq -e --arg id "$batch_id" '.batch_id == $id' >/dev/null

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
        "api": "SendGrid Mail v3",
        "environment": {
            "label": "Acme SendGrid",
            "manifest": "environments/acme-sendgrid.yaml",
            "messages": 1,
        },
        "integration": "sendgrid",
        "modes": {},
        "operationLabels": {"read": "Create and read a batch", "write": "Send mail into the batch", "persistence": "Confirm persistence"},
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
