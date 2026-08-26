#!/usr/bin/env bash
set -euo pipefail

mode=${1:-direct}
if [ "$mode" != "direct" ] && [ "$mode" != "proxy" ]; then
  echo "usage: $0 <direct|proxy>" >&2
  exit 2
fi

token=${FAB_ASANA_TOKEN:-}
if [ -z "$token" ]; then
  echo "FAB_ASANA_TOKEN is required" >&2
  exit 1
fi

if [ "$mode" = "direct" ]; then
  if [ -z "${FAB_ASANA_URL:-}" ]; then
    echo "FAB_ASANA_URL is required in direct mode" >&2
    exit 1
  fi
  base=${FAB_ASANA_URL%/}
  curl_opts=(-sS)
else
  base="https://app.asana.com/api/1.0"
  curl_opts=(-sS --proxy "${HTTPS_PROXY:?HTTPS_PROXY is required in proxy mode}")
  if [ -n "${SSL_CERT_FILE:-}" ]; then
    curl_opts+=(--cacert "$SSL_CERT_FILE")
  fi
fi

auth=(-H "Authorization: Bearer $token" -H "Content-Type: application/json")

read_json() {
  curl "${curl_opts[@]}" "${auth[@]}" "$@"
}

me=$(read_json "$base/users/me")
echo "$me" | jq -e '.data.gid == "user-val"' >/dev/null
echo "$me" | jq -e '.data.email == "val@acme.example"' >/dev/null

tasks_before=$(read_json "$base/projects/proj-checkout/tasks")
echo "$tasks_before" | jq -e '.data | length == 5' >/dev/null
echo "$tasks_before" | jq -e '[.data[].gid] | index("task-double-charge")' >/dev/null

completed=$(read_json -X PUT "$base/tasks/task-double-charge" -d '{"data":{"completed":true}}')
echo "$completed" | jq -e '.data.completed == true' >/dev/null

comment=$(read_json -X POST "$base/tasks/task-double-charge/stories" -d '{"data":{"text":"Refund issued as rf_4812."}}')
echo "$comment" | jq -e '.data.text | test("Refund issued")' >/dev/null

stories=$(read_json "$base/tasks/task-double-charge/stories")
echo "$stories" | jq -e '[.data[].text] | any(test("Refund issued"))' >/dev/null

created=$(read_json -X POST "$base/tasks" -d '{"data":{"name":"Follow up with Northwind","projects":["proj-checkout"],"assignee":"me"}}')
created_gid=$(echo "$created" | jq -r '.data.gid')
test -n "$created_gid"

tasks_after=$(read_json "$base/projects/proj-checkout/tasks")
echo "$tasks_after" | jq -e --arg gid "$created_gid" '[.data[].gid] | index($gid)' >/dev/null
echo "$tasks_after" | jq -e '.data | length == 6' >/dev/null

operations='{"read":"passed","write":"passed","persistence":"passed"}'
mode_report=$(jq -n --argjson operations "$operations" '{operations:$operations,status:"passed",messagesBefore:5,messagesAfter:6}')
echo "$mode_report"
