#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <fab binary>" >&2
  exit 2
fi

fab_bin=$1
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
sdk_dir=$(mktemp -d "${TMPDIR:-/tmp}/fabricate-gmail-sdk.XXXXXX")
trap 'rm -rf "$sdk_dir"' EXIT
report_path="$sdk_dir/gmail.compatibility.json"
sdk_version=144.0.0

# This is intentionally outside the workspace: Google APIs is a conformance
# prerequisite, never a Fabricate runtime or development dependency.
npm install --prefix "$sdk_dir" --no-package-lock --ignore-scripts --no-audit --no-fund "googleapis@$sdk_version"
cp "$repo_dir/resources/gmail/conformance/googleapis.mjs" "$sdk_dir/googleapis.mjs"

FAB_COMPATIBILITY_REPORT="$report_path" \
FAB_COMPATIBILITY_SDK_VERSION="$sdk_version" \
FAB_COMPATIBILITY_COMMIT="$(git -C "$repo_dir" rev-parse HEAD)" \
  "$fab_bin" run --environment "$repo_dir/environments/acme-gmail.yaml" -- \
  node "$sdk_dir/googleapis.mjs" direct
FAB_COMPATIBILITY_REPORT="$report_path" \
FAB_COMPATIBILITY_SDK_VERSION="$sdk_version" \
FAB_COMPATIBILITY_COMMIT="$(git -C "$repo_dir" rev-parse HEAD)" \
  "$fab_bin" run --environment "$repo_dir/environments/acme-gmail.yaml" --proxy -- \
  node "$sdk_dir/googleapis.mjs" proxy

generated_dir="$repo_dir/packages/docs-content/resources/integrations/_generated"
mkdir -p "$generated_dir"
cp "$report_path" "$generated_dir/gmail.compatibility.json"
