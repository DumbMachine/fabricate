#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_OKTA_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_OKTA_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_OKTA_URL:-}" ]; then
    echo "FAB_OKTA_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_OKTA_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://acme.okta.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

users=$(read_json "$base/api/v1/users")
echo "$users" | jq -e 'map(select(.profile.email == "val@acme.example" and .status == "ACTIVE")) | length == 1' >/dev/null
echo "$users" | jq -e 'map(select(.profile.email == "jordan.hale@acme.example" and .status == "STAGED")) | length == 1' >/dev/null
echo "$users" | jq -e 'map(select(.status == "DEPROVISIONED")) | length == 0' >/dev/null
echo "$users" | jq -e 'length == 8' >/dev/null

deprovisioned=$(read_json "$base/api/v1/users?search=status%20eq%20%22DEPROVISIONED%22")
echo "$deprovisioned" | jq -e '.[0].profile.login == "old.contractor@acme.example"' >/dev/null

finance=$(read_json "$base/api/v1/groups/00g-finance/users")
echo "$finance" | jq -e '[.[].profile.email] | index("aisha@acme.example") and index("val@acme.example")' >/dev/null

checkout=$(read_json "$base/api/v1/apps/0oa-acme-checkout")
echo "$checkout" | jq -e '.label == "Acme Checkout"' >/dev/null
echo "$checkout" | jq -e '.settings.notes.admin | test("contoso-eu")' >/dev/null

assigned=$(read_json "$base/api/v1/apps/0oa-shopify-admin/users")
echo "$assigned" | jq -e '[.[].id] | index("00u-val") and index("00u-ravi")' >/dev/null

created=$(read_json -X POST "$base/api/v1/users" -d '{"profile":{"firstName":"Casey","lastName":"Ng","email":"casey.ng@acme.example","login":"casey.ng@acme.example"}}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id"
echo "$created" | jq -e '.status == "ACTIVE"' >/dev/null

fetched=$(read_json "$base/api/v1/users/$created_id")
echo "$fetched" | jq -e '.profile.email == "casey.ng@acme.example"' >/dev/null

users_after=$(read_json "$base/api/v1/users")
echo "$users_after" | jq -e --arg id "$created_id" '[.[].id] | index($id)' >/dev/null
echo "$users_after" | jq -e 'length == 9' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 9,
    "messagesBefore": 8,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Okta Management API 2024-07",
        "environment": {
            "label": "Acme Okta",
            "manifest": "environments/acme-okta.yaml",
            "messages": 9,
        },
        "integration": "okta",
        "modes": {},
        "operationLabels": {
            "read": "List users",
            "write": "Create user",
            "persistence": "Confirm created user",
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
