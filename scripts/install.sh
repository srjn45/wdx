#!/bin/sh
# Install wdx from a release.
#
#   curl -fsSL https://github.com/srjn45/warden/releases/latest/download/install.sh | sh
#
# Downloads the archive for this OS/arch, verifies its checksum, installs the
# binary into ~/.local/bin and runs the daemon as a service: a systemd user
# unit on Linux (with linger, so it runs from boot), a launchd agent on macOS
# (from login). Every install restarts the daemon on the new binary. No git or
# Go required.
#
# Env:
#   WDX_VERSION        release to install (e.g. 0.1.0); default: latest
#   WDX_DOWNLOAD_BASE  where releases live; default: GitHub Releases.
#                      Local builds use file://<repo>/dist (see `make install`).
#   WDX_BIN_DIR        install directory; default: ~/.local/bin
#
# Flags: --uninstall   stop and remove the service and the binary; ~/.wdx stays
set -eu

NAME=wdx
REPO=srjn45/warden
GITHUB_BASE="https://github.com/$REPO/releases/download"
BASE="${WDX_DOWNLOAD_BASE:-$GITHUB_BASE}"
BIN_DIR="${WDX_BIN_DIR:-$HOME/.local/bin}"
TARGET="$BIN_DIR/$NAME"
HOME_DIR="$HOME/.$NAME"

# The service names internal/service looks for.
UNIT="$HOME/.config/systemd/user/$NAME.service"
LABEL="com.github.srjn45.$NAME"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
SERVICE_LOG="$HOME/Library/Logs/$NAME.log"

info() { printf '==> %s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# wdx runs the installed binary on the default home, the one the service
# serves.
wdx() { env -u WDX_HOME "$TARGET" "$@"; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
	linux | darwin) ;;
	*) die "unsupported OS '$os' (linux and darwin only)" ;;
esac

# --- uninstall ------------------------------------------------------------
uninstall() {
	if [ -x "$TARGET" ]; then
		wdx daemon stop || warn "could not stop the daemon"
	fi
	if [ -f "$UNIT" ]; then
		systemctl --user disable "$NAME.service" >/dev/null 2>&1 || true
		rm -f "$UNIT"
		systemctl --user daemon-reload || true
		info "removed the systemd user service $NAME.service"
		info "linger stays enabled for other services; disable it with: loginctl disable-linger"
	fi
	if [ -f "$PLIST" ]; then
		launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
		rm -f "$PLIST"
		info "removed the launchd agent $LABEL"
	fi
	rm -f "$TARGET"
	info "removed $TARGET; your projects and settings stay in $HOME_DIR"
}

case "${1:-}" in
	"") ;;
	--uninstall)
		uninstall
		exit 0
		;;
	*) die "unknown argument: $1" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

# --- platform -------------------------------------------------------------
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture '$arch' (amd64 and arm64 only)" ;;
esac

# --- version --------------------------------------------------------------
if [ -n "${WDX_VERSION:-}" ]; then
	tag="v${WDX_VERSION#v}"
elif [ "$BASE" = "$GITHUB_BASE" ]; then
	tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)" ||
		die "failed to resolve the latest release"
else
	tag="$(curl -fsSL "$BASE/LATEST")" || die "failed to read $BASE/LATEST"
fi
[ -n "$tag" ] || die "could not determine which version to install"
version="${tag#v}"

# --- download & verify ----------------------------------------------------
archive="${NAME}_${version}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/$NAME-install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

info "downloading $NAME $tag ($os/$arch)"
curl -fsSL "$BASE/$tag/$archive" -o "$tmp/$archive" || die "failed to download $BASE/$tag/$archive"
curl -fsSL "$BASE/$tag/checksums.txt" -o "$tmp/checksums.txt" || die "failed to download checksums.txt"

expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$expected" ] || die "no checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" "$NAME" || die "failed to extract $archive"

# --- install --------------------------------------------------------------
# Copy then rename so a running wdx is never replaced by a half-written file.
mkdir -p "$BIN_DIR"
cp "$tmp/$NAME" "$TARGET.tmp.$$"
chmod 0755 "$TARGET.tmp.$$"
mv -f "$TARGET.tmp.$$" "$TARGET"
info "installed $NAME $version -> $TARGET"

case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*)
		warn "$BIN_DIR is not on your PATH; add this to your shell profile:"
		printf '    export PATH="%s:$PATH"\n' "$BIN_DIR" >&2
		;;
esac

# --- settings -------------------------------------------------------------
mkdir -p "$HOME_DIR"
chmod 0700 "$HOME_DIR"
if [ ! -e "$HOME_DIR/config.yaml" ]; then
	cat >"$HOME_DIR/config.yaml" <<'EOF'
# wdx settings, read when wdx and its daemon start; after changing them run:
#   wdx daemon restart
# Every setting is optional; uncomment one to change it. See wdx --help.

# Never prompt, as --no-input does: true or false.
# no_input: false

# Color text output: auto (only on a terminal, unless NO_COLOR is set),
# always or never.
# color: auto

# log:
#   # Lowest level logged: debug, info, warn, error.
#   level: info
#   # Days of log files kept besides today's.
#   retention_days: 7

# otel:
#   # OpenTelemetry collector the daemon sends its metrics to over OTLP/HTTP,
#   # such as the one wdx observe starts; empty sends none.
#   endpoint: http://localhost:4318
#   # Seconds between two sends of the metrics.
#   interval_seconds: 15
EOF
	chmod 0600 "$HOME_DIR/config.yaml"
	info "wrote the settings template $HOME_DIR/config.yaml"
fi

# --- daemon service -------------------------------------------------------
# Stop the daemon running now, the old binary's or one started by hand, so
# the service starts the new one.
wdx daemon stop >/dev/null || warn "could not stop the running daemon"

install_systemd() {
	if ! command -v systemctl >/dev/null 2>&1 || ! systemctl --user show-environment >/dev/null 2>&1; then
		warn "no systemd user session: the daemon is started now but not after a reboot; then run: $NAME daemon start"
		return 1
	fi
	mkdir -p "$(dirname "$UNIT")"
	# The service gets this shell's PATH, so it finds the same git.
	cat >"$UNIT" <<EOF
[Unit]
Description=$NAME daemon, serving the $NAME commands on $HOME_DIR/$NAME.sock

[Service]
ExecStart=$TARGET daemon start --foreground
Environment="PATH=$PATH"
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF
	systemctl --user daemon-reload
	systemctl --user enable "$NAME.service" 2>/dev/null || die "failed to enable $NAME.service"
	info "installed the systemd user service $NAME.service"

	# Linger starts the user's services at boot and keeps them after logout.
	user="$(id -un)"
	if [ "$(loginctl show-user "$user" -p Linger --value 2>/dev/null)" != yes ]; then
		if loginctl enable-linger "$user" 2>/dev/null; then
			info "enabled linger, so the daemon runs from boot"
		else
			warn "could not enable linger, so the daemon starts only at login; to run it from boot:"
			printf '    sudo loginctl enable-linger %s\n' "$user" >&2
		fi
	fi
}

install_launchd() {
	mkdir -p "$(dirname "$PLIST")" "$(dirname "$SERVICE_LOG")"
	launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	# The agent gets this shell's PATH, so it finds the same git. It restarts
	# the daemon only when it fails, so wdx daemon stop keeps it stopped.
	cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$TARGET</string>
		<string>daemon</string>
		<string>start</string>
		<string>--foreground</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>$PATH</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>$SERVICE_LOG</string>
	<key>StandardErrorPath</key>
	<string>$SERVICE_LOG</string>
</dict>
</plist>
EOF
	launchctl bootstrap "gui/$(id -u)" "$PLIST" || die "failed to load the launchd agent $PLIST"
	info "installed the launchd agent $LABEL, which starts the daemon at login"
}

case "$os" in
	linux) install_systemd || true ;;
	darwin) install_launchd ;;
esac

# Starts the daemon through the service, or waits until the agent has, and
# fails if it does not answer.
wdx daemon start >/dev/null || die "the daemon did not start; see $NAME daemon status and the log in $HOME_DIR/logs"
wdx daemon status
info "to see the logs and metrics in Grafana, run: $NAME observe start (needs Docker or Podman)"
