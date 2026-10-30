#!/bin/sh
# Update an installed wdx to a release built by dist.sh, as a user's wdx
# update would from GitHub, then roll it back. Both are this tree: the
# installed one built as 0.0.1, the release as 0.0.2, for this machine
# only. Everything lives in a temporary home with its own daemon.
#
# Usage: scripts/test-update.sh
set -eu

OLD=0.0.1
NEW=0.0.2
PKG=github.com/srjn45/wdx/internal/identity

cd "$(dirname "$0")/.."
# Under /tmp: the daemon's socket path must fit the 104 bytes macOS allows.
WDX_HOME="$(mktemp -d /tmp/wdx-update.XXXXXX)"
export WDX_HOME
bin="$WDX_HOME/bin/wdx"
cleanup() {
	"$bin" daemon stop --json >/dev/null 2>&1 || true
	rm -rf "$WDX_HOME"
}
trap cleanup EXIT

die() {
	printf 'error: %s\n' "$*" >&2
	for log in "$WDX_HOME"/logs/*.log; do
		[ -f "$log" ] && { echo "--- $log" >&2; tail -n 30 "$log" >&2; }
	done
	exit 1
}

# want <jq filter> <command...>: runs wdx with --json and checks the filter
# holds on its output.
want() {
	filter="$1"
	shift
	out="$("$bin" "$@" --json)" || die "wdx $*: exit $?: $out"
	echo "$out" | jq -e "$filter" >/dev/null || die "wdx $*: want $filter, got $out"
}

echo "installing wdx $OLD in $WDX_HOME"
CGO_ENABLED=0 go build -ldflags "-X $PKG.Version=$OLD" -o "$bin" ./cmd/wdx
DIST="$WDX_HOME/dist" PLATFORMS="$(go env GOOS)/$(go env GOARCH)" scripts/dist.sh "$NEW" none >/dev/null
printf 'otel:\n  endpoint: ""\nupdate:\n  base_url: file://%s\n' "$WDX_HOME/dist" >"$WDX_HOME/config.yaml"

want '.result == "started"' daemon start
want '.result == "created"' project create --name before --cancel

echo "updating to $NEW"
want ".result == \"updated\" and .update.to == \"$NEW\"" update --apply
want ".cli.version == \"$NEW\" and .daemon.version == \"$NEW\"" version
want '[.projects[].name] == ["before"]' project list

echo "rolling back to $OLD"
want '.result == "rolled_back"' update --rollback --cancel
want ".cli.version == \"$OLD\" and .daemon.version == \"$OLD\"" version
want '[.projects[].name] == ["before"]' project list
echo "updated to $NEW and rolled back to $OLD"
