#!/bin/sh
# Print the statement coverage of each package from a coverage profile, as a
# Markdown table. With -coverpkg every test binary reports every package, so
# a block counts as covered when any of them ran it.
#
# Usage: scripts/cover.sh <profile>
set -eu

PROFILE="${1:?usage: cover.sh <profile>}"
MODULE="$(go list -m)"

awk -v module="$MODULE/" '
	NR == 1 { next }
	{
		stmts[$1] = $2
		if ($3 > 0) covered[$1] = 1
	}
	END {
		for (block in stmts) {
			pkg = block
			sub(/\/[^\/]*:.*/, "", pkg)
			sub(module, "", pkg)
			total[pkg] += stmts[block]
			all += stmts[block]
			if (block in covered) { hit[pkg] += stmts[block]; allhit += stmts[block] }
		}
		print "| Package | Coverage |"
		print "|---|---:|"
		for (pkg in total) printf "| %s | %.1f%% |\n", pkg, 100 * hit[pkg] / total[pkg] | "sort"
		close("sort")
		printf "| **total** | **%.1f%%** |\n", 100 * allhit / all
	}
' "$PROFILE"
