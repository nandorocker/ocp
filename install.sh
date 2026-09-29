#!/bin/sh
# OCP installer.
#
#   curl -fsSL https://raw.githubusercontent.com/nandorocker/ocp/v0.1.0/install.sh | sh
#
# Environment:
#   OCP_VERSION    version tag to install (default: latest release)
#   OCP_INSTALL   install directory (default: $HOME/.local/bin)
set -eu

REPO="nandorocker/ocp"
VERSION="${OCP_VERSION:-}"
INSTALL_DIR="${OCP_INSTALL:-$HOME/.local/bin}"

say() { printf '%s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

detect_platform() {
	os=$(uname -s)
	case "$os" in
		Linux) os=linux ;;
		Darwin) os=darwin ;;
		*) die "unsupported operating system: $os" ;;
	esac

	arch=$(uname -m)
	case "$arch" in
		x86_64 | amd64) arch=amd64 ;;
		aarch64 | arm64) arch=arm64 ;;
		*) die "unsupported architecture: $arch" ;;
	esac

	printf '%s_%s' "$os" "$arch"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/^.*= *//'
	else
		die "no sha256 tool found (install sha256sum, shasum, or openssl)"
	fi
}

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

[ -n "$VERSION" ] || VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
[ -n "$VERSION" ] || die "could not determine the latest release"

case "$VERSION" in
	v*) VERSION=${VERSION#v} ;;
esac

platform=$(detect_platform)
asset="ocp_${VERSION}_${platform}.tar.gz"
base="https://github.com/$REPO/releases/download/v$VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading ocp $VERSION ($platform)"
curl -fsSL "$base/$asset" -o "$tmp/$asset" || die "download failed: $base/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

expected=$(awk -v a="$asset" '$2 == a || $2 == "*"a {print $1; exit}' "$tmp/checksums.txt")
[ -n "$expected" ] || die "no checksum published for $asset"
actual=$(sha256_of "$tmp/$asset")
[ "$expected" = "$actual" ] || die "checksum mismatch for $asset"

mkdir -p "$tmp/extract"
tar -xzf "$tmp/$asset" -C "$tmp/extract"
binary="$tmp/extract/ocp"
[ -f "$binary" ] || die "archive did not contain an ocp binary"
chmod +x "$binary"

mkdir -p "$INSTALL_DIR"
mv "$binary" "$INSTALL_DIR/ocp"
chmod +x "$INSTALL_DIR/ocp"

say "Installed $INSTALL_DIR/ocp ($("$INSTALL_DIR/ocp" version))"

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		say ""
		say "Add $INSTALL_DIR to your PATH:"
		say "    export PATH=\"$INSTALL_DIR:\$PATH\""
		;;
esac
