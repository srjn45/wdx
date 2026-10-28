#!/bin/sh
# Check that every commit in a range follows Conventional Commits with one of
# the types AGENTS.md lists: CHANGELOG.md and the next version are worked out
# from them. Merge commits are skipped.
#
# Usage: scripts/commits.sh <range>    e.g. origin/main..HEAD
set -eu

RANGE="${1:?usage: commits.sh <range>}"
TYPES='feat|fix|perf|docs|test|refactor|chore'

bad="$(git log --no-merges --format='%h %s' "$RANGE" |
	grep -Ev "^[0-9a-f]+ ($TYPES)(\([a-z0-9-]+\))?!?: [^ ]" || true)"
if [ -n "$bad" ]; then
	printf 'error: commits that are not <type>[(scope)][!]: <subject>, with a type of %s:\n%s\n' \
		"$(echo "$TYPES" | sed 's/|/, /g')" "$bad" >&2
	exit 1
fi
echo "commits in $RANGE follow Conventional Commits"
