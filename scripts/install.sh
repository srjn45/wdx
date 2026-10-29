#!/bin/sh
# Install wdx from a release.
#
#   curl -fsSL https://github.com/srjn45/wdx/releases/latest/download/install.sh | sh
#
# Downloads the archive for this OS/arch, verifies its checksum, installs the
# binary into ~/.local/bin, the completion of every shell found among bash,
# zsh and fish, and runs the daemon as a service: a systemd user unit on
# Linux (with linger, so it runs from boot), a launchd agent on macOS (from
# login). Every install restarts the daemon on the new binary. No git or Go
# required.
#
# Env:
#   WDX_CHANNEL        stable (default) installs the latest stable release;
#                      dev the latest release of either kind
#   WDX_VERSION        release to install (e.g. 0.1.0), whatever the channel
#   WDX_DOWNLOAD_BASE  where releases live; default: GitHub Releases.
#                      Local builds use file://<repo>/dist (see `make install`).
#   WDX_BIN_DIR        install directory; default: ~/.local/bin
#
# Flags: --uninstall    stop and remove the service, the binary and the
#                      completions; ~/.wdx stays
#        --completions  only (re)install the completions of the installed wdx
set -eu

NAME=wdx
REPO=srjn45/wdx
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

# The shell completions. Each shell loads the files of these directories
# when it starts.
BASH_COMPLETION="${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/$NAME"
ZSH_COMPLETIONS="$HOME/.zsh/completions"
FISH_COMPLETION="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions/$NAME.fish"

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

# --- shell completion -----------------------------------------------------
# brew_zsh_dir prints Homebrew's zsh site-functions, which zsh installed by
# Homebrew searches, when Homebrew is installed.
brew_zsh_dir() {
	command -v brew >/dev/null 2>&1 && printf '%s/share/zsh/site-functions' "$(brew --prefix)"
}

# write_completion writes the completion script of shell $1 to file $2.
write_completion() {
	mkdir -p "$(dirname "$2")"
	if wdx completion "$1" >"$2.tmp.$$" && mv -f "$2.tmp.$$" "$2"; then
		info "installed the $1 completion $2"
		return 0
	fi
	rm -f "$2.tmp.$$"
	warn "could not write the $1 completion to $2"
	return 1
}

# install_completions installs the completion of every shell found. It never
# edits a shell's startup file: it says what to add when the shell would not
# find the script.
install_completions() {
	if command -v bash >/dev/null 2>&1 && write_completion bash "$BASH_COMPLETION"; then
		loader=
		for f in /usr/share/bash-completion/bash_completion /etc/bash_completion \
			"$(brew --prefix 2>/dev/null)/etc/profile.d/bash_completion.sh"; do
			[ -f "$f" ] && loader="$f"
		done
		if [ -z "$loader" ]; then
			warn "the bash-completion package, which loads it, is not installed; or add this to ~/.bashrc:"
			printf '    source <(%s completion bash)\n' "$NAME" >&2
		fi
	fi
	if command -v zsh >/dev/null 2>&1; then
		dir="$ZSH_COMPLETIONS"
		brew_dir="$(brew_zsh_dir || true)"
		if [ -n "$brew_dir" ] && [ -w "$brew_dir" ]; then
			dir="$brew_dir"
		fi
		# Only zsh's own fpath is read: an interactive zsh would run the
		# user's ~/.zshrc, which may do anything.
		if write_completion zsh "$dir/_$NAME" &&
			! zsh -c 'print -rl -- $fpath' 2>/dev/null | grep -qxF "$dir" &&
			! grep -qsF ".zsh/completions" "${ZDOTDIR:-$HOME}/.zshrc"; then
			warn "zsh does not search $dir yet; add this to ~/.zshrc, before compinit:"
			# shellcheck disable=SC2016 # a line for the user's ~/.zshrc
			printf '    fpath=(%s $fpath)\n' "$dir" >&2
		fi
	fi
	if command -v fish >/dev/null 2>&1; then
		write_completion fish "$FISH_COMPLETION" || true
	fi
}

remove_completions() {
	for f in "$BASH_COMPLETION" "$ZSH_COMPLETIONS/_$NAME" "$(brew_zsh_dir || true)/_$NAME" "$FISH_COMPLETION"; do
		if [ -f "$f" ]; then
			rm -f "$f"
			info "removed the completion $f"
		fi
	done
}

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
	remove_completions
	rm -f "$TARGET"
	info "removed $TARGET; your projects and settings stay in $HOME_DIR"
}

case "${1:-}" in
	"") ;;
	--uninstall)
		uninstall
		exit 0
		;;
	--completions)
		[ -x "$TARGET" ] || die "$TARGET is not installed"
		install_completions
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
# GitHub's latest release is never a pre-release, while its list of releases
# starts with the newest of either kind.
channel="${WDX_CHANNEL:-stable}"
case "$channel" in
	stable) latest_api=releases/latest latest_file=LATEST ;;
	dev) latest_api='releases?per_page=1' latest_file=LATEST-dev ;;
	*) die "unknown WDX_CHANNEL '$channel' (stable or dev)" ;;
esac
if [ -n "${WDX_VERSION:-}" ]; then
	tag="v${WDX_VERSION#v}"
elif [ "$BASE" = "$GITHUB_BASE" ]; then
	tag="$(curl -fsSL "https://api.github.com/repos/$REPO/$latest_api" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)" ||
		die "failed to resolve the latest $channel release"
else
	tag="$(curl -fsSL "$BASE/$latest_file")" || die "failed to read $BASE/$latest_file"
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
		# shellcheck disable=SC2016 # a line for the user's profile
		printf '    export PATH="%s:$PATH"\n' "$BIN_DIR" >&2
		;;
esac

install_completions

# --- settings -------------------------------------------------------------
# The installed wdx writes the config file: every setting it has, keeping
# the values an earlier file sets.
mkdir -p "$HOME_DIR"
chmod 0700 "$HOME_DIR"
if wdx settings write --color never >/dev/null; then
	info "the settings in $HOME_DIR/config.yaml list every setting of this version"
else
	warn "could not write the settings in $HOME_DIR/config.yaml; fix the problems above, then run: $NAME settings write"
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
# Stopping or restarting the daemon stops only the daemon: terminals' holders
# run in its cgroup and must outlive it.
KillMode=process

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
	<!-- Terminals' holders must outlive the daemon. -->
	<key>AbandonProcessGroup</key>
	<true/>
	<!-- Stopping waits for a change in flight (30 s) and one it cancels
	     (10 s); launchd's default 20 s would kill the daemon midway. -->
	<key>ExitTimeOut</key>
	<integer>60</integer>
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
