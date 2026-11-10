#!/bin/sh
# Install wdx-hub with install-hub.sh as the system service it installs,
# update it with wdx-hub update, see an update to a release that does not
# serve put back, roll it back, update it through the script again and
# uninstall it, with the shells' completions: the tests of the wdx docs
# HUB_INSTALL and HUB_COMPLETION. On the way it checks an update as root
# trusts only what root chose (SEC_FIXES F5, F15): not the release address in
# the hub's config file, nor a logs/ that is a symlink, and that the log file
# it opens is the hub's user's. Its releases are this tree, built by dist.sh
# as three versions for this machine and read from file://. The first prints
# its text output in capitals, so that the install works only by reading
# --json, whose keys are a contract (ARCH_STD_FIXES Q9); the third runs but
# never serves.
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
# Where the releases wrapped in a script keep the real binaries they run.
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
# mktemp makes it 0700, but wdx-hub update without --apply runs as the hub's
# user, which must read the releases in it.
chmod 0755 "$WORK"
DIST="$WORK/dist"
cleanup() {
	sudo sh scripts/install-hub.sh --purge --yes >/dev/null 2>&1 || true
	sudo rm -rf "$REAL"
	rm -rf "$WORK"
}
trap cleanup EXIT

# hub runs the installed wdx-hub as root, which becomes the hub's user, with
# the releases' address in root's environment, the one update --apply takes
# it from.
hub() { sudo env WDX_HUB_UPDATE_BASE_URL="file://$DIST" "$NAME" "$@"; }

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

# The completion of each shell, as install-hub.sh writes them for every user.
COMPLETIONS="bash:/usr/share/bash-completion/completions/$NAME zsh:/usr/local/share/zsh/site-functions/_$NAME fish:/usr/share/fish/vendor_completions.d/$NAME.fish"

# completions checks that the completion of every shell found is installed
# and names wdx-hub, and that of a shell not found is not.
completions() {
	for c in $COMPLETIONS; do
		shell="${c%%:*}"
		file="${c#*:}"
		if command -v "$shell" >/dev/null 2>&1; then
			grep -q "$NAME" "$file" || die "no $shell completion in $file"
		else
			[ ! -e "$file" ] || die "a $shell completion in $file, with no $shell"
		fi
	done
}

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

# wrap <version>: replaces the release's binary with the script on stdin, which
# runs the real one, kept in $REAL/<version>, as $real.
wrap() {
	archive="${NAME}_${1}_linux_${arch}.tar.gz"
	stage="$WORK/wrapped-$1"
	mkdir -p "$stage"
	tar -xzf "$DIST/v$1/$archive" -C "$stage"
	sudo install -D -m 0755 "$stage/$NAME" "$REAL/$1/$NAME"
	{
		printf '#!/bin/sh\nreal=%s\n' "$REAL/$1/$NAME"
		cat
	} >"$stage/$NAME"
	chmod 0755 "$stage/$NAME"
	tar -czf "$DIST/v$1/$archive" -C "$stage" "$NAME" LICENSE NOTICE THIRD-PARTY-NOTICES.md
	(cd "$DIST/v$1" && sha256sum ./*.tar.gz | sed 's| \./| |' >checksums.txt)
}

# The first release's setting, version and user print their text in
# capitals, which install-hub.sh would misread if it parsed text.
wrap "$V1" <<'EOF'
case "${1:-}" in
	setting | version | user)
		case " $* " in
			*" --json "*) ;;
			*)
				out="$("$real" "$@")"
				status=$?
				printf '%s\n' "$out" | tr '[:lower:]' '[:upper:]'
				exit "$status"
				;;
		esac
		;;
esac
exec "$real" "$@"
EOF

# The third release says it is V3 and writes the settings, so the update's
# preflight passes, but its serve fails at once.
wrap "$V3" <<'EOF'
[ "${1:-}" = serve ] && exit 1
exec "$real" "$@"
EOF

echo "installing $NAME $V1"
out="$(sudo env WDX_DOWNLOAD_BASE="file://$DIST" WDX_VERSION="$V1" WDX_HUB_PUBLIC_URL=http://localhost:8080 \
	sh scripts/install-hub.sh </dev/null)" || die "install: $out"
echo "$out"
echo "$out" | grep -q '^    http://localhost:8080/signup?invite=' || die "install printed no invite"
echo "$out" | grep -q "answers, version $V1\$" || die "install did not read the version the hub answers"
echo "$out" | grep -q 'listens on :8443 ' || die "install did not read the node endpoint's port"
[ "$(sudo "$NAME" version)" = "$(sudo "$NAME" version | tr '[:lower:]' '[:upper:]')" ] || die "$V1 does not print its text in capitals"
answers "$V1"
hub user list >/dev/null || die "sudo $NAME user list failed"
want ".update.current == \"$V1\" and .update.available" update
completions
owned

echo "refusing the release address the config file names, which install-hub.sh saved"
# So that the update's own run opens the day's log file.
sudo sh -c "rm -f $HUB_HOME/logs/*.log"
if out="$(sudo "$NAME" update --apply --version "$V2" --json)"; then
	die "the update from the config file's address succeeded: $out"
fi
echo "$out" | jq -e '.code == "update_source_untrusted"' >/dev/null || die "want update_source_untrusted, got $out"
sudo sh -c "ls $HUB_HOME/logs/*.log" >/dev/null || die "the refused update logged nothing"
answers "$V1"
owned

echo "refusing a logs/ that is a symlink"
elsewhere="$WORK/elsewhere"
mkdir "$elsewhere"
sudo mv "$HUB_HOME/logs" "$HUB_HOME/logs.kept"
sudo -u "$NAME" ln -s "$elsewhere" "$HUB_HOME/logs"
if out="$(hub update --apply --version "$V2" --json)"; then
	die "the update through a symlinked logs/ succeeded: $out"
else
	status=$?
fi
[ "$status" -eq 3 ] || die "want exit 3, got $status: $out"
echo "$out" | jq -e '.code == "home_unsafe"' >/dev/null || die "want home_unsafe, got $out"
[ -z "$(ls -A "$elsewhere")" ] || die "the update wrote through the symlink: $(ls -A "$elsewhere")"
sudo rm "$HUB_HOME/logs"
sudo mv "$HUB_HOME/logs.kept" "$HUB_HOME/logs"
answers "$V1"

echo "updating to $V2"
sudo sh -c "rm -f $HUB_HOME/logs/*.log"
want ".result == \"updated\" and .update.from == \"$V1\" and .update.to == \"$V2\"" update --apply --version "$V2"
answers "$V2"
sudo test -f "$HUB_HOME/backups/hub-$V1.db" || die "no backup of the database of $V1"
# The update's own run opened the day's log file: owned checks it is the hub's user's.
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

echo "updating to $V2 through the script, which writes the completions again"
sudo rm -f "/usr/share/bash-completion/completions/$NAME"
out="$(sudo env WDX_DOWNLOAD_BASE="file://$DIST" WDX_VERSION="$V2" sh scripts/install-hub.sh </dev/null)" ||
	die "the script's update: $out"
echo "$out" | grep -q "update --apply" || die "the script did not update: $out"
answers "$V2"
completions
owned

echo "uninstalling"
sudo sh scripts/install-hub.sh --uninstall || die "uninstall failed"
for path in "/usr/local/bin/$NAME" "/etc/systemd/system/$NAME.service" "/usr/local/lib/$NAME"; do
	[ ! -e "$path" ] || die "uninstall left $path"
done
for c in $COMPLETIONS; do
	[ ! -e "${c#*:}" ] || die "uninstall left ${c#*:}"
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
echo "installed $V1, updated to $V2, put $V2 back after $V3, rolled back to $V1, updated to $V2 by the script, uninstalled and purged, with the completions"
