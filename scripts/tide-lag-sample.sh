#!/bin/sh
# Gather what the wdx doc TIDE_LAG asks for, on the machine where tide lags.
# Run it from a terminal window outside tide, WHILE tide lags, and keep
# typing in tide during the 20 s sample. It writes one report and prints
# its path; send that file (or wdx doc create --path <report>).
#
# Usage: sh tide-lag-sample.sh [seconds]   (default 20)

set -u
secs="${1:-20}"
report="$HOME/tide-lag-$(date +%Y%m%d-%H%M%S).txt"
logs="${WDX_HOME:-$HOME/.wdx}/logs"

say() { printf '%s\n' "$*" | tee -a "$report"; }
section() { say ""; say "== $*"; }

say "tide lag report, $(date), $(uname -sm), wdx $(wdx version 2>/dev/null | head -1)"

section "A. the holder's vt falling behind (expect 0)"
if ls "$logs"/*.log >/dev/null 2>&1; then
	say "vt fell behind lines: $(cat "$logs"/*.log | grep -c 'vt fell behind')"
	say "longest 'screen kept in history' (ms):"
	grep -h 'screen kept in history' "$logs"/*.log | grep -oE 'duration_ms=[0-9]+' | sort -t= -k2 -n | tail -3 | tee -a "$report"
else
	say "no logs in $logs"
fi

section "B. processes"
tide_pid=$(pgrep -f 'wdx tide$' | head -1)
holder_pid=$(pgrep -f 'wdx holder' | head -1)
say "tide pid: ${tide_pid:-none}   holder pid (first): ${holder_pid:-none}"
say "wdx processes:"
pids=$(pgrep -d, wdx)
[ -n "$pids" ] && ps -o pid,pcpu,rss,etime,command -p "$pids" 2>/dev/null | tee -a "$report"

if [ -z "$tide_pid" ]; then
	say ""
	say "tide is not running: start it, make it lag, then run this again."
	exit 1
fi

section "C. sampling tide for $secs s: keep typing in tide now"
if command -v sample >/dev/null 2>&1; then
	if sample "$tide_pid" "$secs" -file "$report.tide-sample.txt" >/dev/null 2>&1; then
		say "tide sample: $report.tide-sample.txt"
	else
		say "sample of tide failed (needs Xcode command line tools: xcode-select --install)"
	fi
	if [ -n "$holder_pid" ]; then
		sample "$holder_pid" 10 -file "$report.holder-sample.txt" >/dev/null 2>&1 \
			&& say "holder sample: $report.holder-sample.txt"
	fi
elif command -v perf >/dev/null 2>&1; then
	perf record -q -g -p "$tide_pid" -o "$report.perf.data" -- sleep "$secs" 2>/dev/null \
		&& perf report -i "$report.perf.data" --stdio --no-children 2>/dev/null | head -80 > "$report.tide-sample.txt" \
		&& say "tide profile: $report.tide-sample.txt"
else
	say "neither sample (macOS) nor perf (Linux) found"
fi

section "D. still to do by hand"
say "In this window: wdx attach --name <the agent>  and type while tide lags."
say "Note here whether it lagged there too: ______"

say ""
say "report: $report"
say "send it with:  wdx doc create --path $report --name TIDE_SAMPLE"
[ -f "$report.tide-sample.txt" ] && say "and the sample: wdx doc create --path $report.tide-sample.txt --name TIDE_SAMPLE_TREE"
