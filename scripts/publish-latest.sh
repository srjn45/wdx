#!/bin/sh
# Point the dev channel at a published release: write its tag to LATEST-dev on
# the channels branch, which wdx update and install.sh read. GitHub's latest
# release is never a pre-release, and its list of releases is in no promised
# order, so nothing else of GitHub's says which dev release is the newest.
#
# The file only moves forward, so publishing an old tag again leaves it alone.
# It runs on its own for a release already published whose run failed here.
#
# Usage: scripts/publish-latest.sh <tag>    (scripts/publish.sh runs it)
set -eu

TAG="${1:?usage: publish-latest.sh <tag>}"
BRANCH=channels
FILE=LATEST-dev
ATTEMPTS=5

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }

# newest_tag prints the highest of the tags it reads, vX.Y.Z or vX.Y.Z-dev.N,
# a stable release after the dev ones leading to it, and ignores anything else.
newest_tag() {
	sed -n -e 's/^v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)$/\1 \2 \3 1 0 &/p' \
		-e 's/^v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)-dev\.\([0-9]*\)$/\1 \2 \3 0 \4 &/p' |
		sort -n -k1,1 -k2,2 -k3,3 -k4,4 -k5,5 | tail -n 1 | cut -d' ' -f6
}

[ "$(printf '%s\n' "$TAG" | newest_tag)" = "$TAG" ] || die "$TAG is not a release tag"

export GIT_AUTHOR_NAME="wdx release" GIT_AUTHOR_EMAIL="wdx-release@users.noreply.github.com"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

# Another tag's run may push between the read and the push, which then fails
# without harm; the next attempt compares with what it pushed.
attempt=1
while [ "$attempt" -le "$ATTEMPTS" ]; do
	parent="" current=""
	heads="$(git ls-remote origin "refs/heads/$BRANCH")"
	if [ -n "$heads" ]; then
		git fetch -q origin "refs/heads/$BRANCH"
		parent="$(git rev-parse FETCH_HEAD)"
		current="$(git show "$parent:$FILE")"
	fi
	if [ "$current" = "$TAG" ]; then
		info "$FILE is already $TAG"
		exit 0
	fi
	if [ "$(printf '%s\n%s\n' "$TAG" "$current" | newest_tag)" != "$TAG" ]; then
		info "$FILE stays at $current, newer than $TAG"
		exit 0
	fi
	blob="$(printf '%s\n' "$TAG" | git hash-object -w --stdin)"
	tree="$(printf '100644 blob %s\t%s\n' "$blob" "$FILE" | git mktree)"
	commit="$(git commit-tree "$tree" ${parent:+-p "$parent"} -m "$FILE: $TAG")"
	if git push -q origin "$commit:refs/heads/$BRANCH"; then
		info "$FILE now names $TAG"
		exit 0
	fi
	attempt=$((attempt + 1))
	sleep 2
done
die "could not move $FILE to $TAG; run scripts/publish-latest.sh $TAG again"
