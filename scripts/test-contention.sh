#!/bin/sh
# Runs the tests RUNS times at once on a machine kept busy, as when several
# agents test at the same time: runs must not affect each other, and load must
# slow the tests rather than fail them. GO_TEST is the command that runs them.
set -u

logs="$(mktemp -d "${TMPDIR:-/tmp}/wdx-contention.XXXXXX")"
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

started=""
i=0
while [ "$i" -lt "$RUNS" ]; do
	i=$((i + 1))
	# shellcheck disable=SC2086 # GO_TEST and PKG are lists of words
	$GO_TEST -count="$COUNT" -timeout=30m $PKG >"$logs/run$i.log" 2>&1 &
	started="$started $!:$i"
done

failed=0
for run in $started; do
	if wait "${run%%:*}"; then
		echo "run ${run##*:}: ok"
	else
		failed=1
		echo "run ${run##*:}: FAILED, see $logs/run${run##*:}.log"
		grep -E '^(--- FAIL|    --- FAIL|FAIL|panic:)|^runtest:' "$logs/run${run##*:}.log" | cut -c1-200
	fi
done
if [ "$failed" = 0 ]; then
	rm -rf "$logs"
fi
exit "$failed"
