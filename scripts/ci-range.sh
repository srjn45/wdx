#!/bin/sh
# Prints the commits a CI run is about: those of the PR or of the push to main;
# none on other runs, a tag's included, as they were checked on main. A push
# whose before is not in the history, as after a force push, is its last commit.
set -eu

range=HEAD..HEAD
if [ "$EVENT" = pull_request ]; then
	range="$BASE..$HEAD"
elif [ "$EVENT" = push ] && [ "${REF#refs/heads/}" != "$REF" ]; then
	if git cat-file -e "$BEFORE^{commit}" 2>/dev/null; then
		range="$BEFORE..$SHA"
	else
		range="$SHA~1..$SHA"
	fi
fi
echo "$range"
