#!/bin/sh
# Build release archives the same way the GitHub release will publish them:
#
#   dist/v<version>/wdx_<version>_<os>_<arch>.tar.gz   (wdx, LICENSE, NOTICE,
#                                                      THIRD-PARTY-NOTICES.md)
#   dist/v<version>/checksums.txt
#   dist/LATEST-dev                  (contains v<version>)
#   dist/LATEST                      (the same, for a stable version only)
#
# A version with a pre-release suffix (0.2.0-dev.3) is a dev release; the
# installer's channels read these files.
#
# Usage: scripts/dist.sh <version> <commit>
set -eu

NAME=wdx
VERSION="${1:?usage: dist.sh <version> <commit>}"
VERSION="${VERSION#v}"
COMMIT="${2:-none}"
PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

PKG=github.com/srjn45/warden/internal/identity
LDFLAGS="-s -w -X $PKG.Version=$VERSION -X $PKG.Commit=$COMMIT"

cd "$(dirname "$0")/.."
OUT="dist/v$VERSION"
rm -rf "$OUT"
mkdir -p "$OUT"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

for p in $PLATFORMS; do
	os="${p%/*}"
	arch="${p#*/}"
	echo "building $os/$arch"
	mkdir -p "$STAGE/$os-$arch"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
		go build -trimpath -ldflags "$LDFLAGS" -o "$STAGE/$os-$arch/$NAME" ./cmd/wdx
	cp LICENSE NOTICE THIRD-PARTY-NOTICES.md "$STAGE/$os-$arch/"
	tar -czf "$OUT/${NAME}_${VERSION}_${os}_${arch}.tar.gz" -C "$STAGE/$os-$arch" "$NAME" LICENSE NOTICE THIRD-PARTY-NOTICES.md
done

if command -v sha256sum >/dev/null 2>&1; then
	(cd "$OUT" && sha256sum ./*.tar.gz | sed 's| \./| |' > checksums.txt)
else
	(cd "$OUT" && shasum -a 256 ./*.tar.gz | sed 's| \./| |' > checksums.txt)
fi

echo "v$VERSION" > dist/LATEST-dev
case "$VERSION" in
	*-*) ;;
	*) echo "v$VERSION" > dist/LATEST ;;
esac
echo "release v$VERSION -> $OUT"
