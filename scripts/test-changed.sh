#!/bin/sh
# Runs the tests of the test files changed in RANGE or not yet committed TIMES
# times over, at 1 and 4 CPUs, on a machine kept busy: a test that fails only
# now and then is found while it is being written. GO_TEST is the command that
# runs them. Tests run in batches of a process each, as cobra keeps every flag
# it saw, which in one process adds up to more than the tests' memory cap.
set -u

batch=5

changed="$(
	{
		git diff --name-only --diff-filter=d "$RANGE"
		git diff --name-only --diff-filter=d HEAD
		git ls-files --others --exclude-standard
	} | grep '_test\.go$' | sort -u
)"
if [ -z "$changed" ]; then
	echo "no test file changed in $RANGE"
	exit 0
fi

hogs=""
# shellcheck disable=SC2329 # the trap runs it
stop_hogs() {
	for pid in $hogs; do
		kill "$pid" 2>/dev/null
	done
}
trap stop_hogs EXIT
for _ in $(seq "$(getconf _NPROCESSORS_ONLN)"); do
	sh -c 'while :; do :; done' &
	hogs="$hogs $!"
done

batches=$(((TIMES + batch - 1) / batch))
failed=0
for dir in $(echo "$changed" | xargs -n1 dirname | sort -u); do
	# shellcheck disable=SC2086 # the files of this directory, without spaces
	names="$(echo "$changed" | grep -E "^$dir/[^/]+$" | xargs sed -nE 's/^func (Test[A-Za-z0-9_]*)\(t \*testing\.T\).*/\1/p' | sort -u | paste -sd'|')"
	if [ -z "$names" ]; then
		continue
	fi
	echo "== ./$dir: $names"
	i=0
	while [ "$i" -lt "$batches" ]; do
		i=$((i + 1))
		# shellcheck disable=SC2086 # GO_TEST is a list of words
		$GO_TEST -count="$batch" -cpu=1,4 -timeout=20m -run "^($names)\$" "./$dir" || failed=1
	done
done
if [ "$failed" = 1 ]; then
	echo "a changed test failed: find out why, and never answer by retrying it (docs/FLAKES.md)" >&2
fi
exit "$failed"
