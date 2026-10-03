#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_CANNY_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_CANNY_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_CANNY_URL:-}" ]; then
    echo "FAB_CANNY_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_CANNY_URL%/}
  curl_opts=(-sS --fail)
else
  base="https://canny.io"
  curl_opts=(-sS --fail --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

posts_before=$(read_json -X POST "$base/api/v1/posts/list" -d '{"boardID":"board-acme-app"}')
echo "$posts_before" | jq -e '.posts | length == 2' >/dev/null
echo "$posts_before" | jq -e '[.posts[].title] | index("CSV export downloads an empty file")' >/dev/null
echo "$posts_before" | jq -e '[.posts[] | select(.title == "Refund duplicate charge") | .status] == ["in progress"]' >/dev/null
echo "$posts_before" | jq -e '[.posts[].details] | any(test("INV-4812"))' >/dev/null

created=$(read_json -X POST "$base/api/v1/posts/create" -d '{"authorID":"user-priya","boardID":"board-acme-app","title":"Show invoice numbers on export","details":"Include INV-4812."}')
created_id=$(echo "$created" | jq -r '.id')
test -n "$created_id"
test "$created_id" != "null"

posts_after=$(read_json -X POST "$base/api/v1/posts/list" -d '{"boardID":"board-acme-app","limit":10}')
echo "$posts_after" | jq -e --arg id "$created_id" '[.posts[].id] | index($id)' >/dev/null
echo "$posts_after" | jq -e '.posts | length == 3' >/dev/null

retrieved=$(read_json -X POST "$base/api/v1/posts/retrieve" -d "$(jq -nc --arg id "$created_id" '{id:$id}')")
echo "$retrieved" | jq -e --arg id "$created_id" '.id == $id' >/dev/null
echo "$retrieved" | jq -e '.title == "Show invoice numbers on export"' >/dev/null

python3 - "$mode" <<'PY'
import json, os, sys
from datetime import datetime, timezone

mode = sys.argv[1]
mode_report = {
    "messagesAfter": 3,
    "messagesBefore": 2,
    "operations": {"read": "passed", "write": "passed", "persistence": "passed"},
    "status": "passed",
}
path = os.environ.get("FAB_COMPATIBILITY_REPORT")
if path:
    report = {
        "api": "Canny API v1",
        "environment": {
            "label": "Acme Canny",
            "manifest": "environments/acme-canny.yaml",
            "messages": 2,
        },
        "integration": "canny",
        "modes": {},
        "operationLabels": {
            "read": "List posts",
            "write": "Create post",
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
