# Releasing wdx

A version is a git tag; there is no version file. The build reads it with `git describe`, so a build between releases is named after the last tag and the commits since (`0.2.0-dev.4-3-gabc1234`).

```
make release-dev   tag HEAD as the next dev release, build it into dist/ and push it
make release       commit CHANGELOG.md, tag the next stable release, build it into dist/ and push it
make changelog     print what changed since the last stable release
make notices       write THIRD-PARTY-NOTICES.md from the modules linked into wdx
```

While wdx builds its vt from our fork, run `make vt-sync` before releasing, so a release has upstream's latest vt (docs/VT.md).

Both release targets need a clean working tree on `main`, not behind `origin/main`, and a passing `make check` and `make test`, and refuse when there is nothing to release. They end by pushing `main` and the tag together.

## Publishing

`.github/workflows/release.yml` publishes the GitHub Release of each `v*` tag pushed: it runs the jobs of `ci.yml` on the tag, then `scripts/publish.sh`, which checks that the tag is annotated and on `main`, builds the archives with the tag's own `scripts/dist.sh`, and uploads them with `checksums.txt` and the tag's `install.sh`. The tag's notes are the release's. A dev tag is a pre-release, so GitHub's latest release is always the newest stable one. Once it is public, `scripts/publish-latest.sh` writes the tag to `LATEST-dev` on the `channels` branch, which holds nothing else, and which only ever moves forward. A failed run leaves a draft at most; running it again replaces it, and on a release already published does only that last step. A tag pushed before releases were published is published by running the workflow by hand with that tag (`gh workflow run release.yml -f tag=v0.2.0-dev.20`), without the checks.

Each release has two archives per platform (linux and macOS, amd64 and arm64), both listed in `checksums.txt`:

```
wdx_<version>_<os>_<arch>.tar.gz       wdx
wdx-hub_<version>_<os>_<arch>.tar.gz   wdx-hub, the hub (docs/HUB_ARCH.md; deploy/hub runs it)
```

Both binaries carry the same version and commit, and each archive the same `LICENSE`, `NOTICE` and `THIRD-PARTY-NOTICES.md`. `scripts/install.sh`, `make install` and `wdx update` install wdx only. `scripts/install-hub.sh` installs the hub, on Linux with systemd, from the same releases: the same `checksums.txt`, `LATEST`/`LATEST-dev` and `WDX_CHANNEL`, `WDX_VERSION` and `WDX_DOWNLOAD_BASE`, so a release holds both programs and nothing is published for the hub alone; `wdx-hub update` updates it. The hub also runs from its archive, its container image or its systemd unit by hand (`deploy/hub/README.md`).

`dist/` keeps every release built here: `make install` installs from it, and `update.base_url: file://<repo>/dist` makes `wdx update` read it instead of GitHub.

The first `make` that needs git-cliff downloads the pinned version into `bin/` and checks it against the checksums in `scripts/fetch-release.sh`.

## Versions

Versions follow [Semantic Versioning](https://semver.org), worked out by [git-cliff](https://git-cliff.org) (`cliff.toml`) from the [Conventional Commits](https://www.conventionalcommits.org) since the last stable tag:

| Commit | Bump before 1.0 | Bump from 1.0 |
|---|---|---|
| `feat!:` or a `BREAKING CHANGE:` footer | minor | major |
| `feat:` | minor | minor |
| `fix:`, `perf:` | patch | patch |
| `docs:`, `test:`, `refactor:`, `chore:` | none | none |

A breaking change is anything a user or script can notice: the `--json` output, error codes, exit codes, commands and flags, settings, and the API. The first release is `v0.1.0`; 1.0 comes when that contract is stable.

## Channels

| Channel | Tag | Made by | Installed by |
|---|---|---|---|
| stable | `vX.Y.Z` | `make release` | default |
| dev | `vX.Y.Z-dev.N` | `make release-dev` | `WDX_CHANNEL=dev` |

A dev release is tagged with the stable version it leads to and `N`, the number of commits since the last stable tag, so `v0.2.0-dev.7` comes before `v0.2.0`. Dev releases may be buggy. A stable release usually promotes a commit that has run as a dev release: `make release` commits only `CHANGELOG.md` on top of it and tags that commit.

The dev channel installs the newest release of either kind, so dev users get each stable release too. `dist.sh` writes `dist/LATEST-dev` for every release and `dist/LATEST` for stable ones only; on GitHub, stable comes from its latest release, which is never a pre-release, and dev from `LATEST-dev` on the `channels` branch, as nothing of GitHub's says which dev release is the newest: its list of releases is in no promised order, and has held a new release below older ones. That file's raw URL is cached for up to 5 minutes, so a release can take that long to be offered.

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh                   # stable
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | WDX_CHANNEL=dev sh   # dev
```

The script comes from `main` rather than from a release, since GitHub's `releases/latest/download/` answers 404 until there is a stable release. The hub's comes from `main` too, and is run as root:

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install-hub.sh | sudo sh
```

The stable channel's latest release may have no `wdx-hub` archive yet; the script then says to use `WDX_CHANNEL=dev`. It saves the channel as the hub's `update.channel`, and `WDX_DOWNLOAD_BASE` as its `update.base_url`.

`WDX_VERSION` installs one release, whatever the channel. `WDX_CHANNEL=dev` also saves `update.channel: dev` in the config file, so `wdx update` goes on following dev; an install without it leaves the setting as it is. `wdx setting set --name update.channel --value dev` does the same for a wdx already installed.

## Updating

An installed wdx updates itself:

```
wdx update                        show the installed version, the newest on the channel and whether it is newer
wdx update --apply                install the newest release on the channel
wdx update --apply --version X    install release X, which must not be older
wdx update --rollback             install the version the last update replaced
```

The channel is the `update.channel` setting (`stable` by default), or `--channel` for one run. Releases come from `update.base_url`: GitHub Releases by default, or `file://<repo>/dist` to update from local builds. Only a newer version installs: to go back, roll back, which also means a dev release waits for the next stable one rather than switching down to an older one.

`--apply` downloads the archive and checks it against `checksums.txt`, then, before touching anything, runs the new binary's `settings write` on the config file into a scratch file and its `version` on that file. A value the new version does not accept stops the update there (`settings_invalid`). Then it stops the daemon, keeps the installed binary, a backup of the database and the config file in `~/.wdx/previous/`, installs the new binary and the config file written anew, and starts the daemon on them, which migrates the database. On macOS, the new binary is signed before the daemon is stopped, so that the permissions macOS grants wdx stay (docs/DAEMON.md); when signing fails, the update goes on unsigned, with a warning. If any of those steps fails, the old binary, database and config file are put back and the daemon starts on them again (`update_failed`, with `undone` in its details). One update or rollback runs at a time.

Terminals and agents run on through an update, each in its own `wdx holder` process of the version that started it (docs/DAEMON.md), so a change in how a terminal's screen or keys work reaches a terminal only when it is started anew. A shell: close it and open another. An agent: `wdx agent restart`, or `R` ("new session") in tide, which stops its holder and starts a new one; `/exit` in it then `wdx agent start` does the same. `wdx project close --stop-running` then `wdx project open` does every terminal and agent of a project at once.

The config file written anew lists every setting of the new version with its description: the values set stay as written, new settings appear commented with their defaults, and keys the version no longer has are moved, commented, to the end, so it still starts. Comments of the user's own are not kept. A rollback puts back the config file as it was before the update.

Only one previous version is kept, replaced by each update and removed by a rollback. A rollback keeps the database when the previous version can use it: migrations that only add things leave it usable. A migration marked `-- breaks-older: true` records itself in the database as the oldest schema a binary must know; rolling back across it can only restore the backup taken at the update, losing every change since, so it asks first: `--discard-changes` answers yes and `--cancel` no, and `--json` needs one of them; no fails with `restore_required`.

`make install` and `scripts/install.sh` install without keeping a previous version.

### The hub

An installed hub updates itself in the same way, on the same `internal/update`, with the same flags, results and JSON:

```
wdx-hub update                        the check, as wdx update
sudo wdx-hub update --apply           install the newest release on the hub's update.channel
sudo wdx-hub update --rollback        install the version the last update replaced
```

`--apply` and `--rollback` need root (`root_required`) and the layout `install-hub.sh` makes (`not_installed`), both exit 2. What differs from wdx's: the database is backed up through the running hub (`POST /v1/backup`) to `<home>/backups/hub-<old version>.db`, the last 3 kept, before the hub stops; the hub is stopped and started with `systemctl`, after its unit is written anew from the new binary's `service unit` and `daemon-reload`, and a start waits on the socket for the new version; the previous version is kept in `/usr/local/lib/wdx-hub/previous/`, beside the staging directories, so nothing is renamed across filesystems. Run as root, every other `wdx-hub` command goes on as the hub's user, `wdx-hub`; `update --apply` and `--rollback` keep root and run the new binary as that user. Running `install-hub.sh` again on an installed hub runs the installed `wdx-hub update --apply`.

## Licenses

wdx is licensed under Apache 2.0 (`LICENSE`, `NOTICE`). Every archive carries `LICENSE`, `NOTICE` and `THIRD-PARTY-NOTICES.md` next to the binary: the full license and notice files of Go and of every module linked into wdx, as their licenses ask of a binary release. `scripts/notices.sh` writes it from `go list`, so it changes with the dependencies; a release refuses to run while it is out of date, and the script fails on a license it does not know, so a new dependency's license is looked at before it ships.

## Tags and the changelog

Tags are annotated, their message holding the release's notes, and are never moved or deleted: a broken release is fixed by the next one. `CHANGELOG.md` is generated: change a commit message's wording before releasing, never the file.

## CI

`.github/workflows/ci.yml` runs on every push to `main`, every pull request, every tag before it is published, weekly and on demand:

```
make cover         on Linux: the tests, as make test runs them, with coverage: bin/cover.html and a
                   summary per package; under the same 3 GB memory cap, in a user systemd CI starts
make test-update   on Linux: a wdx built from the tree updated to a release dist.sh builds of it, and
                   rolled back
make test-hub-install
                   on Linux, as root through sudo with systemd: wdx-hub installed by install-hub.sh
                   from releases dist.sh builds, updated, a release that never serves put back, rolled
                   back, updated by the script again and uninstalled; CI only, never on a developer's
                   machine
make check         on Linux: golangci-lint (.golangci.yml), shellcheck and actionlint; a tidy and verified
                   go.mod; current notices; govulncheck; go vet for macOS and Linux arm64; the commit
                   messages in RANGE
```

Locally `make check` reads the commits in `origin/main..HEAD`; CI reads those of the push or pull request, and none for a tag, whose commits were checked on `main`. A push whose previous commit is not in the history, as after a force push, has its last commit checked. The linters are pinned in the `Makefile` and fetched into `bin/` on first use: the Go tools built by `go install`, shellcheck checked against the checksums in `scripts/fetch-release.sh`. The weekly run catches vulnerabilities published since the last push; a fix in the standard library means raising the `go` line of `go.mod`, which the release builds then use.
