#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_DOCUSIGN_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_DOCUSIGN_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_DOCUSIGN_URL:-}" ]; then
    echo "FAB_DOCUSIGN_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_DOCUSIGN_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://demo.docusign.net"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

list_path="$base/restapi/v2.1/accounts/acct-acme/envelopes"

envelopes=$(read_json "$list_path")
echo "$envelopes" | jq -e '.totalSetSize == "3"' >/dev/null
echo "$envelopes" | jq -e '[.envelopes[].envelopeId] | index("env-fernworks-msa")' >/dev/null
echo "$envelopes" | jq -e '[.envelopes[].envelopeId] | index("env-tinyshop-refund")' >/dev/null
echo "$envelopes" | jq -e '[.envelopes[].envelopeId] | index("env-offer-jordan")' >/dev/null

waiting=$(read_json "$list_path/env-tinyshop-refund")
echo "$waiting" | jq -e '.status == "sent"' >/dev/null
echo "$waiting" | jq -e '.recipients.signers[0].email == "marco@tinyshop.example"' >/dev/null
echo "$waiting" | jq -e '.envelopeDocuments[0].name == "TinyShop-cancellation.pdf"' >/dev/null

created=$(read_json -X POST "$list_path" -d '{"emailSubject":"Please sign the Northwind expansion addendum","status":"sent","documents":[{"name":"Northwind-addendum.pdf","fileExtension":"pdf","documentBase64":"Tm9ydGh3aW5kLWFkZGVuZHVtLnBkZgo="}],"recipients":{"signers":[{"name":"Dana Whitfield","email":"dana@northwind.example","routingOrder":"1"}]}}')
echo "$created" | jq -e '.status == "sent"' >/dev/null
created_id=$(echo "$created" | jq -r '.envelopeId')
test -n "$created_id"

persisted=$(read_json "$list_path/$created_id")
echo "$persisted" | jq -e '.emailSubject == "Please sign the Northwind expansion addendum"' >/dev/null
echo "$persisted" | jq -e '.recipients.signers[0].email == "dana@northwind.example"' >/dev/null
echo "$persisted" | jq -e '.envelopeDocuments[0].name == "Northwind-addendum.pdf"' >/dev/null

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
        "api": "DocuSign eSignature REST API v2.1",
        "environment": {
            "label": "Acme DocuSign",
            "manifest": "environments/acme-docusign.yaml",
            "messages": 3,
        },
        "integration": "docusign",
        "modes": {},
        "operationLabels": {
            "read": "List envelopes",
            "write": "Create envelope",
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
