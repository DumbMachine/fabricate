#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
checks="$root/checks.json"
actual="${FAB_EVAL_DUMP:-/tmp/fab-dump}"
reward="${1:-/logs/verifier/reward.txt}"
mkdir -p "$(dirname "$reward")"

if command -v fab >/dev/null 2>&1; then
  if fab diff --checks "$checks" --reward-file "$reward" "$actual"; then
    exit 0
  fi
  exit 1
fi

echo "fab is not on PATH; run: fab eval $root/environment.yaml --checks $checks -- ./agent" >&2
echo 0 > "$reward"
exit 1
