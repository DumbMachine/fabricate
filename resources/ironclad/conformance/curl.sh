#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_IRONCLAD_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_IRONCLAD_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_IRONCLAD_URL:-}" ]; then
    echo "FAB_IRONCLAD_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_IRONCLAD_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://na1.ironcladapp.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

workflows=$(read_json "$base/public/api/v1/workflows")
echo "$workflows" | jq -e '[.list[].id] | index("wf-delhivery-msa") and index("wf-tinyshop-cancel")' >/dev/null
echo "$workflows" | jq -e '[.list[].id] | index("wf-fernworks-msa") | not' >/dev/null

fern=$(read_json "$base/public/api/v1/workflows/wf-fernworks-msa")
echo "$fern" | jq -e '.attributes.counterpartyName == "Priya Nair"' >/dev/null
echo "$fern" | jq -e '.attributes.docusignEnvelopeId == "env-fernworks-msa"' >/dev/null
echo "$fern" | jq -e '.attributes.draft[0].filename == "Fernworks-MSA.pdf"' >/dev/null
echo "$fern" | jq -e '.status == "completed"' >/dev/null

record=$(read_json "$base/public/api/v1/records/rec-fernworks-msa")
echo "$record" | jq -e '.properties.docusignEnvelopeId.value == "env-fernworks-msa"' >/dev/null
echo "$record" | jq -e '.attachments.signedCopy.filename == "Fernworks-MSA.pdf"' >/dev/null

tiny=$(read_json "$base/public/api/v1/records/rec-tinyshop-cancel")
echo "$tiny" | jq -e '.properties.docusignEnvelopeId.value == "env-tinyshop-refund"' >/dev/null

approvals=$(read_json "$base/public/api/v1/workflows/wf-delhivery-msa/approvals")
echo "$approvals" | jq -e '.roles[0].assignees[0].email == "iris@acme.example"' >/dev/null
echo "$approvals" | jq -e '.approvalGroups[0].reviewers[0].status == "pending"' >/dev/null

comment=$(read_json -X POST "$base/public/api/v1/workflows/wf-delhivery-msa/comments" -d '{"comment":"Iris, please review the Delhivery MSA."}')
echo "$comment" | jq -e '.commentMessage | test("Delhivery MSA")' >/dev/null

comments=$(read_json "$base/public/api/v1/workflows/wf-delhivery-msa/comments")
echo "$comments" | jq -e '[.list[].commentMessage] | any(test("Delhivery MSA"))' >/dev/null
echo "$comments" | jq -e '.count == 1' >/dev/null

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
        "api": "Ironclad Public API v1",
        "environment": {
            "label": "Acme Ironclad",
            "manifest": "environments/acme-ironclad.yaml",
            "messages": 1,
        },
        "integration": "ironclad",
        "modes": {},
        "operationLabels": {
            "read": "Read workflows",
            "write": "Comment on the Delhivery MSA",
            "persistence": "Confirm the comment",
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
