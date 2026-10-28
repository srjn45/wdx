#!/bin/sh
# Build a pinned Go tool into bin/. go install checks the module against the
# Go checksum database, so no checksums are recorded here.
#
# Usage: scripts/fetch-go-tool.sh <package>@<version> <target>
set -eu

PKG="${1:?usage: fetch-go-tool.sh <package>@<version> <target>}"
TARGET="${2:?usage: fetch-go-tool.sh <package>@<version> <target>}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

GOBIN="$tmp" GOFLAGS='' go install "$PKG"
mkdir -p "$(dirname "$TARGET")"
mv "$tmp/$(basename "${PKG%@*}")" "$TARGET"
echo "installed $PKG -> $TARGET"
