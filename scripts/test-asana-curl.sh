#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <fab binary>" >&2
  exit 2
fi

fab_bin=$1
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
report_dir=$(mktemp -d "${TMPDIR:-/tmp}/fabricate-asana-curl.XXXXXX")
trap 'rm -rf "$report_dir"' EXIT
report_path="$report_dir/asana.compatibility.json"
client="curl@$(curl --version | awk 'NR==1 {print $2}')"

run_mode() {
  local mode=$1
  local extra=()
  if [ "$mode" = "proxy" ]; then
    extra=(--proxy)
  fi
  FAB_COMPATIBILITY_REPORT="$report_path" \
  FAB_COMPATIBILITY_COMMIT="$(git -C "$repo_dir" rev-parse HEAD)" \
    "$fab_bin" run --environment "$repo_dir/environments/acme-asana.yaml" "${extra[@]}" -- \
    bash -c '
      set -euo pipefail
      mode=$0
      report_path=$1
      client=$2
      commit=$3
      repo_dir=$4
      result=$("$repo_dir/resources/asana/conformance/curl.sh" "$mode")
      python3 - "$report_path" "$mode" "$result" "$client" "$commit" <<'"'"'PY'"'"'
import json, sys
path, mode, result, client, commit = sys.argv[1:6]
mode_report = json.loads(result)
try:
    with open(path, encoding="utf-8") as fh:
        report = json.load(fh)
except FileNotFoundError:
    report = {
        "api": "Asana REST API 1.0",
        "environment": {"label": "Acme Asana", "manifest": "environments/acme-asana.yaml", "messages": 5},
        "integration": "asana",
        "modes": {},
        "operationLabels": {
            "read": "Read sprint board",
            "write": "Complete task and comment",
            "persistence": "Confirm persistence",
        },
        "verification": {
            "kind": "curl",
            "label": "HTTP client",
            "client": client,
            "title": "HTTP API verification",
        },
    }
report["modes"][mode] = mode_report
report["testedAt"] = __import__("datetime").datetime.now(__import__("datetime").timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"
report["testedCommit"] = commit
report["verification"]["client"] = client
with open(path, "w", encoding="utf-8") as fh:
    json.dump(report, fh, indent=2)
    fh.write("\n")
PY
    ' "$mode" "$report_path" "$client" "$(git -C "$repo_dir" rev-parse HEAD)" "$repo_dir"
}

run_mode direct
run_mode proxy

generated_dir="$repo_dir/packages/docs-content/resources/integrations/_generated"
mkdir -p "$generated_dir"
cp "$report_path" "$generated_dir/asana.compatibility.json"
