#!/bin/sh
# Install wdx from a release.
#
#   curl -fsSL https://github.com/srjn45/warden/releases/latest/download/install.sh | sh
#
# Downloads the archive for this OS/arch, verifies its checksum and installs
# the binary into ~/.local/bin. No git or Go required.
#
# Env:
#   WDX_VERSION        release to install (e.g. 0.1.0); default: latest
#   WDX_DOWNLOAD_BASE  where releases live; default: GitHub Releases.
#                      Local builds use file://<repo>/dist (see `make install`).
#   WDX_BIN_DIR        install directory; default: ~/.local/bin
#
# Flags: --uninstall
set -eu

NAME=wdx
REPO=srjn45/warden
GITHUB_BASE="https://github.com/$REPO/releases/download"
BASE="${WDX_DOWNLOAD_BASE:-$GITHUB_BASE}"
BIN_DIR="${WDX_BIN_DIR:-$HOME/.local/bin}"
TARGET="$BIN_DIR/$NAME"

info() { printf '==> %s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

case "${1:-}" in
	"") ;;
	--uninstall)
		rm -f "$TARGET"
		info "removed $TARGET"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

# --- platform -------------------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
	linux | darwin) ;;
	*) die "unsupported OS '$os' (linux and darwin only)" ;;
esac
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture '$arch' (amd64 and arm64 only)" ;;
esac

# --- version --------------------------------------------------------------
if [ -n "${WDX_VERSION:-}" ]; then
	tag="v${WDX_VERSION#v}"
elif [ "$BASE" = "$GITHUB_BASE" ]; then
	tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)" ||
		die "failed to resolve the latest release"
else
	tag="$(curl -fsSL "$BASE/LATEST")" || die "failed to read $BASE/LATEST"
fi
[ -n "$tag" ] || die "could not determine which version to install"
version="${tag#v}"

# --- download & verify ----------------------------------------------------
archive="${NAME}_${version}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/$NAME-install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

info "downloading $NAME $tag ($os/$arch)"
curl -fsSL "$BASE/$tag/$archive" -o "$tmp/$archive" || die "failed to download $BASE/$tag/$archive"
curl -fsSL "$BASE/$tag/checksums.txt" -o "$tmp/checksums.txt" || die "failed to download checksums.txt"

expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$expected" ] || die "no checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" "$NAME" || die "failed to extract $archive"

# --- install --------------------------------------------------------------
# Copy then rename so a running wdx is never replaced by a half-written file.
mkdir -p "$BIN_DIR"
cp "$tmp/$NAME" "$TARGET.tmp.$$"
chmod 0755 "$TARGET.tmp.$$"
mv -f "$TARGET.tmp.$$" "$TARGET"
info "installed $("$TARGET" version) -> $TARGET"

case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*)
		warn "$BIN_DIR is not on your PATH; add this to your shell profile:"
		printf '    export PATH="%s:$PATH"\n' "$BIN_DIR" >&2
		;;
esac
