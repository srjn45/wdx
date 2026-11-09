#!/bin/sh
# Install wdx-hub with install-hub.sh as the system service it installs,
# update it with wdx-hub update, see an update to a release that does not
# serve put back, roll it back, update it through the script again and
# uninstall it: the tests of the wdx doc HUB_INSTALL. Its releases are this
# tree, built by dist.sh as three versions for this machine and read from
# file://; the third runs but never serves.
#
# It needs root through sudo without a password, and systemd: it installs
# into /usr/local, /etc/systemd and /var/lib, so it runs in CI, never on a
# developer's machine.
#
# Usage: scripts/test-hub-install.sh
set -eu

V1=0.0.1
V2=0.0.2
V3=0.0.3
NAME=wdx-hub
HUB_HOME=/var/lib/wdx-hub/.wdx-hub
# Where the release that never serves keeps the real binary it runs.
REAL=/usr/local/lib/wdx-hub-test

cd "$(dirname "$0")/.."

die() {
	printf 'error: %s\n' "$*" >&2
	sudo journalctl -u "$NAME" -n 40 --no-pager >&2 || true
	for log in $(sudo sh -c "ls $HUB_HOME/logs/*.log 2>/dev/null"); do
		echo "--- $log" >&2
		sudo tail -n 30 "$log" >&2
	done
	exit 1
}

if [ "$(uname -s)" != Linux ] || [ ! -d /run/systemd/system ]; then
	die "needs Linux with systemd"
fi
sudo -n true 2>/dev/null || die "needs sudo without a password"
if [ -e "/usr/local/bin/$NAME" ] || id -u "$NAME" >/dev/null 2>&1; then
	die "$NAME is installed on this machine, which this test would replace and remove"
fi

WORK="$(mktemp -d /tmp/wdx-hub-install.XXXXXX)"
DIST="$WORK/dist"
cleanup() {
	sudo sh scripts/install-hub.sh --purge --yes >/dev/null 2>&1 || true
	sudo rm -rf "$REAL"
	rm -rf "$WORK"
}
trap cleanup EXIT

# hub runs the installed wdx-hub as root, which becomes the hub's user.
hub() { sudo "$NAME" "$@"; }

# want <jq filter> <command...>: runs the installed wdx-hub with --json and
# checks the filter holds on its output.
want() {
	filter="$1"
	shift
	out="$(hub "$@" --json)" || die "$NAME $*: exit $?: $out"
	echo "$out" | jq -e "$filter" >/dev/null || die "$NAME $*: want $filter, got $out"
}

# answers checks the hub answers on its socket as version $1.
answers() { want ".hub.version == \"$1\"" version; }

# owned checks that nothing in the hub's home is another user's, as root's
# commands and updates must leave it.
owned() {
	others="$(sudo find /var/lib/wdx-hub ! -user "$NAME")"
	[ -z "$others" ] || die "not the hub's user's: $others"
}

arch="$(go env GOARCH)"
for v in "$V1" "$V2" "$V3"; do
	DIST="$DIST" PLATFORMS="linux/$arch" scripts/dist.sh "$v" test >/dev/null
done

# The third release says it is V3 and writes the settings, so the update's
# preflight passes, but its serve fails at once.
archive="${NAME}_${V3}_linux_${arch}.tar.gz"
stage="$WORK/broken"
mkdir -p "$stage"
tar -xzf "$DIST/v$V3/$archive" -C "$stage"
sudo install -D -m 0755 "$stage/$NAME" "$REAL/$NAME"
cat >"$stage/$NAME" <<EOF
#!/bin/sh
[ "\${1:-}" = serve ] && exit 1
exec $REAL/$NAME "\$@"
EOF
chmod 0755 "$stage/$NAME"
tar -czf "$DIST/v$V3/$archive" -C "$stage" "$NAME" LICENSE NOTICE THIRD-PARTY-NOTICES.md
(cd "$DIST/v$V3" && sha256sum ./*.tar.gz | sed 's| \./| |' >checksums.txt)

echo "installing $NAME $V1"
out="$(sudo env WDX_DOWNLOAD_BASE="file://$DIST" WDX_VERSION="$V1" WDX_HUB_PUBLIC_URL=http://localhost:8080 \
	sh scripts/install-hub.sh </dev/null)" || die "install: $out"
echo "$out"
echo "$out" | grep -q 'signup?invite=' || die "install printed no invite"
answers "$V1"
hub user list >/dev/null || die "sudo $NAME user list failed"
want ".update.current == \"$V1\" and .update.available" update
owned

echo "updating to $V2"
want ".result == \"updated\" and .update.from == \"$V1\" and .update.to == \"$V2\"" update --apply --version "$V2"
answers "$V2"
sudo test -f "$HUB_HOME/backups/hub-$V1.db" || die "no backup of the database of $V1"
owned

echo "updating to $V3, which does not serve"
if out="$(hub update --apply --version "$V3" --json)"; then
	die "the update to $V3 succeeded: $out"
fi
echo "$out" | jq -e '.code == "update_failed"' >/dev/null || die "want update_failed, got $out"
answers "$V2"
owned

echo "rolling back to $V1"
want '.result == "rolled_back"' update --rollback --cancel
answers "$V1"
owned

echo "updating to $V2 through the script"
out="$(sudo env WDX_DOWNLOAD_BASE="file://$DIST" WDX_VERSION="$V2" sh scripts/install-hub.sh </dev/null)" ||
	die "the script's update: $out"
echo "$out" | grep -q "update --apply" || die "the script did not update: $out"
answers "$V2"
owned

echo "uninstalling"
sudo sh scripts/install-hub.sh --uninstall || die "uninstall failed"
for path in "/usr/local/bin/$NAME" "/etc/systemd/system/$NAME.service" "/usr/local/lib/$NAME"; do
	[ ! -e "$path" ] || die "uninstall left $path"
done
for path in "$HUB_HOME/config.yaml" "$HUB_HOME/hub.db"; do
	sudo test -f "$path" || die "uninstall removed $path"
done
id -u "$NAME" >/dev/null || die "uninstall removed the user"

echo "purging"
sudo sh scripts/install-hub.sh --purge --yes </dev/null || die "purge failed"
if id -u "$NAME" >/dev/null 2>&1 || sudo test -e /var/lib/wdx-hub; then
	die "purge left the user or its home"
fi
echo "installed $V1, updated to $V2, put $V2 back after $V3, rolled back to $V1, updated to $V2 by the script, uninstalled and purged"
