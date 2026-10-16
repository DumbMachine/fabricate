#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_VANTA_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_VANTA_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_VANTA_URL:-}" ]; then
    echo "FAB_VANTA_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_VANTA_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.vanta.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

vendors=$(read_json "$base/v1/vendors")
echo "$vendors" | jq -e '[.results.data[].name] | (index("Shiprocket") != null and index("Razorpay") != null)' >/dev/null
echo "$vendors" | jq -e '.results.data[] | select(.name=="Razorpay") | .latestDecision.status == "APPROVED"' >/dev/null
echo "$vendors" | jq -e '.results.data[] | select(.name=="Shiprocket") | .latestDecision == null' >/dev/null

tests=$(read_json "$base/v1/tests")
echo "$tests" | jq -e '.results.data | length == 1' >/dev/null
echo "$tests" | jq -e '.results.data[0].name == "Access review for Shopify admin"' >/dev/null
echo "$tests" | jq -e '.results.data[0].status == "NEEDS_ATTENTION"' >/dev/null

test=$(read_json "$base/v1/tests/shopify-admin-access-review")
echo "$test" | jq -e '.failureDescription | contains("old.contractor@acme.example") and contains("access-review-2026-08.pdf")' >/dev/null

entities=$(read_json "$base/v1/tests/shopify-admin-access-review/entities")
echo "$entities" | jq -e '.results.data[0].displayName == "old.contractor@acme.example" and .results.data[0].entityStatus == "FAILING"' >/dev/null

uploads=$(read_json "$base/v1/documents/doc-shopify-admin-access/uploads")
echo "$uploads" | jq -e '.results.data[0].fileName == "access-review-2026-08.pdf"' >/dev/null

docs_before=$(read_json "$base/v1/documents")
echo "$docs_before" | jq -e '.results.data | length == 1' >/dev/null

created=$(read_json -X POST "$base/v1/documents" -d '{"title":"NDR evidence note","description":"Shipment 10483 NDR packet.","timeSensitivity":"MOST_RECENT","cadence":"P0D","reminderWindow":"P0D","isSensitive":false}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id" && test "$created_id" != "null"
echo "$created" | jq -e '.title == "NDR evidence note" and .uploadStatus == "Needs document"' >/dev/null

persisted=$(read_json "$base/v1/documents/$created_id")
echo "$persisted" | jq -e '.title == "NDR evidence note"' >/dev/null

docs_after=$(read_json "$base/v1/documents")
echo "$docs_after" | jq -e --arg id "$created_id" '[.results.data[].id] | index($id) != null' >/dev/null
echo "$docs_after" | jq -e '.results.data | length == 2' >/dev/null

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
        "api": "Vanta Manage API v1",
        "environment": {
            "label": "Acme Vanta",
            "manifest": "environments/acme-vanta.yaml",
            "messages": 1,
        },
        "integration": "vanta",
        "modes": {},
        "operationLabels": {
            "read": "List vendors and the failing access test",
            "write": "Create a document",
            "persistence": "Confirm the document persists",
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
