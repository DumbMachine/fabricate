#!/bin/sh
# Candidate for fab eval: non-interactive OpenCode. Transcript goes to stderr
# so the score on stdout stays a single JSON object.
set -eu

task_dir="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"

if ! command -v opencode >/dev/null 2>&1; then
	echo "error: opencode is not on PATH" >&2
	exit 1
fi

model="${OPENCODE_MODEL:-opencode/muse-spark-1.3-contributor-free}"

exec opencode run --auto --pure \
	-m "$model" \
	--dir "$task_dir" \
	-f "$task_dir/instruction.md" \
	-- "$(cat "$task_dir/agent-prompt.md")" >&2
