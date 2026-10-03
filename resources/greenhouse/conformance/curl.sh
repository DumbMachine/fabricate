#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_GREENHOUSE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_GREENHOUSE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_GREENHOUSE_URL:-}" ]; then
    echo "FAB_GREENHOUSE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_GREENHOUSE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://harvest.greenhouse.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

jobs=$(read_json "$base/v1/jobs")
echo "$jobs" | jq -e 'any(.[]; .id == "job-support" and .name == "Support Engineer")' >/dev/null

candidates=$(read_json "$base/v1/candidates")
echo "$candidates" | jq -e 'any(.[]; any(.email_addresses[]; .value == "jordan.hale@acme.example"))' >/dev/null
echo "$candidates" | jq -e 'any(.[]; any(.email_addresses[]; .value == "sasha.iqbal@example.com"))' >/dev/null

jordan=$(read_json "$base/v1/candidates/cand-jordan")
echo "$jordan" | jq -e 'any(.applications[]; .id == "app-jordan" and .current_stage.name == "Offer")' >/dev/null

offers=$(read_json "$base/v1/offers")
echo "$offers" | jq -e 'any(.[]; .id == "offer-jordan" and .status == "accepted" and .application_id == "app-jordan")' >/dev/null

app_before=$(read_json "$base/v1/applications/app-jordan")
echo "$app_before" | jq -e '.id == "app-jordan" and .current_stage.name == "Offer"' >/dev/null
before=$(echo "$app_before" | jq -r '.last_activity_at')
test -n "$before"

note=$(read_json -X POST "$base/v1/candidates/cand-jordan/activity_feed/notes" \
  -H "On-Behalf-Of: user-leo" \
  -d '{"user_id":"user-leo","body":"Offer letter filed for app-jordan.","visibility":"public"}')
echo "$note" | jq -e '.body == "Offer letter filed for app-jordan." and .user.id == "user-leo"' >/dev/null

feed=$(read_json "$base/v1/candidates/cand-jordan/activity_feed")
echo "$feed" | jq -e 'any(.notes[]; .body == "Offer letter filed for app-jordan.")' >/dev/null

app_after=$(read_json "$base/v1/applications/app-jordan")
after=$(echo "$app_after" | jq -r '.last_activity_at')
test -n "$after"
test "$after" != "$before"
echo "$app_after" | jq -e '.id == "app-jordan" and .candidate_id == "cand-jordan"' >/dev/null

sasha=$(read_json "$base/v1/applications/app-sasha")
echo "$sasha" | jq -e '.current_stage.name == "On-site" and .last_activity_at == "2026-08-24T10:30:00Z"' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 1,
    "messagesBefore": 0,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Greenhouse Harvest API v1",
        "environment": {
            "label": "Acme Greenhouse",
            "manifest": "environments/acme-greenhouse.yaml",
            "messages": 0,
        },
        "integration": "greenhouse",
        "modes": {},
        "operationLabels": {
            "read": "List jobs, candidates, and offers",
            "write": "Add a note for app-jordan",
            "persistence": "Confirm the note and application activity",
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
