#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_REDSHIFT_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_REDSHIFT_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_REDSHIFT_URL:-}" ]; then
    echo "FAB_REDSHIFT_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_REDSHIFT_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://redshift-data.us-east-1.amazonaws.com"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

post() {
  local target=$1
  local body=$2
  curl "${curl_opts[@]}" \
    -H "Authorization: Bearer $token" \
    -H "Content-Type: application/x-amz-json-1.1" \
    -H "X-Amz-Target: RedshiftData.$target" \
    -d "$body" \
    "$base/"
}

orders=$(post ExecuteStatement '{"ClusterIdentifier":"acme-warehouse","Database":"analytics","Sql":"SELECT * FROM commerce.orders"}')
echo "$orders" | jq -e '.Status == "FINISHED" and .ClusterIdentifier == "acme-warehouse" and .Database == "analytics"' >/dev/null
order_id=$(echo "$orders" | jq -r '.Id')
test -n "$order_id" && test "$order_id" != "null"

order_rows=$(post GetStatementResult "$(jq -nc --arg id "$order_id" '{Id:$id}')")
echo "$order_rows" | jq -e '.TotalNumRows == 3' >/dev/null
echo "$order_rows" | jq -e '[.Records[][0].stringValue] | sort == ["10482","10483","10484"]' >/dev/null
echo "$order_rows" | jq -e '[.. | .stringValue? // empty] | index("1 x AG-TEE-02; 2 x AG-MUG-01")' >/dev/null

described=$(post DescribeStatement "$(jq -nc --arg id "$order_id" '{Id:$id}')")
echo "$described" | jq -e '.Status == "FINISHED" and .HasResultSet == true and .ResultRows == 3' >/dev/null

saas=$(post ExecuteStatement '{"ClusterIdentifier":"acme-warehouse","Database":"analytics","StatementName":"acme-saas","Sql":"SELECT * FROM commerce.saas_invoices"}')
saas_id=$(echo "$saas" | jq -r '.Id')
test -n "$saas_id" && test "$saas_id" != "null"
saas_rows=$(post GetStatementResult "$(jq -nc --arg id "$saas_id" '{Id:$id}')")
echo "$saas_rows" | jq -e '[.. | .stringValue? // empty] | index("INV-4812") and index("pay_saas_4812") and index("pay_saas_4812b")' >/dev/null
echo "$saas_rows" | jq -e '[.. | .longValue? // empty] | index(1240)' >/dev/null

persisted=$(post DescribeStatement "$(jq -nc --arg id "$saas_id" '{Id:$id}')")
echo "$persisted" | jq -e '.Status == "FINISHED" and (.QueryString | test("saas_invoices"))' >/dev/null

listed=$(post ListStatements '{}')
echo "$listed" | jq -e --arg id "$saas_id" --arg oid "$order_id" '([.Statements[].Id] | index($id)) and ([.Statements[].Id] | index($oid))' >/dev/null

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
        "api": "Amazon Redshift Data API 2012-12-01",
        "environment": {
            "label": "Acme Redshift",
            "manifest": "environments/acme-redshift.yaml",
            "messages": 10,
        },
        "integration": "redshift",
        "modes": {},
        "operationLabels": {
            "read": "Query commerce.orders",
            "write": "Execute a saas_invoices statement",
            "persistence": "DescribeStatement and GetStatementResult",
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
