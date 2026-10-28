#!/bin/sh
# Download the pinned git-cliff release for this machine into bin/ and
# verify it against the checksums below, recorded when the pin was set.
#
# Usage: scripts/fetch-git-cliff.sh <version> <target>
set -eu

VERSION="${1:?usage: fetch-git-cliff.sh <version> <target>}"
TARGET="${2:?usage: fetch-git-cliff.sh <version> <target>}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# The sha256 of each archive of $VERSION. Changing the pin means updating
# these from the release's own .sha512 files.
[ "$VERSION" = 2.14.2 ] || die "no checksums recorded for git-cliff $VERSION; add them to $0"
case "$(uname -s)/$(uname -m)" in
	Linux/x86_64 | Linux/amd64)
		triple=x86_64-unknown-linux-gnu
		sum=24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4 ;;
	Linux/aarch64 | Linux/arm64)
		triple=aarch64-unknown-linux-gnu
		sum=69e208f6e4baef3a0f9b759d5f44223aa9336b86c691072a3d0c98d35a816e69 ;;
	Darwin/x86_64)
		triple=x86_64-apple-darwin
		sum=4e647d2deb152aca1e987f225cf42b080b991dd98f3bce4027ef95446d982ac3 ;;
	Darwin/arm64)
		triple=aarch64-apple-darwin
		sum=a0be97e237d440d34c5e508ec21d070b2c85cbb8be48a46a56f9a4e8bd247f05 ;;
	*) die "no git-cliff release for $(uname -s)/$(uname -m)" ;;
esac

archive="git-cliff-$VERSION-$triple.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$archive" \
	"https://github.com/orhun/git-cliff/releases/download/v$VERSION/$archive" ||
	die "failed to download $archive"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
	got="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
[ "$got" = "$sum" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$(dirname "$TARGET")"
mv "$tmp/git-cliff-$VERSION/git-cliff" "$TARGET"
echo "installed git-cliff $VERSION -> $TARGET"
