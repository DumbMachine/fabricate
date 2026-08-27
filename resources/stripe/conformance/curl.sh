#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_STRIPE_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_STRIPE_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_STRIPE_URL:-}" ]; then
    echo "FAB_STRIPE_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_STRIPE_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.stripe.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

read_json() {
  curl "${curl_opts[@]}" -H "Authorization: Bearer $token" -H "Content-Type: application/json" "$@"
}

listed=$(read_json "$base/v1/customers")
echo "$listed" | jq -e '.. | strings | select(. == "Northwind")' >/dev/null

got=$(read_json "$base/v1/customers/cus_northwind")
echo "$got" | jq -e '.. | strings | select(. == "Northwind")' >/dev/null

created=$(read_json -X POST "$base/v1/customers" -d '{"name":"Acme Billing","email":"billing@acme.example"}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id" && test "$created_id" != "null"

persisted=$(read_json "$base/v1/customers/$created_id")
echo "$persisted" | jq -e '.. | strings | select(. == "Acme Billing")' >/dev/null

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
        "api": "Stripe API",
        "environment": {
            "label": "Acme Stripe",
            "manifest": "environments/acme-stripe.yaml",
            "messages": 3,
        },
        "integration": "stripe",
        "modes": {},
        "operationLabels": {"read": "List customers", "write": "Create a customer", "persistence": "Confirm persistence"},
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
