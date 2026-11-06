#!/bin/sh
# Step 1 of the wdx doc MAC_SIGNING: checks, by hand on a Mac, that a
# self-signed wdx keeps its macOS permissions across an update. Fetch it on
# the Mac with:
#
#   curl -fsSLO https://raw.githubusercontent.com/srjn45/wdx/main/scripts/mac-signing-check.sh
#
#   sh mac-signing-check.sh setup     make the certificate, sign the installed
#                                     wdx, restart the daemon on it
#   sh mac-signing-check.sh switch    install the newest dev release, signed,
#                                     the way wdx update will: stop, replace,
#                                     start with launchctl kickstart
#   sh mac-signing-check.sh switch 0.4.0-dev.160   a given release
#
# Run it in Terminal.app on the Mac itself: granting Full Disk Access and
# watching for prompts need the screen. The key goes in a keychain of its own,
# ~/.wdx/codesign/wdx.keychain-db, so codesign should show no dialog: if one
# shows, note it. Everything it prints is also appended to
# ~/wdx-signing-check.log: send that file back.
#
# The binary it replaces is kept in ~/.wdx/signing-check/. wdx update
# --rollback does not go back across a switch made by this script.
set -eu

NAME=wdx
REPO=srjn45/wdx
BASE="https://github.com/$REPO/releases/download"
LABEL="com.github.srjn45.$NAME"
IDENTITY="$NAME-codesign"
# A keychain of its own, as the doc's revision 3 has it: wdx unlocks it with
# its password file, so codesign shows no dialog.
SIGN_DIR="$HOME/.wdx/codesign"
KEYCHAIN="$SIGN_DIR/wdx.keychain-db"
PASSWORD_FILE="$SIGN_DIR/password"
LOG="$HOME/wdx-signing-check.log"
KEEP="$HOME/.wdx/signing-check"

[ "$(uname -s)" = Darwin ] || { echo "error: run this on macOS" >&2; exit 1; }

# Everything goes to the screen and to the log.
if [ -z "${WDX_CHECK_LOGGING:-}" ]; then
	export WDX_CHECK_LOGGING=1
	{ echo; echo "===== $(date '+%Y-%m-%d %H:%M:%S') sh $0 $*"; } >>"$LOG"
	set +e
	sh "$0" "$@" 2>&1 | tee -a "$LOG"
	status=$?
	set -e
	echo "(appended to $LOG)"
	exit "$status"
fi

step() { printf '\n==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; exit 1; }

TARGET="$(command -v "$NAME" || true)"
[ -n "$TARGET" ] || fail "no $NAME on PATH; install it first"
uid="$(id -u)"

show_binary() {
	info "path: $1"
	info "version: $("$1" version --json 2>/dev/null | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' | head -n 1)"
	codesign -dv "$1" 2>&1 | grep -E '^(Identifier|Signature|Authority|TeamIdentifier|CDHash)' | sed 's/^/    /' || true
	info "designated requirement: $(codesign -d -r- "$1" 2>&1 | sed -n 's/^designated => //p')"
}

show_launchd() {
	launchctl print "gui/$uid/$LABEL" 2>&1 |
		grep -E '^[[:space:]]*(state|pid|last exit code|last exit reason|program) =' | sed 's/^[[:space:]]*/    /' ||
		info "launchctl print found no $LABEL"
}

# sign copies $1 to $2, signs and verifies the copy.
sign() {
	cp "$1" "$2"
	security unlock-keychain -p "$(cat "$PASSWORD_FILE")" "$KEYCHAIN" || fail "could not unlock $KEYCHAIN"
	codesign --force --keychain "$KEYCHAIN" --sign "$IDENTITY" --identifier "$LABEL" "$2" ||
		fail "codesign failed"
	info "if macOS showed a dialog asking whether codesign may use the key, that is a FINDING: write it down"
	codesign --verify --strict "$2" || fail "codesign --verify failed"
	info "signed and verified"
}

# swap stops the daemon, puts $1 in place of the installed wdx, and starts it,
# as wdx update does: launchctl kill SIGTERM, then launchctl kickstart.
swap() {
	mkdir -p "$KEEP"
	old="$KEEP/$NAME.$(date +%Y%m%d-%H%M%S)"
	cp "$TARGET" "$old"
	info "kept the replaced binary as $old"

	step "stopping the daemon (wdx daemon stop)"
	"$TARGET" daemon stop || fail "wdx daemon stop failed"
	show_launchd

	step "replacing $TARGET"
	cp "$1" "$TARGET.tmp.$$"
	chmod 0755 "$TARGET.tmp.$$"
	mv -f "$TARGET.tmp.$$" "$TARGET"
	"$TARGET" settings write --color never >/dev/null || info "warning: wdx settings write failed"

	step "starting the daemon (wdx daemon start, which runs launchctl kickstart)"
	if "$TARGET" daemon start; then
		info "daemon started"
	else
		info "DAEMON DID NOT START: this is the exit 78 case if the line below says so"
		show_launchd
		info "to put the old binary back: cp $old $TARGET && wdx daemon start"
		exit 1
	fi
	show_launchd
	"$TARGET" daemon status || true
	show_binary "$TARGET"
}

ask_probe() {
	cat <<EOF

==> now check the prompt, by hand
    1. Open a NEW terminal in wdx (tide, or: wdx terminal create in a project),
       not one opened before this run.
    2. In it, run:   ls ~/Downloads ~/Documents ~/Desktop
    3. Note whether macOS asked "wdx would like to access…", and whether
       the ls listed the files or said "Operation not permitted".
EOF
	printf '\n    Did macOS ask? (y/n) '
	read -r asked
	printf '    Did ls list the files? (y/n) '
	read -r listed
	info "answers: asked=$asked listed=$listed"
}

setup() {
	step "this Mac"
	info "macOS $(sw_vers -productVersion) ($(sw_vers -buildVersion)), $(uname -m)"
	info "openssl: $(command -v openssl || echo none) — $(openssl version 2>&1 || true)"
	if openssl pkcs12 -help 2>&1 | grep -q -- '-legacy'; then legacy=-legacy; info "pkcs12 has -legacy (OpenSSL 3)"; else legacy=; info "pkcs12 has no -legacy (LibreSSL)"; fi

	step "the installed wdx, before"
	show_binary "$TARGET"
	show_launchd

	step "the certificate $IDENTITY"
	if [ -f "$KEYCHAIN" ] && security find-certificate -c "$IDENTITY" "$KEYCHAIN" >/dev/null 2>&1; then
		info "already in $KEYCHAIN"
	else
		mkdir -p "$SIGN_DIR"
		chmod 0700 "$SIGN_DIR"
		(umask 077 && openssl rand -hex 32 >"$PASSWORD_FILE") || fail "could not write $PASSWORD_FILE"
		password="$(cat "$PASSWORD_FILE")"
		rm -f "$KEYCHAIN"
		security create-keychain -p "$password" "$KEYCHAIN" || fail "security create-keychain failed"
		security set-keychain-settings "$KEYCHAIN" || fail "security set-keychain-settings failed"
		info "made the keychain $KEYCHAIN"
		tmp="$(mktemp -d "${TMPDIR:-/tmp}/$NAME-cert.XXXXXX")"
		trap 'rm -rf "$tmp"' EXIT
		cat >"$tmp/req.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = v3
prompt = no
[dn]
CN = $IDENTITY
[v3]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF
		openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
			-keyout "$tmp/key.pem" -out "$tmp/cert.pem" -config "$tmp/req.cnf" 2>&1 | sed 's/^/    /' ||
			fail "openssl req failed"
		# shellcheck disable=SC2086 # $legacy is empty or one flag
		openssl pkcs12 -export $legacy -inkey "$tmp/key.pem" -in "$tmp/cert.pem" \
			-out "$tmp/cert.p12" -passout pass:wdx-import -name "$IDENTITY" || fail "openssl pkcs12 failed"
		security import "$tmp/cert.p12" -k "$KEYCHAIN" -P wdx-import -T /usr/bin/codesign 2>&1 | sed 's/^/    /' ||
			fail "security import failed"
		security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" "$KEYCHAIN" >/dev/null ||
			fail "security set-key-partition-list failed"
		info "made the certificate and let codesign use its key"
	fi
	info "identities in $KEYCHAIN (self-signed ones may show as not valid; codesign uses them anyway):"
	security find-identity -p codesigning "$KEYCHAIN" 2>&1 | sed 's/^/    /'

	step "signing the installed wdx"
	tmpbin="$(mktemp "${TMPDIR:-/tmp}/$NAME-signed.XXXXXX")"
	sign "$TARGET" "$tmpbin"
	swap "$tmpbin"
	rm -f "$tmpbin"

	cat <<EOF

==> grant Full Disk Access once
    System Settings → Privacy & Security → Full Disk Access → "+"
    → Cmd+Shift+G → paste:  $TARGET
    and turn it on. If wdx is listed there already, remove it and add it again.
EOF
	printf '\n    Press Enter once it is granted. '
	read -r _
	ask_probe
	info "setup done. After the next dev release, run: sh $0 switch"
}

switch() {
	version="${1:-}"
	if [ -z "$version" ]; then
		tag="$(curl -fsSL "https://raw.githubusercontent.com/$REPO/channels/LATEST-dev")" || fail "could not read the latest dev release"
		version="${tag#v}"
	fi
	version="${version#v}"
	current="$("$TARGET" version --json | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' | head -n 1)"
	[ "$version" != "$current" ] || fail "$version is installed already; run this again after the next release, or name one: sh $0 switch <version>"
	security find-certificate -c "$IDENTITY" "$KEYCHAIN" >/dev/null 2>&1 || fail "no $IDENTITY certificate in $KEYCHAIN; run: sh $0 setup"

	step "the installed wdx, before"
	show_binary "$TARGET"

	case "$(uname -m)" in arm64) arch=arm64 ;; *) arch=amd64 ;; esac
	archive="${NAME}_${version}_darwin_${arch}.tar.gz"
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/$NAME-switch.XXXXXX")"
	trap 'rm -rf "$tmp"' EXIT
	step "downloading $NAME $version"
	curl -fsSL "$BASE/v$version/$archive" -o "$tmp/$archive" || fail "download of $archive failed"
	curl -fsSL "$BASE/v$version/checksums.txt" -o "$tmp/checksums.txt" || fail "download of checksums.txt failed"
	expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
	[ "$expected" = "$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')" ] || fail "checksum mismatch"
	tar -xzf "$tmp/$archive" -C "$tmp" "$NAME"
	info "as downloaded:"
	show_binary "$tmp/$NAME"

	step "signing it"
	sign "$tmp/$NAME" "$tmp/$NAME.signed"
	swap "$tmp/$NAME.signed"
	info "the designated requirement above should read the same as before the switch"
	ask_probe
	info "switch done: send $LOG back"
}

case "${1:-}" in
	setup) setup ;;
	switch) shift; switch "${1:-}" ;;
	*) echo "usage: sh $0 setup | switch [version]" >&2; exit 2 ;;
esac
