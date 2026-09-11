#!/bin/sh
# Score a non-interactive OpenCode run against the Gmail trash gold dump.
# Requires opencode on PATH. Override the pinned model with OPENCODE_MODEL.
set -eu

. "$(CDPATH= cd -- "$(dirname "$0")" && pwd)/common.sh"

cd "$repo_root"
exec "$fab_bin" eval acme-gmail --proxy \
	--expected "$task_dir/expected" \
	--output json \
	-- "$task_dir/run-opencode.sh"
