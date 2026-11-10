#!/bin/sh
# Install wdx-hub from a release, as a system service.
#
#   curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install-hub.sh | sudo sh
#
# Linux with systemd only, run as root. Downloads the archive for this arch,
# verifies its checksum, makes the system user wdx-hub with its home in
# /var/lib/wdx-hub, asks the settings that have no default (public_url and
# how the web endpoint gets its TLS), makes the master key, installs the
# binary in /usr/local/bin, the completion of every shell found among bash,
# zsh and fish for every user, and the service wdx-hub.service, starts it,
# and prints an invite for the first user. With the hub installed already it
# updates it instead, through the installed wdx-hub update --apply. The wdx
# doc HUB_INSTALL designs it.
#
# Env:
#   WDX_CHANNEL           stable (default) installs the latest stable release;
#                         dev the latest release of either kind; saved as the
#                         channel wdx-hub update follows
#   WDX_VERSION           release to install (e.g. 0.5.0), whatever the channel
#   WDX_DOWNLOAD_BASE     where releases live; default: GitHub Releases. Saved
#                         as the hub's update.base_url, which wdx-hub update
#                         --apply, as root, takes only from its environment:
#                         run again, the script gives it there
#   WDX_HUB_PUBLIC_URL    the address browsers and nodes reach the hub at,
#                         instead of asking
#   WDX_HUB_WEB_TLS       off (a proxy in front terminates TLS) or files,
#                         instead of asking
#   WDX_HUB_WEB_TLS_CERT  with files, the certificate's absolute path
#   WDX_HUB_WEB_TLS_KEY   with files, the private key's absolute path
#
# Flags: --uninstall      stop and remove the service, the binary, the
#                         completions and /usr/local/lib/wdx-hub; the user and
#                         its home stay
#        --purge          --uninstall, then remove the user and its home, with
#                         the hub's accounts, docs and backups, after asking
#        --purge --yes    the same without asking
set -eu

NAME=wdx-hub
REPO=srjn45/wdx
GITHUB_BASE="https://github.com/$REPO/releases/download"
BASE="${WDX_DOWNLOAD_BASE:-$GITHUB_BASE}"

# The installed layout, hubconfig.Installed: wdx-hub update updates a hub
# installed so.
HUB_USER=wdx-hub
HUB_DIR=/var/lib/wdx-hub
HUB_HOME="$HUB_DIR/.$NAME"
TARGET=/usr/local/bin/$NAME
UNIT=/etc/systemd/system/$NAME.service
LIB=/usr/local/lib/$NAME
# The shells' completions for every user, hubconfig.Installed.Completions:
# wdx-hub update --apply writes them again.
BASH_COMPLETION=/usr/share/bash-completion/completions/$NAME
ZSH_COMPLETION=/usr/local/share/zsh/site-functions/_$NAME
FISH_COMPLETION=/usr/share/fish/vendor_completions.d/$NAME.fish

# The PATH the hub's commands run with, whatever the caller's.
SAFE_PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
PATH="$SAFE_PATH:$PATH"
export PATH

info() { printf '==> %s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# as_hub runs a command as the hub's user, in a clean environment with the
# user's home: the WDX_HUB_ variables this script reads are not the hub's.
as_hub() {
	(cd / && runuser -u "$HUB_USER" -- env -i HOME="$HUB_DIR" PATH="$SAFE_PATH" "$@")
}

# has_terminal is true when the questions can be asked: the script's stdin is
# curl's pipe, so they are read from /dev/tty.
has_terminal() { (: </dev/tty) 2>/dev/null; }

# ask prints question $1 on the terminal and reads the answer into the
# variable $2; with no terminal it fails, naming the variable $3 that gives
# the answer instead.
ask() {
	has_terminal || die "no terminal to ask $1; set $3"
	printf '%s' "$1: " >/dev/tty
	read -r answer </dev/tty || die "no answer to $1"
	eval "$2=\$answer"
}

[ "$(uname -s)" = Linux ] || die "wdx-hub installs on Linux with systemd only; elsewhere run wdx-hub serve in a terminal"
if [ ! -d /run/systemd/system ] || ! command -v systemctl >/dev/null 2>&1; then
	die "systemd is not running here; the hub installs as a systemd service"
fi
[ "$(id -u)" -eq 0 ] || die "the hub installs as a system service, which needs root: run it with sudo sh"

# --- uninstall ------------------------------------------------------------
uninstall() {
	if [ -f "$UNIT" ]; then
		systemctl disable --now "$NAME.service" >/dev/null 2>&1 || warn "could not stop $NAME.service"
		rm -f "$UNIT"
		systemctl daemon-reload || true
		info "removed the service $NAME.service"
	fi
	rm -f "$TARGET"
	rm -rf "$LIB"
	info "removed $TARGET and $LIB"
	for f in "$BASH_COMPLETION" "$ZSH_COMPLETION" "$FISH_COMPLETION"; do
		if [ -f "$f" ]; then
			rm -f "$f"
			info "removed the completion $f"
		fi
	done
}

purge() {
	if [ "${1:-}" != --yes ]; then
		has_terminal || die "--purge asks before removing the hub's data, and there is no terminal; to remove it without asking: --purge --yes"
		printf 'remove the user %s and %s, with every account, doc and backup of the hub? [y/N] ' "$HUB_USER" "$HUB_DIR" >/dev/tty
		read -r answer </dev/tty || answer=
		case "$answer" in
			y | Y | yes) ;;
			*) die "cancelled; nothing removed" ;;
		esac
	fi
	uninstall
	if id -u "$HUB_USER" >/dev/null 2>&1; then
		userdel "$HUB_USER" || die "could not remove the user $HUB_USER"
		info "removed the user $HUB_USER"
	fi
	rm -rf "$HUB_DIR"
	info "removed $HUB_DIR, the hub's database, certificate authority, settings and backups"
}

case "${1:-}" in
	"") ;;
	--uninstall)
		[ $# -eq 1 ] || die "unknown argument: $2"
		uninstall
		info "the user $HUB_USER and $HUB_DIR stay: the database, the certificate authority, the master key and the backups; --purge removes them"
		exit 0
		;;
	--purge)
		case "$#:${2:-}" in
			1: | 2:--yes) ;;
			*) die "unknown argument: $2" ;;
		esac
		purge "${2:-}"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
esac

# --- update ---------------------------------------------------------------
# One way to update, wdx-hub update, so that the script installs only.
if [ -x "$TARGET" ] && [ -f "$UNIT" ]; then
	info "$NAME is installed: updating it with $NAME update --apply"
	set -- update --apply
	[ -n "${WDX_CHANNEL:-}" ] && set -- "$@" --channel "$WDX_CHANNEL"
	[ -n "${WDX_VERSION:-}" ] && set -- "$@" --version "${WDX_VERSION#v}"
	set -- "$TARGET" "$@"
	# For this run only, as --channel and --version are.
	if [ -n "${WDX_DOWNLOAD_BASE:-}" ]; then
		set -- WDX_HUB_UPDATE_BASE_URL="$WDX_DOWNLOAD_BASE" "$@"
	fi
	exec env -i PATH="$SAFE_PATH" TERM="${TERM:-dumb}" ${NO_COLOR:+NO_COLOR=1} "$@"
fi

for tool in curl tar useradd runuser; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

# --- platform -------------------------------------------------------------
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture '$arch' (amd64 and arm64 only)" ;;
esac

# --- version --------------------------------------------------------------
# As install.sh resolves wdx's: the same releases hold both programs.
channel="${WDX_CHANNEL:-stable}"
case "$channel" in
	stable) latest_file=LATEST ;;
	dev) latest_file=LATEST-dev ;;
	*) die "unknown WDX_CHANNEL '$channel' (stable or dev)" ;;
esac
if [ -n "${WDX_VERSION:-}" ]; then
	tag="v${WDX_VERSION#v}"
elif [ "$BASE" != "$GITHUB_BASE" ]; then
	tag="$(curl -fsSL "$BASE/$latest_file")" || die "failed to read $BASE/$latest_file"
elif [ "$channel" = dev ]; then
	tag="$(curl -fsSL "https://raw.githubusercontent.com/$REPO/channels/$latest_file")" ||
		die "failed to resolve the latest $channel release"
else
	tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)" ||
		die "failed to resolve the latest $channel release"
fi
[ -n "$tag" ] || die "could not determine which version to install"
version="${tag#v}"

# --- download & verify ----------------------------------------------------
archive="${NAME}_${version}_linux_${arch}.tar.gz"
# In root's directory of the hub, which update stages its downloads in too:
# the hub's user runs the new binary from it before it is installed, which a
# private or noexec TMPDIR would not let it.
mkdir -p "$LIB"
chmod 0711 "$LIB"
tmp="$(mktemp -d "$LIB/install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$BASE/$tag/checksums.txt" -o "$tmp/checksums.txt" || die "failed to download $BASE/$tag/checksums.txt"
expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
if [ -z "$expected" ]; then
	# Releases before the hub's have only wdx's archives.
	[ "$channel" = stable ] && [ -z "${WDX_VERSION:-}" ] &&
		die "the latest stable release, $tag, has no $NAME; install the newest dev release with WDX_CHANNEL=dev"
	die "release $tag has no $archive"
fi

info "downloading $NAME $tag (linux/$arch)"
curl -fsSL "$BASE/$tag/$archive" -o "$tmp/$archive" || die "failed to download $BASE/$tag/$archive"
actual="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive"
tar -xzf "$tmp/$archive" -C "$tmp" "$NAME" || die "failed to extract $archive"
chmod 0711 "$tmp"
new="$tmp/$NAME"

# --- the hub's user -------------------------------------------------------
if id -u "$HUB_USER" >/dev/null 2>&1; then
	home="$(getent passwd "$HUB_USER" | cut -d: -f6)"
	[ "$home" = "$HUB_DIR" ] || die "the user $HUB_USER exists with the home $home; the hub's must be $HUB_DIR"
else
	nologin="$(command -v nologin || echo /bin/false)"
	useradd --system --user-group --home-dir "$HUB_DIR" --no-create-home --shell "$nologin" "$HUB_USER" ||
		die "could not make the user $HUB_USER"
	info "made the system user $HUB_USER, its home $HUB_DIR"
fi
mkdir -p "$HUB_DIR"
chown "$HUB_USER:" "$HUB_DIR"
chmod 0700 "$HUB_DIR"

# --- settings -------------------------------------------------------------
# hub runs the new release as the hub's user, on its home.
hub() { as_hub "$new" "$@" --color never; }

# setting prints the value of setting $1 in the hub's home, and fails when it
# is not set in the file: an install over a hub set up by hand asks nothing it
# has a value for.
setting() {
	out="$(hub setting get --name "$1" 2>/dev/null)" || return 1
	printf '%s\n' "$out" | grep -q '^  from  *default$' && return 1
	printf '%s\n' "$out" | sed -n "1s/^$1 //p"
}

set_setting() {
	hub setting set --name "$1" --value "$2" >/dev/null || die "could not set $1"
}

if ! public_url="$(setting public_url)"; then
	public_url="${WDX_HUB_PUBLIC_URL:-}"
	[ -n "$public_url" ] ||
		ask "public url, where browsers and nodes reach this hub (such as https://hub.example.com)" public_url WDX_HUB_PUBLIC_URL
	set_setting public_url "$public_url"
fi

tls="$(setting web.tls)" || tls=
case "$public_url" in
	http://*) tls=off ;;
esac
if [ -z "$tls" ]; then
	tls="${WDX_HUB_WEB_TLS:-}"
	if [ -z "$tls" ]; then
		has_terminal || die "no terminal to ask how the web endpoint gets its TLS; set WDX_HUB_WEB_TLS to off (a proxy terminates it) or files"
		cat >/dev/tty <<EOF
how does the web endpoint get its TLS?
  1) a proxy in front of it terminates TLS (Caddy, nginx, a load balancer)
  2) certificate files on this machine
EOF
		choice=
		ask choice choice WDX_HUB_WEB_TLS
		case "$choice" in
			1) tls=off ;;
			2) tls=files ;;
			*) die "choice '$choice' is not 1 or 2" ;;
		esac
	fi
	case "$tls" in
		off)
			# Only the proxy, on this machine, reaches it.
			set_setting listen.web 127.0.0.1:8080
			;;
		files)
			cert="${WDX_HUB_WEB_TLS_CERT:-}"
			[ -n "$cert" ] || ask "certificate file, PEM with its chain (absolute path)" cert WDX_HUB_WEB_TLS_CERT
			key="${WDX_HUB_WEB_TLS_KEY:-}"
			[ -n "$key" ] || ask "private key file, PEM (absolute path)" key WDX_HUB_WEB_TLS_KEY
			for f in "$cert" "$key"; do
				as_hub test -r "$f" || die "the user $HUB_USER cannot read $f; give it read access, such as with chgrp $HUB_USER and chmod g+r"
			done
			set_setting web.tls_cert "$cert"
			set_setting web.tls_key "$key"
			set_setting listen.web :443
			;;
		*) die "unknown WDX_HUB_WEB_TLS '$tls' (off or files)" ;;
	esac
	set_setting web.tls "$tls"
fi

# Never printed, and in no command line, which the hub's log and ps show: the
# hub's secrets are sealed under it.
if ! setting master_key >/dev/null; then
	head -c 32 /dev/urandom | base64 | hub setting set --name master_key --value - >/dev/null ||
		die "could not set master_key"
	info "made the master key"
fi
set_setting update.channel "$channel"
[ -n "${WDX_DOWNLOAD_BASE:-}" ] && set_setting update.base_url "$WDX_DOWNLOAD_BASE"
hub setting write >/dev/null || die "could not write the settings in $HUB_HOME/config.yaml"
info "the settings in $HUB_HOME/config.yaml list every setting of this version"

# --- install --------------------------------------------------------------
# Copy then rename so that the binary is never half-written.
mkdir -p "$(dirname "$TARGET")"
cp "$new" "$TARGET.tmp.$$"
chmod 0755 "$TARGET.tmp.$$"
mv -f "$TARGET.tmp.$$" "$TARGET"

# write_completion writes the completion script of shell $1, as the installed
# binary prints it, to file $2, for every user.
write_completion() {
	command -v "$1" >/dev/null 2>&1 || return 1
	mkdir -p "$(dirname "$2")"
	if as_hub "$TARGET" completion "$1" >"$2.tmp.$$" && chmod 0644 "$2.tmp.$$" && mv -f "$2.tmp.$$" "$2"; then
		info "installed the $1 completion $2"
		return 0
	fi
	rm -f "$2.tmp.$$"
	warn "could not write the $1 completion to $2"
	return 1
}
completions=
if write_completion bash "$BASH_COMPLETION"; then
	completions=1
	[ -f /usr/share/bash-completion/bash_completion ] ||
		warn "the bash-completion package, which loads $BASH_COMPLETION, is not installed"
fi
write_completion zsh "$ZSH_COMPLETION" && completions=1
write_completion fish "$FISH_COMPLETION" && completions=1

# The release prints its own unit, as wdx-hub update writes it.
listen_web="$(hub setting get --name listen.web | sed -n '1s/^listen.web //p')"
set -- service unit
case "$listen_web" in
	*:[0-9] | *:[0-9][0-9] | *:[0-9][0-9][0-9] | *:10[01][0-9] | *:102[0-3]) set -- "$@" --privileged-port ;;
esac
hub "$@" >"$UNIT.tmp.$$" || die "$NAME could not print its service unit"
chmod 0644 "$UNIT.tmp.$$"
mv -f "$UNIT.tmp.$$" "$UNIT"
systemctl daemon-reload
systemctl enable --now "$NAME.service" >/dev/null || die "failed to start $NAME.service; see journalctl -u $NAME"
info "installed $NAME $version -> $TARGET, the service $NAME.service"

# --- wait -----------------------------------------------------------------
running=
for _ in $(seq 30); do
	running="$(as_hub "$TARGET" version --color never 2>/dev/null | grep '^hub ' | grep -v '^hub not running')" && break
	running=
	sleep 1
done
[ -n "$running" ] || die "$NAME did not answer within 30 seconds; see journalctl -u $NAME"
info "$NAME answers: $running"

# --- what is left to do ---------------------------------------------------
nodes_port="$(as_hub "$TARGET" setting get --name listen.nodes --color never | sed -n '1s/^listen.nodes .*://p')"
case "$tls" in
	off)
		case "$public_url" in
			http://*) info "the hub serves only this machine, at $public_url" ;;
			*) info "point the proxy at 127.0.0.1:8080, the web endpoint" ;;
		esac
		info "the node endpoint listens on :$nodes_port and must be reachable from the nodes, untouched by the proxy"
		;;
	files) info "open ports 443, the web endpoint, and $nodes_port, the node endpoint, in the firewall" ;;
esac

if as_hub "$TARGET" user list --color never | grep -q '^no users'; then
	invite="$(as_hub "$TARGET" user invite --color never)" || die "could not invite the first user; run: sudo $NAME user invite"
	until_when="$(printf '%s\n' "$invite" | sed -n '1s/.*until //p')"
	info "invite the first user with this link, which works once, until $until_when:"
	printf '%s\n' "$invite" | sed -n '2s/^/    /p'
fi
info "back up $HUB_HOME/config.yaml: it holds the master key, without which the hub's secrets are lost"
info "run admin commands with sudo: sudo $NAME user list"
if [ -n "$completions" ]; then
	info "Tab completes $NAME's commands and flags in a new shell"
fi
info "see whether a newer release is out with sudo $NAME update, and install it with sudo $NAME update --apply"
