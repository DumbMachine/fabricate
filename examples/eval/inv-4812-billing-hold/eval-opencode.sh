#!/bin/sh
# Score a non-interactive OpenCode run against INV-4812 field checks.
# Requires opencode on PATH. Override the pinned model with OPENCODE_MODEL.
set -eu

task_dir="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
repo_root="$(CDPATH= cd -- "$task_dir/../../.." && pwd)"

if [ -n "${FAB:-}" ]; then
	fab_bin=$FAB
elif [ -x "$repo_root/scripts/fab-dev" ]; then
	fab_bin=$repo_root/scripts/fab-dev
elif command -v fab >/dev/null 2>&1; then
	fab_bin=fab
else
	echo "error: fab is not on PATH; set FAB or run from a Fabricate checkout" >&2
	exit 1
fi

cd "$repo_root"
exec "$fab_bin" eval "$task_dir/environment.yaml" --proxy \
	--checks "$task_dir/checks.json" \
	--output json \
	-- "$task_dir/run-opencode.sh"
