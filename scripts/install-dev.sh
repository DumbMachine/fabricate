#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -P -- "$script_dir/.." && pwd)
source_path="$repo_root/scripts/fab-dev"
install_dir=${FAB_INSTALL_DIR:-"$HOME/bin"}
installed_path="$install_dir/fab-dev"

mkdir -p "$install_dir"
if [ -e "$installed_path" ] && [ ! -L "$installed_path" ]; then
	echo "install-dev: refusing to replace non-symlink $installed_path" >&2
	exit 1
fi

ln -sfn "$source_path" "$installed_path"
echo "fab-dev → $source_path"

case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) echo "install-dev: add $install_dir to PATH to invoke fab-dev directly" >&2 ;;
esac
