#!/bin/sh
# Download the pinned release of a tool for this machine into bin/ and
# verify it against the checksums below, recorded when the pin was set.
#
# Usage: scripts/fetch-release.sh <tool> <version> <target>
set -eu

usage="usage: fetch-release.sh <tool> <version> <target>"
TOOL="${1:?$usage}"
VERSION="${2:?$usage}"
TARGET="${3:?$usage}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

platform="$(uname -s)/$(uname -m)"
case "$platform" in
	Linux/amd64) platform=Linux/x86_64 ;;
	Linux/arm64) platform=Linux/aarch64 ;;
esac

# Per tool: where its archives are, which one is this machine's and the
# binary's path inside it, and the sha256 of each archive of $VERSION.
# Changing a pin means updating these from the release's digests.
case "$TOOL@$VERSION" in
	git-cliff@2.14.2)
		case "$platform" in
			Linux/x86_64) triple=x86_64-unknown-linux-gnu sum=24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4 ;;
			Linux/aarch64) triple=aarch64-unknown-linux-gnu sum=69e208f6e4baef3a0f9b759d5f44223aa9336b86c691072a3d0c98d35a816e69 ;;
			Darwin/x86_64) triple=x86_64-apple-darwin sum=4e647d2deb152aca1e987f225cf42b080b991dd98f3bce4027ef95446d982ac3 ;;
			Darwin/arm64) triple=aarch64-apple-darwin sum=a0be97e237d440d34c5e508ec21d070b2c85cbb8be48a46a56f9a4e8bd247f05 ;;
			*) die "no git-cliff release for $platform" ;;
		esac
		archive="git-cliff-$VERSION-$triple.tar.gz"
		url="https://github.com/orhun/git-cliff/releases/download/v$VERSION/$archive"
		binary="git-cliff-$VERSION/git-cliff" ;;
	shellcheck@0.11.0)
		case "$platform" in
			Linux/x86_64) arch=linux.x86_64 sum=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6 ;;
			Linux/aarch64) arch=linux.aarch64 sum=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc ;;
			Darwin/x86_64) arch=darwin.x86_64 sum=c2c15e08df0e8fbc374c335b230a7ee958c313fa5714817a59aa59f1aa594f51 ;;
			Darwin/arm64) arch=darwin.aarch64 sum=339b930feb1ea764467013cc1f72d09cd6b869ebf1013296ba9055ab2ffbd26f ;;
			*) die "no shellcheck release for $platform" ;;
		esac
		archive="shellcheck-v$VERSION.$arch.tar.gz"
		url="https://github.com/koalaman/shellcheck/releases/download/v$VERSION/$archive"
		binary="shellcheck-v$VERSION/shellcheck" ;;
	*) die "no checksums recorded for $TOOL $VERSION; add them to $0" ;;
esac

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$archive" "$url" || die "failed to download $archive"
[ "$(sha256 "$tmp/$archive")" = "$sum" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$(dirname "$TARGET")"
mv "$tmp/$binary" "$TARGET"
echo "installed $TOOL $VERSION -> $TARGET"
