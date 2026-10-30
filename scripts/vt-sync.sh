#!/bin/sh
# Rebase our fork's branch, which holds the vt and ansi modules, onto
# upstream's main and move wdx to it
# (docs/VT.md, Keeping the fork current). Run from a clean wdx checkout,
# on request and before releasing; the fork's checkout is $VT_FORK,
# ~/dev/x by default, on its branch wdx with nothing to commit.
#
# A conflict stops the rebase: resolve it, git rebase --continue in the
# fork, then run this again to finish.
#
# Usage: scripts/vt-sync.sh
set -eu

FORK="${VT_FORK:-$HOME/dev/x}"
BRANCH=wdx
# The modules of the fork wdx builds from, all at the branch's tip.
MODULES="vt ansi"

cd "$(dirname "$0")/.."

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

fork() {
	git -C "$FORK" "$@"
}

[ -z "$(git status --porcelain)" ] || die "the wdx tree has changes; commit or set them aside first"
[ -d "$FORK/.git" ] || die "no fork checkout in $FORK; set VT_FORK"
[ "$(fork symbolic-ref --short HEAD)" = "$BRANCH" ] || die "the fork's checkout in $FORK is not on $BRANCH"
[ -z "$(fork status --porcelain)" ] || die "the fork's tree in $FORK has changes"

echo "fetching upstream's main and the fork"
fork fetch -q upstream main
fork fetch -q origin "$BRANCH"
# The fork's main mirrors upstream's; a push that is not a fast-forward
# fails, leaving it for a person to look at.
fork push -q origin upstream/main:refs/heads/main
upstream="$(fork rev-parse --short=12 upstream/main)"

if fork merge-base --is-ancestor upstream/main HEAD; then
	echo "$BRANCH contains upstream's main ($upstream)"
else
	[ "$(fork rev-parse HEAD)" = "$(fork rev-parse "origin/$BRANCH")" ] ||
		die "the fork's $BRANCH differs from origin/$BRANCH; push or reset it first"
	# Keep the commits wdx pinned on the fork: the rebase rewrites them.
	tag="wdx-$(date -u +%Y%m%d-%H%M%S)"
	fork tag "$tag"
	fork push -q origin "refs/tags/$tag"
	echo "tagged the old tip $tag"
	taken="$(fork cherry upstream/main HEAD | sed -n 's/^- //p')"
	if [ -n "$taken" ]; then
		echo "upstream took these fixes, which the rebase drops (update docs/VT.md):"
		for c in $taken; do
			printf '  %s\n' "$(fork log -1 --format='%h %s' "$c")"
		done
	fi
	echo "rebasing $BRANCH onto upstream's main ($upstream)"
	fork rebase -q upstream/main ||
		die "the rebase stopped on a conflict in $FORK: resolve it, git rebase --continue, then run this again"
fi

if [ "$(fork rev-parse HEAD)" != "$(fork rev-parse "origin/$BRANCH")" ]; then
	for m in $MODULES; do
		echo "testing $m"
		(cd "$FORK/$m" && go test -race ./...)
	done
	fork push -q --force-with-lease origin "$BRANCH"
	echo "pushed the fork's $BRANCH"
fi

tip="$(fork rev-parse --short=12 HEAD)"
moved=
for m in $MODULES; do
	info="$(go mod download -json "github.com/srjn45/x/$m@$tip")" ||
		die "go cannot fetch the fork's $m at $tip: $(echo "$info" | jq -r .Error)"
	version="$(echo "$info" | jq -r .Version)"
	if [ "$version" != "$(go list -m -f '{{.Replace.Version}}' "github.com/charmbracelet/x/$m")" ]; then
		echo "moving wdx's $m to $version"
		go mod edit -replace "github.com/charmbracelet/x/$m=github.com/srjn45/x/$m@$version"
		moved=yes
	fi
done
if [ -z "$moved" ]; then
	echo "wdx already uses the fork's $BRANCH ($tip)"
	exit 0
fi

go mod tidy
make notices
make check
make test
git add go.mod go.sum THIRD-PARTY-NOTICES.md
git commit -q -m "chore: build the vt and ansi from the fork at $tip, on upstream $upstream"
echo "committed: $(git log -1 --format='%h %s')"
