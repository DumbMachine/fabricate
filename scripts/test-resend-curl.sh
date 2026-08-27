#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <fab binary>" >&2
  exit 2
fi

fab_bin=$1
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
report_dir=$(mktemp -d "${TMPDIR:-/tmp}/fabricate-resend-curl.XXXXXX")
trap 'rm -rf "$report_dir"' EXIT
report_path="$report_dir/resend.compatibility.json"
client="curl@$(curl --version | awk 'NR==1 {print $2}')"
commit=$(git -C "$repo_dir" rev-parse HEAD)

export FAB_COMPATIBILITY_REPORT="$report_path"
export FAB_COMPATIBILITY_COMMIT="$commit"
export FAB_COMPATIBILITY_CLIENT="$client"

"$fab_bin" run --environment "$repo_dir/environments/acme-resend.yaml" -- \
  "$repo_dir/resources/resend/conformance/curl.sh" direct
"$fab_bin" run --environment "$repo_dir/environments/acme-resend.yaml" --proxy -- \
  "$repo_dir/resources/resend/conformance/curl.sh" proxy

generated_dir="$repo_dir/packages/docs-content/resources/integrations/_generated"
mkdir -p "$generated_dir"
cp "$report_path" "$generated_dir/resend.compatibility.json"
