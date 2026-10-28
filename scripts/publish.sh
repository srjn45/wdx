#!/bin/sh
# Publish the GitHub Release of a tag that make release pushed: build its
# archives with the tag's own scripts/dist.sh, then upload them with
# checksums.txt and the tag's install.sh, the tag's notes as the body. A dev
# tag is a pre-release, so GitHub's latest release stays the newest stable
# one, as the installer and wdx update expect.
#
# The release is created as a draft and published once every file is up, so
# a failure leaves a draft, which the next run replaces.
#
# Usage: scripts/publish.sh <tag>    (.github/workflows/release.yml runs it)
# Env: GH_TOKEN  a token that may write the repository's releases
set -eu

TAG="${1:?usage: publish.sh <tag>}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }

cd "$(dirname "$0")/.."

# actions/checkout can leave a pushed tag lightweight; the notes live in the
# annotated tag object.
git fetch -q --force origin "refs/tags/$TAG:refs/tags/$TAG"
[ "$(git cat-file -t "$TAG")" = tag ] || die "$TAG is not an annotated tag; make release and make release-dev create them"
git merge-base --is-ancestor "$TAG^{commit}" origin/main || die "$TAG is not on main"

if [ "$(gh release view "$TAG" --json isDraft --jq .isDraft 2>/dev/null)" = true ]; then
	info "deleting the draft a failed run left"
	gh release delete "$TAG" --yes
elif gh release view "$TAG" >/dev/null 2>&1; then
	die "$TAG is already published"
fi

tmp="$(mktemp -d)"
trap 'git worktree remove --force "$tmp/src" 2>/dev/null; rm -rf "$tmp"' EXIT
git worktree add -q --detach "$tmp/src" "$TAG"

version="${TAG#v}"
(cd "$tmp/src" && scripts/dist.sh "$TAG" "$(git rev-parse --short HEAD)")
out="$tmp/src/dist/v$version"

git tag --list --format='%(contents:body)' "$TAG" >"$tmp/notes.md"
case "$version" in
	*-*) kind="--prerelease --latest=false" ;;
	*) kind="--latest" ;;
esac
# shellcheck disable=SC2086 # $kind holds flags
gh release create "$TAG" --draft --verify-tag $kind \
	--title "$(git tag --list --format='%(contents:subject)' "$TAG")" \
	--notes-file "$tmp/notes.md" \
	"$out"/*.tar.gz "$out/checksums.txt" "$tmp/src/scripts/install.sh"
gh release edit "$TAG" --draft=false
info "published $TAG"
