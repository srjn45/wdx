#!/bin/sh
# Download the pinned shellcheck release for this machine into bin/ and
# verify it against the checksums below, recorded when the pin was set.
#
# Usage: scripts/fetch-shellcheck.sh <version> <target>
set -eu

VERSION="${1:?usage: fetch-shellcheck.sh <version> <target>}"
TARGET="${2:?usage: fetch-shellcheck.sh <version> <target>}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# The sha256 of each archive of $VERSION. Changing the pin means updating
# these from the digests GitHub shows on the release.
[ "$VERSION" = 0.11.0 ] || die "no checksums recorded for shellcheck $VERSION; add them to $0"
case "$(uname -s)/$(uname -m)" in
	Linux/x86_64 | Linux/amd64)
		platform=linux.x86_64
		sum=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6 ;;
	Linux/aarch64 | Linux/arm64)
		platform=linux.aarch64
		sum=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc ;;
	Darwin/x86_64)
		platform=darwin.x86_64
		sum=c2c15e08df0e8fbc374c335b230a7ee958c313fa5714817a59aa59f1aa594f51 ;;
	Darwin/arm64)
		platform=darwin.aarch64
		sum=339b930feb1ea764467013cc1f72d09cd6b869ebf1013296ba9055ab2ffbd26f ;;
	*) die "no shellcheck release for $(uname -s)/$(uname -m)" ;;
esac

archive="shellcheck-v$VERSION.$platform.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$archive" \
	"https://github.com/koalaman/shellcheck/releases/download/v$VERSION/$archive" ||
	die "failed to download $archive"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
	got="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
[ "$got" = "$sum" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$(dirname "$TARGET")"
mv "$tmp/shellcheck-v$VERSION/shellcheck" "$TARGET"
echo "installed shellcheck $VERSION -> $TARGET"
