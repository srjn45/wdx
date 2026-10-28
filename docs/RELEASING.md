# Releasing wdx

A version is a git tag; there is no version file. The build reads it with `git describe`, so a build between releases is named after the last tag and the commits since (`0.2.0-dev.4-3-gabc1234`).

```
make release-dev   tag HEAD as the next dev release and build it into dist/
make release       commit CHANGELOG.md, tag the next stable release and build it into dist/
make changelog     print what changed since the last stable release
```

Both release targets need a clean working tree on `main` and a passing `make test`, and refuse when there is nothing to release. Nothing is pushed: until the repository has a remote, `dist/` is the release, and `make install` installs from it. The first `make` that needs git-cliff downloads the pinned version into `bin/` and checks it against the checksums in `scripts/fetch-git-cliff.sh`.

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

The dev channel installs the newest release of either kind, so dev users get each stable release too. `dist.sh` writes `dist/LATEST-dev` for every release and `dist/LATEST` for stable ones only; on GitHub, the latest release is never a pre-release.

```sh
curl -fsSL https://github.com/srjn45/warden/releases/latest/download/install.sh | sh                   # stable
curl -fsSL https://github.com/srjn45/warden/releases/latest/download/install.sh | WDX_CHANNEL=dev sh   # dev
```

`WDX_VERSION` installs one release, whatever the channel.

## Updating

An installed wdx updates itself:

```
wdx update                        show the installed version, the newest on the channel and whether it is newer
wdx update --apply                install the newest release on the channel
wdx update --apply --version X    install release X, which must not be older
wdx update --rollback             install the version the last update replaced
```

The channel is the `update.channel` setting (`stable` by default), or `--channel` for one run. Releases come from `update.base_url`: GitHub Releases by default, or `file://<repo>/dist` to update from local builds. Only a newer version installs: to go back, roll back, which also means a dev release waits for the next stable one rather than switching down to an older one.

`--apply` downloads the archive, checks it against `checksums.txt` and runs the new binary's `version` before touching anything. Then it stops the daemon, keeps the installed binary and a backup of the database in `~/.wdx/previous/`, installs the new binary and starts the daemon on it, which migrates the database. If any of those steps fails, the old binary and database are put back and the daemon starts on them again (`update_failed`, with `undone` in its details). One update or rollback runs at a time.

Only one previous version is kept, replaced by each update and removed by a rollback. A rollback keeps the database when the previous version can use it: migrations that only add things leave it usable. A migration marked `-- breaks-older: true` records itself in the database as the oldest schema a binary must know; rolling back across it can only restore the backup taken at the update, losing every change since, so it asks first (`--discard-changes` answers, and is needed with `--json`; without it the rollback fails with `restore_required`).

`make install` and `scripts/install.sh` install without keeping a previous version.

## Tags and the changelog

Tags are annotated, their message holding the release's notes, and are never moved or deleted: a broken release is fixed by the next one. `CHANGELOG.md` is generated: change a commit message's wording before releasing, never the file.
