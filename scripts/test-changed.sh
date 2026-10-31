#!/bin/sh
# Repeats the tests of the test files changed in RANGE or not yet committed that
# a change can reach (scripts/changedtests) TIMES times over, at 1 and 4 CPUs, on
# a machine kept busy: a test that fails only now and then is found while it is
# being written. GO_TEST is the command that runs them.
set -u

selected="$(go run ./scripts/changedtests "$RANGE")" || exit 1
if [ -z "$selected" ]; then
	echo "no test a change in $RANGE can reach"
	exit 0
fi

# The cli tests' TestMain builds wdx: once is enough for all these runs.
WDX_TEST_BINARIES="$(mktemp -d)"
export WDX_TEST_BINARIES
trap 'rm -rf "$WDX_TEST_BINARIES"' EXIT

# Each loop ends when this run does, however it ends.
keep_busy() {
	for _ in $(seq "$(getconf _NPROCESSORS_ONLN)"); do
		sh -c 'while kill -0 "$1" 2>/dev/null; do :; done' hog "$$" &
	done
	wait
}
# Runs that overlap load each other already: one at a time keeps the machine
# busy, and the next takes over when it ends.
if command -v flock >/dev/null; then
	(
		flock 9
		keep_busy
	) 9>"${TMPDIR:-/tmp}/wdx-test-changed-$(id -u).lock" &
else
	keep_busy &
fi

failed=0
while read -r dir names; do
	echo "== ./$dir: $names"
	# Each batch is a process, whose TestMain and what tests share start again.
	# The cli's are small, as cobra keeps every flag it saw, which in one process
	# adds up to more than the tests' memory cap.
	case "$dir" in
	internal/cli) batch=2 ;;
	*) batch=10 ;;
	esac
	left="$TIMES"
	while [ "$left" -gt 0 ]; do
		count=$((left < batch ? left : batch))
		left=$((left - count))
		# shellcheck disable=SC2086 # GO_TEST is a list of words
		$GO_TEST -count="$count" -cpu=1,4 -timeout=20m -run "^($names)\$" "./$dir" </dev/null || failed=1
	done
done <<EOF
$selected
EOF
if [ "$failed" = 1 ]; then
	echo "a changed test failed: find out why, and never answer by retrying it (docs/FLAKES.md)" >&2
fi
exit "$failed"
