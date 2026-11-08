#!/bin/sh
# Build release archives the same way the GitHub release will publish them:
#
#   dist/v<version>/wdx_<version>_<os>_<arch>.tar.gz       (wdx, LICENSE, NOTICE,
#                                                          THIRD-PARTY-NOTICES.md)
#   dist/v<version>/wdx-hub_<version>_<os>_<arch>.tar.gz   (the same, with wdx-hub)
#   dist/v<version>/checksums.txt
#   dist/LATEST-dev                  (contains v<version>)
#   dist/LATEST                      (the same, for a stable version only)
#
# A version with a pre-release suffix (0.2.0-dev.3) is a dev release; the
# installer's channels read these files. DIST sets another directory than
# dist, PLATFORMS fewer platforms (os/arch, space-separated).
#
# Usage: scripts/dist.sh <version> <commit>
set -eu

# The programs, each built from cmd/<name> into an archive of its own per
# platform; install.sh and wdx update fetch only wdx's.
PROGRAMS="wdx wdx-hub"
VERSION="${1:?usage: dist.sh <version> <commit>}"
VERSION="${VERSION#v}"
COMMIT="${2:-none}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
DIST="${DIST:-dist}"

PKG=github.com/srjn45/wdx/internal/identity
LDFLAGS="-s -w -X $PKG.Version=$VERSION -X $PKG.Commit=$COMMIT"

cd "$(dirname "$0")/.."
OUT="$DIST/v$VERSION"
rm -rf "$OUT"
mkdir -p "$OUT"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

for p in $PLATFORMS; do
	os="${p%/*}"
	arch="${p#*/}"
	for name in $PROGRAMS; do
		echo "building $name for $os/$arch"
		dir="$STAGE/$name-$os-$arch"
		mkdir -p "$dir"
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
			go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$name" "./cmd/$name"
		cp LICENSE NOTICE THIRD-PARTY-NOTICES.md "$dir/"
		tar -czf "$OUT/${name}_${VERSION}_${os}_${arch}.tar.gz" -C "$dir" "$name" LICENSE NOTICE THIRD-PARTY-NOTICES.md
	done
done

if command -v sha256sum >/dev/null 2>&1; then
	(cd "$OUT" && sha256sum ./*.tar.gz | sed 's| \./| |' > checksums.txt)
else
	(cd "$OUT" && shasum -a 256 ./*.tar.gz | sed 's| \./| |' > checksums.txt)
fi

echo "v$VERSION" > "$DIST/LATEST-dev"
case "$VERSION" in
	*-*) ;;
	*) echo "v$VERSION" > "$DIST/LATEST" ;;
esac
echo "release v$VERSION -> $OUT"
