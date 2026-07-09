#!/bin/sh
# fabricate installer — downloads the latest `fab` binary for this OS/arch
# from GitHub Releases and puts it on your PATH. No build step, no Docker
# image to build: engine images auto-pull on the first `fab create`.
#
#   curl -fsSL https://raw.githubusercontent.com/dumbmachine/fabricate/main/install.sh | sh
#
# Optional environment overrides:
#   FAB_INSTALL_DIR       where to install (default: /usr/local/bin if
#                         writable, else $HOME/.local/bin)
#   FAB_VERSION           release tag to install (default: latest)
#   FAB_INSTALL_BASE_URL  base URL for release assets (advanced / testing)
set -eu

REPO="dumbmachine/fabricate"
BINARY="fab"

info() { printf '%s\n' "$*" >&2; }
err() {
	printf 'install.sh: error: %s\n' "$*" >&2
	exit 1
}

# --- detect platform (must match .goreleaser.yaml archive names) ---
os=$(uname -s)
case "$os" in
Darwin) os="darwin" ;;
Linux) os="linux" ;;
*) err "unsupported OS '$os' — fab ships macOS and Linux binaries; build from source instead" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch="amd64" ;;
arm64 | aarch64) arch="arm64" ;;
*) err "unsupported architecture '$arch'" ;;
esac

asset="fabricate_${os}_${arch}.tar.gz"

# --- resolve download URL ---
: "${FAB_VERSION:=latest}"
if [ -n "${FAB_INSTALL_BASE_URL:-}" ]; then
	url="${FAB_INSTALL_BASE_URL}/${asset}"
elif [ "$FAB_VERSION" = "latest" ]; then
	url="https://github.com/${REPO}/releases/latest/download/${asset}"
else
	url="https://github.com/${REPO}/releases/download/${FAB_VERSION}/${asset}"
fi

# --- choose install dir ---
if [ -z "${FAB_INSTALL_DIR:-}" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		FAB_INSTALL_DIR="/usr/local/bin"
	else
		FAB_INSTALL_DIR="$HOME/.local/bin"
	fi
fi
mkdir -p "$FAB_INSTALL_DIR" || err "cannot create $FAB_INSTALL_DIR"

# --- download + extract ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "fabricate: downloading ${asset} (${FAB_VERSION})…"
if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$url" -o "$tmp/$asset" || err "download failed: $url"
elif command -v wget >/dev/null 2>&1; then
	wget -qO "$tmp/$asset" "$url" || err "download failed: $url"
else
	err "need curl or wget on PATH"
fi

tar -xzf "$tmp/$asset" -C "$tmp" || err "could not extract $asset"
[ -f "$tmp/$BINARY" ] || err "archive did not contain '$BINARY'"

# install(1) sets mode atomically; fall back to mv+chmod if it's absent.
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$tmp/$BINARY" "$FAB_INSTALL_DIR/$BINARY" || err "could not install to $FAB_INSTALL_DIR (try FAB_INSTALL_DIR=\$HOME/.local/bin)"
else
	mv "$tmp/$BINARY" "$FAB_INSTALL_DIR/$BINARY" || err "could not install to $FAB_INSTALL_DIR"
	chmod 0755 "$FAB_INSTALL_DIR/$BINARY"
fi

info "fabricate: installed ${BINARY} → ${FAB_INSTALL_DIR}/${BINARY}"

# --- PATH hint ---
case ":$PATH:" in
*":$FAB_INSTALL_DIR:"*) ;;
*)
	info ""
	info "note: ${FAB_INSTALL_DIR} is not on your PATH — add it:"
	info "  export PATH=\"${FAB_INSTALL_DIR}:\$PATH\""
	;;
esac

info ""
info "get started (needs a running Docker daemon; engine images auto-pull):"
info "  fab create gmail -p support-inbox"
