# Shared paths for the Gmail trash eval scripts. Sourced, not executed.
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
