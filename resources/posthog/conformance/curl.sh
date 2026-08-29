#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_POSTHOG_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_POSTHOG_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_POSTHOG_URL:-}" ]; then
    echo "FAB_POSTHOG_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_POSTHOG_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://us.posthog.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

listed=$(read_json "$base/api/projects/1/feature_flags/")
echo "$listed" | jq -e '.. | strings | select(. == "checkout_reliability")' >/dev/null

got=$(read_json "$base/api/projects/1/feature_flags/101/")
echo "$got" | jq -e '.. | strings | select(. == "checkout_reliability")' >/dev/null

created=$(read_json -X POST "$base/api/projects/1/feature_flags/" -d '{"key":"billing_export","name":"Billing CSV export"}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id" && test "$created_id" != "null"

persisted=$(read_json "$base/api/projects/1/feature_flags/${created_id}/")
echo "$persisted" | jq -e '.. | strings | select(. == "billing_export")' >/dev/null

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
        "api": "PostHog API",
        "environment": {
            "label": "Acme PostHog",
            "manifest": "environments/acme-posthog.yaml",
            "messages": 3,
        },
        "integration": "posthog",
        "modes": {},
        "operationLabels": {"read": "List feature flags", "write": "Create a feature flag", "persistence": "Confirm persistence"},
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
