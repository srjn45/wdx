#!/bin/sh
# Cut a release: tag it, then build its archives into dist/. Nothing is
# pushed.
#
#   scripts/release.sh stable   vX.Y.Z: commits CHANGELOG.md, then tags that commit
#   scripts/release.sh dev      vX.Y.Z-dev.N: tags HEAD, without a commit
#
# X.Y.Z is the next stable version, which git-cliff works out from the
# commits since the last stable tag; N counts those commits. See
# docs/RELEASING.md.
#
# Env: GIT_CLIFF  the git-cliff binary; the Makefile passes the pinned one
set -eu

BRANCH=main
STABLE_TAGS='v[0-9]*.[0-9]*.[0-9]*'

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }

# Only git-cliff's errors are worth seeing; its info and warnings are noise here.
cliff() { RUST_LOG=error "${GIT_CLIFF:?set GIT_CLIFF to the git-cliff binary}" "$@"; }

channel="${1:-}"
case "$channel" in
	stable | dev) ;;
	*) die "usage: release.sh stable|dev" ;;
esac

cd "$(dirname "$0")/.."

[ "$(git branch --show-current)" = "$BRANCH" ] || die "releases are cut from $BRANCH"
[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
scripts/notices.sh >/dev/null
[ -z "$(git status --porcelain)" ] || die "THIRD-PARTY-NOTICES.md is out of date with the modules; commit it as scripts/notices.sh wrote it"
# A stable release usually promotes a commit already released as dev.
if [ "$channel" = stable ]; then
	released="$(git tag --points-at HEAD --list "$STABLE_TAGS" | grep -v -- - || true)"
else
	released="$(git tag --points-at HEAD --list 'v*')"
fi
[ -z "$released" ] || die "HEAD is already released as $released"

last="$(git describe --tags --abbrev=0 --match "$STABLE_TAGS" --exclude '*-*' 2>/dev/null || true)"
next="$(cliff --bumped-version)"
[ "$next" != "$last" ] || die "nothing to release since $last: no feat, fix, perf or breaking commit"

info "testing"
make test

if [ "$channel" = stable ]; then
	tag="$next"
	notes="$(cliff --unreleased --tag "$tag" --strip all)"
	cliff --tag "$tag" --output CHANGELOG.md
	git add CHANGELOG.md
	git commit -q -m "chore(release): $tag"
	info "committed CHANGELOG.md"
else
	tag="$next-dev.$(git rev-list --count "${last:+$last..}HEAD")"
	notes="$(cliff --unreleased --strip all)"
fi

git tag -a --cleanup=verbatim "$tag" -m "wdx $tag" -m "$notes"
info "tagged $tag"

scripts/dist.sh "$tag" "$(git rev-parse --short HEAD)"
