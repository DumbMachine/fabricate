#!/bin/sh
set -eu

. "$(CDPATH= cd -- "$(dirname "$0")" && pwd)/common.sh"

cd "$repo_root"
exec "$fab_bin" gold acme-gmail \
	--dump "$task_dir/expected" \
	--output json \
	-- "$task_dir/solution/solve.sh"
