#!/bin/sh
# fabricate installer — downloads the latest `fab` binary for this OS/arch
# from GitHub Releases and puts it on your PATH. No build step, no Docker
# image to build: infrastructure engine images auto-pull on `fab create`.
#
#   curl -fsSL https://raw.githubusercontent.com/DumbMachine/fabricate/main/install.sh | sh
#
# Optional environment overrides:
#   FAB_INSTALL_DIR       where to install (default: /usr/local/bin if
#                         writable, else $HOME/.local/bin)
#   FAB_VERSION           release tag to install (default: latest, resolved
#                         to the current GitHub release tag)
#   FAB_INSTALL_BASE_URL  base URL for release assets (advanced / testing)
set -eu

# Canonical owner casing: github.com redirects case-insensitively, but
# raw.githubusercontent.com (used for `curl | sh`) is case-sensitive.
REPO="DumbMachine/fabricate"
BINARY="fab"
CHECKSUMS="checksums.txt"

info() { printf '%s\n' "$*" >&2; }
step() { printf '==> %s\n' "$1" >&2; }
err() {
	printf 'install.sh: error: %s\n' "$*" >&2
	exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# Progress on a TTY; stay quiet in CI / redirected stderr.
# GitHub asset CDNs occasionally drop; retry a few times.
download() {
	url=$1
	dest=$2
	if have curl; then
		if [ -t 2 ]; then
			curl -fL --retry 3 --retry-delay 2 --progress-bar "$url" -o "$dest"
		else
			curl -fsSL --retry 3 --retry-delay 2 "$url" -o "$dest"
		fi
	elif have wget; then
		if [ -t 2 ]; then
			wget -t 3 -O "$dest" "$url"
		else
			wget -t 3 -qO "$dest" "$url"
		fi
	else
		err "need curl or wget on PATH"
	fi
}

download_quiet() {
	url=$1
	dest=$2
	if have curl; then
		curl -fsSL --retry 3 --retry-delay 2 "$url" -o "$dest"
	elif have wget; then
		wget -t 3 -qO "$dest" "$url"
	else
		err "need curl or wget on PATH"
	fi
}

# Pull a 64-char hex digest out of GNU sha256sum, BSD shasum, or OpenSSL.
# Prints the hash on stdout; returns non-zero if it cannot be parsed.
file_sha256() {
	out=""
	if have shasum; then
		out=$(shasum -a 256 "$1")
	elif have sha256sum; then
		out=$(sha256sum "$1")
	elif have openssl; then
		out=$(openssl dgst -sha256 "$1")
	else
		return 1
	fi
	printf '%s\n' "$out" | tr -d '\r' | awk '{
		for (i = 1; i <= NF; i++) {
			if (length($i) == 64 && $i ~ /^[0-9a-fA-F]+$/) { print tolower($i); exit 0 }
		}
		exit 1
	}'
}

# Follow GitHub's /releases/latest redirect (no API, no rate limit).
resolve_latest_tag() {
	latest_url="https://github.com/${REPO}/releases/latest"
	loc=""
	if have curl; then
		loc=$(curl -fsSI "$latest_url" | awk 'tolower($1) == "location:" { print $2; exit }' | tr -d '\r')
	elif have wget; then
		loc=$(wget -S --spider --max-redirect=0 "$latest_url" 2>&1 | awk 'tolower($1) == "location:" { print $2; exit }' | tr -d '\r' || true)
	else
		err "need curl or wget on PATH"
	fi
	tag=${loc##*/}
	case "$tag" in
	v[0-9]*) printf '%s\n' "$tag" ;;
	*) err "could not resolve latest release from GitHub (got '${loc:-empty}')" ;;
	esac
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

# Under Rosetta, uname reports x86_64; install the native Apple Silicon build.
if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
	if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = "1" ]; then
		arch="arm64"
	fi
fi

case "${os}_${arch}" in
darwin_arm64) platform_label="macOS (Apple Silicon)" ;;
darwin_amd64) platform_label="macOS (Intel)" ;;
linux_arm64) platform_label="Linux (ARM64)" ;;
linux_amd64) platform_label="Linux (x86_64)" ;;
*) platform_label="${os}/${arch}" ;;
esac

asset="fabricate_${os}_${arch}.tar.gz"

# --- choose install dir ---
if [ -z "${FAB_INSTALL_DIR:-}" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		FAB_INSTALL_DIR="/usr/local/bin"
	else
		FAB_INSTALL_DIR="$HOME/.local/bin"
	fi
fi
mkdir -p "$FAB_INSTALL_DIR" || err "cannot create $FAB_INSTALL_DIR"

if [ -e "$FAB_INSTALL_DIR/$BINARY" ]; then
	step "Updating fab"
else
	step "Installing fab"
fi
step "Detected platform: ${platform_label}"

# --- resolve download URL ---
: "${FAB_VERSION:=latest}"
case "$FAB_VERSION" in
latest) ;;
v[0-9]*) ;;
*) FAB_VERSION="v${FAB_VERSION}" ;;
esac

if [ -n "${FAB_INSTALL_BASE_URL:-}" ]; then
	version_label=$FAB_VERSION
	url="${FAB_INSTALL_BASE_URL}/${asset}"
	checksums_url="${FAB_INSTALL_BASE_URL}/${CHECKSUMS}"
	require_checksums=0
else
	if [ "$FAB_VERSION" = "latest" ]; then
		FAB_VERSION=$(resolve_latest_tag)
	fi
	version_label=$FAB_VERSION
	url="https://github.com/${REPO}/releases/download/${FAB_VERSION}/${asset}"
	checksums_url="https://github.com/${REPO}/releases/download/${FAB_VERSION}/${CHECKSUMS}"
	require_checksums=1
fi

step "Resolved version: ${version_label}"

# --- download + verify + extract ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

step "Downloading ${asset}"
download "$url" "$tmp/$asset" || err "download failed: $url"

step "Verifying checksum"
if download_quiet "$checksums_url" "$tmp/$CHECKSUMS"; then
	expected=$(tr -d '\r' <"$tmp/$CHECKSUMS" | awk -v f="$asset" '$2 == f || $2 == ("*" f) { print tolower($1); exit }')
	[ -n "$expected" ] || err "${CHECKSUMS} has no entry for ${asset}"
	got=$(file_sha256 "$tmp/$asset") || err "could not hash ${asset} (need shasum, sha256sum, or openssl)"
	[ "$expected" = "$got" ] || err "checksum mismatch for ${asset}
  expected: ${expected}
  got:      ${got}"
elif [ "$require_checksums" -eq 1 ]; then
	err "could not download ${CHECKSUMS} for ${FAB_VERSION}"
else
	info "warning: skipping checksum verification (no ${CHECKSUMS} at ${FAB_INSTALL_BASE_URL})"
fi

tar -xzf "$tmp/$asset" -C "$tmp" || err "could not extract $asset"
[ -f "$tmp/$BINARY" ] || err "archive did not contain '$BINARY'"

# install(1) sets mode atomically; fall back to mv+chmod if it's absent.
if have install; then
	install -m 0755 "$tmp/$BINARY" "$FAB_INSTALL_DIR/$BINARY" || err "could not install to $FAB_INSTALL_DIR (try FAB_INSTALL_DIR=\$HOME/.local/bin)"
else
	mv "$tmp/$BINARY" "$FAB_INSTALL_DIR/$BINARY" || err "could not install to $FAB_INSTALL_DIR"
	chmod 0755 "$FAB_INSTALL_DIR/$BINARY"
fi

step "Installed ${BINARY} ${version_label} → ${FAB_INSTALL_DIR}/${BINARY}"

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
info "get started:"
info "  fab run acme-gmail --proxy -- <command>"
