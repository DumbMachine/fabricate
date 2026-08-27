#!/usr/bin/env bash
# Black-box resource conformance. Adding a resource means adding
# resources/<id>/conformance/curl.sh and/or sdk.json plus
# environments/acme-<id>.yaml. This script runs every client that exists.
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

list_resources() {
  local dir id
  for dir in "$repo_dir"/resources/*/conformance; do
    [ -d "$dir" ] || continue
    id=$(basename "$(dirname "$dir")")
    if [ -f "$dir/sdk.json" ] || [ -f "$dir/curl.sh" ]; then
      printf '%s\n' "$id"
    fi
  done | sort
}

changed_since() {
  local ref=$1
  if [ -z "$ref" ] || [ "$(printf '%s' "$ref" | tr -d '0')" = "" ]; then
    list_resources
    return
  fi
  local changed shared=false line id
  changed=$(git -C "$repo_dir" diff --name-only --no-renames --relative "$ref"...HEAD) || {
    echo "test: git diff $ref...HEAD failed" >&2
    exit 2
  }
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
      environments/*|environment/*|engine/http/*|httpresource/*|requestlog/*|scenario/*|cli/run.go|scripts/test.sh|Makefile|.github/workflows/ci.yml)
        shared=true
        break
        ;;
    esac
  done <<EOF
$changed
EOF
  if [ "$shared" = true ]; then
    list_resources
    return
  fi
  while IFS= read -r id; do
    while IFS= read -r line; do
      case "$line" in
        resources/"$id"|resources/"$id"/*)
          printf '%s\n' "$id"
          break
          ;;
      esac
    done <<EOF
$changed
EOF
  done < <(list_resources)
}

if [ "${1:-}" = "--list" ]; then
  list_resources
  exit 0
fi

if [ "${1:-}" = "--changed-since" ]; then
  changed_since "${2:-}"
  exit 0
fi

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <fab binary> <resource> [resource...]" >&2
  echo "       $0 --list" >&2
  echo "       $0 --changed-since <git-ref>" >&2
  exit 2
fi

fab_bin=$1
shift

run_curl() {
  local resource=$1 env=$2 report=$3
  local client commit
  client="curl@$(curl --version | awk 'NR==1 {split($2, a, "."); print a[1]}')"
  commit=$(git -C "$repo_dir" rev-parse HEAD)
  export FAB_COMPATIBILITY_REPORT="$report"
  export FAB_COMPATIBILITY_COMMIT="$commit"
  export FAB_COMPATIBILITY_CLIENT="$client"
  "$fab_bin" run --environment "$env" -- \
    "$repo_dir/resources/$resource/conformance/curl.sh" direct
  "$fab_bin" run --environment "$env" --proxy -- \
    "$repo_dir/resources/$resource/conformance/curl.sh" proxy
}

run_sdk() {
  local resource=$1 env=$2 report=$3 sdk=$4 work=$5
  local package entry sdk_version
  local sdk_meta
  sdk_meta=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d["package"]+"\t"+d["entry"])' "$sdk")
  package=${sdk_meta%%$'\t'*}
  entry=${sdk_meta#*$'\t'}
  case "$entry" in
    */*|.*)
      echo "test: $resource sdk.json entry must be a basename" >&2
      exit 2
      ;;
  esac
  sdk_version=${package##*@}
  # Official SDKs are a conformance prerequisite, never a Fabricate runtime
  # or development dependency. Install outside the workspace.
  npm install --prefix "$work" --no-package-lock --ignore-scripts --no-audit --no-fund "$package"
  cp "$repo_dir/resources/$resource/conformance/$entry" "$work/$entry"
  FAB_COMPATIBILITY_REPORT="$report" \
  FAB_COMPATIBILITY_SDK_VERSION="$sdk_version" \
  FAB_COMPATIBILITY_COMMIT="$(git -C "$repo_dir" rev-parse HEAD)" \
    "$fab_bin" run --environment "$env" -- \
    node "$work/$entry" direct
  FAB_COMPATIBILITY_REPORT="$report" \
  FAB_COMPATIBILITY_SDK_VERSION="$sdk_version" \
  FAB_COMPATIBILITY_COMMIT="$(git -C "$repo_dir" rev-parse HEAD)" \
    "$fab_bin" run --environment "$env" --proxy -- \
    node "$work/$entry" proxy
}

run_one() {
  local resource=$1
  local conformance env work curl_report sdk_report published
  if [[ ! "$resource" =~ ^[a-z][a-z0-9-]*$ ]]; then
    echo "test: invalid resource $resource" >&2
    exit 2
  fi
  conformance="$repo_dir/resources/$resource/conformance"
  env="$repo_dir/environments/acme-$resource.yaml"
  if [ ! -d "$conformance" ]; then
    echo "test: missing $conformance" >&2
    exit 2
  fi
  if [ ! -f "$env" ]; then
    echo "test: missing $env" >&2
    exit 2
  fi
  work=$(mktemp -d "${TMPDIR:-/tmp}/fabricate-$resource-conformance.XXXXXX")
  # shellcheck disable=SC2064
  trap "rm -rf $(printf '%q' "$work")" EXIT
  curl_report="$work/curl.compatibility.json"
  sdk_report="$work/sdk.compatibility.json"
  published=""
  if [ -f "$conformance/curl.sh" ]; then
    echo "test: $resource curl" >&2
    run_curl "$resource" "$env" "$curl_report"
    published=$curl_report
  fi
  if [ -f "$conformance/sdk.json" ]; then
    echo "test: $resource sdk" >&2
    mkdir -p "$work/npm"
    run_sdk "$resource" "$env" "$sdk_report" "$conformance/sdk.json" "$work/npm"
    published=$sdk_report
  fi
  if [ -z "$published" ]; then
    echo "test: $resource needs conformance/curl.sh, conformance/sdk.json, or both" >&2
    exit 2
  fi
  if [ -n "${DOCSEXAMPLES_BIN:-}" ]; then
    "$DOCSEXAMPLES_BIN" install-compat --repo "$repo_dir" --from "$published" --resource "$resource"
  else
    go run ./cmd/docsexamples install-compat --repo "$repo_dir" --from "$published" --resource "$resource"
  fi
  rm -rf "$work"
  trap - EXIT
}

cd "$repo_dir"
for resource in "$@"; do
  run_one "$resource"
done
