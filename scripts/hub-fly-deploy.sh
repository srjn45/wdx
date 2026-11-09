#!/bin/sh
# Deploy wdx-hub on Fly.io from a clean checkout of a release tag.
#
#   make hub-fly-deploy APP=<name>
#
# Snapshots the app's volume when it has a machine already, deploys the
# tag with deploy/hub/fly.toml, built by Fly's builder from
# deploy/hub/Dockerfile, and waits until https://<name>.fly.dev answers the
# tag's version. It needs flyctl, a Fly account and the network, so it never
# runs in CI or in tests. The wdx doc HUB_FLY designs it.
#
# Env:
#   APP    the Fly app's name, as fly apps create made it
set -eu

info() { printf '==> %s\n' "$*"; }
warn() { printf 'warn: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# How long the new version has to answer, and how often it is asked.
WAIT_SECONDS=300
POLL_SECONDS=5

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# --- checks, before anything reaches Fly ----------------------------------
if command -v flyctl >/dev/null 2>&1; then
	FLY=flyctl
elif command -v fly >/dev/null 2>&1; then
	FLY=fly
else
	die "flyctl is not on the PATH: install it (https://fly.io/docs/flyctl/install/) and run fly auth login"
fi

APP="${APP:-}"
[ -n "$APP" ] || die "APP is not set: run make hub-fly-deploy APP=<name>, the name fly apps create made"

[ -z "$(git status --porcelain)" ] || die "the tree has changes: deploy from a clean checkout of a release tag (git status lists them)"

tag="$(git describe --tags --exact-match --match 'v*' HEAD 2>/dev/null)" ||
	die "HEAD is not a release tag: git checkout v<version> first, since a hub's version is always a release's"
VERSION="${tag#v}"
COMMIT="$(git rev-parse --short HEAD)"
URL="https://$APP.fly.dev"

# --- the snapshot ---------------------------------------------------------
# A deploy across a breaks-older migration can be rolled back only onto the
# database as it was before it.
machines="$("$FLY" machines list --app "$APP" --json)" || die "could not list the machines of $APP: is it the name fly apps create made?"
if printf '%s' "$machines" | grep -q '"id"'; then
	volumes="$("$FLY" volumes list --app "$APP" --json)" || die "could not list the volumes of $APP"
	ids="$(printf '%s' "$volumes" | grep -o '"id": *"vol_[^"]*"' | sed 's/.*"\(vol_[^"]*\)"/\1/')"
	[ -n "$ids" ] || die "$APP has a machine but no volume: fly volumes list --app $APP"
	for volume in $ids; do
		snapshot="$("$FLY" volumes snapshots create "$volume" --app "$APP" --json)" ||
			die "could not snapshot the volume $volume; nothing was deployed"
		id="$(printf '%s' "$snapshot" | grep -o '"id": *"[^"]*"' | head -n 1 | sed 's/.*"\([^"]*\)"/\1/')"
		if [ -n "$id" ]; then
			info "snapshot $id of the volume $volume taken; restore it with fly volumes create --snapshot-id $id"
		else
			warn "snapshot of the volume $volume requested, its id not known yet: fly volumes snapshots list $volume --app $APP"
		fi
	done
else
	info "$APP has no machine yet: no volume to snapshot"
fi

# --- the deploy -----------------------------------------------------------
# The repository's root is the build's context, as the Dockerfile wants; the
# ignore file beside it leaves out what the build does not need.
info "deploying $tag ($COMMIT) to $APP"
"$FLY" deploy "$ROOT" --app "$APP" \
	--config "$ROOT/deploy/hub/fly.toml" \
	--dockerfile "$ROOT/deploy/hub/Dockerfile" \
	--ignorefile "$ROOT/deploy/hub/Dockerfile.dockerignore" \
	--ha=false \
	--build-arg "VERSION=$VERSION" \
	--build-arg "COMMIT=$COMMIT" ||
	die "fly deploy failed; fly releases --app $APP --image lists the images to roll back to"

# --- the wait -------------------------------------------------------------
info "waiting for $URL/v1/version to answer $VERSION"
waited=0
answer=
while :; do
	answer="$(curl -fsS --max-time 10 "$URL/v1/version" 2>&1)" || true
	case "$answer" in
		*"\"version\":\"$VERSION\""*)
			info "$APP serves $VERSION at $URL"
			exit 0
			;;
	esac
	[ "$waited" -lt "$WAIT_SECONDS" ] ||
		die "$URL/v1/version did not answer $VERSION within ${WAIT_SECONDS}s; its last answer: $answer"
	sleep "$POLL_SECONDS"
	waited=$((waited + POLL_SECONDS))
done
