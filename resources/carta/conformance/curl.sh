#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_CARTA_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_CARTA_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_CARTA_URL:-}" ]; then
    echo "FAB_CARTA_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_CARTA_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://api.carta.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

issuer=$(read_json "$base/v1alpha1/issuers/issuer-acme")
echo "$issuer" | jq -e '.issuer.legalName == "Acme"' >/dev/null

stakeholders=$(read_json "$base/v1alpha1/issuers/issuer-acme/stakeholders")
echo "$stakeholders" | jq -e '[.stakeholders[].email] | index("val@acme.example")' >/dev/null
echo "$stakeholders" | jq -e '[.stakeholders[].fullName] | index("Harbor Capital")' >/dev/null

val=$(read_json "$base/v1alpha1/issuers/issuer-acme/stakeholders/stk-val")
echo "$val" | jq -e '.stakeholder.email == "val@acme.example"' >/dev/null

certificates=$(read_json "$base/v1alpha1/issuers/issuer-acme/certificates")
echo "$certificates" | jq -e '.certificates[0].shareClassName == "Preferred"' >/dev/null
echo "$certificates" | jq -e '.certificates[0].stakeholderId == "stk-harbor"' >/dev/null

grants=$(read_json "$base/v1alpha1/issuers/issuer-acme/optionGrants")
echo "$grants" | jq -e '.optionGrants[0].stakeholderId == "stk-val"' >/dev/null
echo "$grants" | jq -e '.optionGrants[0].stockOptionType == "ISO"' >/dev/null

created=$(read_json -X POST "$base/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/LATEST/draftOptionGrants" -d '{
  "draftOptionGrant": {
    "stockOptionType": "STOCK_OPTION_TYPE_ISO",
    "grantReason": "GRANT_REASON_REFRESH",
    "quantity": {"value": "5000"},
    "exercisePrice": {"currencyCode": {"value": "USD"}, "amount": {"value": "0.42"}},
    "stakeholder": {
      "name": "Val Ortega",
      "email": "val@acme.example",
      "type": "STAKEHOLDER_TYPE_INDIVIDUAL",
      "relationship": "STAKEHOLDER_RELATIONSHIP_EMPLOYEE"
    },
    "notes": "Refresh grant pending board consent."
  }
}')
echo "$created" | jq -e '.draftOptionGrant.notes == "Refresh grant pending board consent."' >/dev/null
set_id=$(echo "$created" | jq -r '.draftOptionGrant.draftOptionGrantSetId')
grant_id=$(echo "$created" | jq -r '.draftOptionGrant.id')
test -n "$set_id"
test -n "$grant_id"

fetched=$(read_json "$base/v1alpha1/issuers/issuer-acme/draftOptionGrantSets/$set_id/draftOptionGrants/$grant_id")
echo "$fetched" | jq -e '.draftOptionGrant.notes == "Refresh grant pending board consent."' >/dev/null
echo "$fetched" | jq -e '.draftOptionGrant.stakeholder.email == "val@acme.example"' >/dev/null

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
        "api": "Carta API Platform v1alpha1",
        "environment": {
            "label": "Acme Carta",
            "manifest": "environments/acme-carta.yaml",
            "messages": 2,
        },
        "integration": "carta",
        "modes": {},
        "operationLabels": {
            "read": "List stakeholders",
            "write": "Create draft option grant",
            "persistence": "Confirm the draft note",
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
