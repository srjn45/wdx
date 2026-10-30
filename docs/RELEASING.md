# Releasing wdx

A version is a git tag; there is no version file. The build reads it with `git describe`, so a build between releases is named after the last tag and the commits since (`0.2.0-dev.4-3-gabc1234`).

```
make release-dev   tag HEAD as the next dev release, build it into dist/ and push it
make release       commit CHANGELOG.md, tag the next stable release, build it into dist/ and push it
make changelog     print what changed since the last stable release
make notices       write THIRD-PARTY-NOTICES.md from the modules linked into wdx
```

Both release targets need a clean working tree on `main`, not behind `origin/main`, and a passing `make check` and `make test`, and refuse when there is nothing to release. They end by pushing `main` and the tag together.

## Publishing

`.github/workflows/release.yml` publishes the GitHub Release of each `v*` tag pushed: it runs the jobs of `ci.yml` on the tag, then `scripts/publish.sh`, which checks that the tag is annotated and on `main`, builds the archives with the tag's own `scripts/dist.sh`, and uploads them with `checksums.txt` and the tag's `install.sh`. The tag's notes are the release's. A dev tag is a pre-release, so GitHub's latest release is always the newest stable one. A failed run leaves a draft at most; running it again replaces it. A tag pushed before releases were published is published by running the workflow by hand with that tag (`gh workflow run release.yml -f tag=v0.2.0-dev.20`), without the checks.

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

The dev channel installs the newest release of either kind, so dev users get each stable release too. `dist.sh` writes `dist/LATEST-dev` for every release and `dist/LATEST` for stable ones only; on GitHub, the latest release is never a pre-release.

```sh
curl -fsSL https://github.com/srjn45/wdx/releases/latest/download/install.sh | sh                   # stable
curl -fsSL https://github.com/srjn45/wdx/releases/latest/download/install.sh | WDX_CHANNEL=dev sh   # dev
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

`--apply` downloads the archive and checks it against `checksums.txt`, then, before touching anything, runs the new binary's `settings write` on the config file into a scratch file and its `version` on that file. A value the new version does not accept stops the update there (`settings_invalid`). Then it stops the daemon, keeps the installed binary, a backup of the database and the config file in `~/.wdx/previous/`, installs the new binary and the config file written anew, and starts the daemon on them, which migrates the database. If any of those steps fails, the old binary, database and config file are put back and the daemon starts on them again (`update_failed`, with `undone` in its details). One update or rollback runs at a time.

The config file written anew lists every setting of the new version with its description: the values set stay as written, new settings appear commented with their defaults, and keys the version no longer has are moved, commented, to the end, so it still starts. Comments of the user's own are not kept. A rollback puts back the config file as it was before the update.

Only one previous version is kept, replaced by each update and removed by a rollback. A rollback keeps the database when the previous version can use it: migrations that only add things leave it usable. A migration marked `-- breaks-older: true` records itself in the database as the oldest schema a binary must know; rolling back across it can only restore the backup taken at the update, losing every change since, so it asks first: `--discard-changes` answers yes and `--cancel` no, and `--json` needs one of them; no fails with `restore_required`.

`make install` and `scripts/install.sh` install without keeping a previous version.

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
make check         on Linux: golangci-lint (.golangci.yml), shellcheck and actionlint; a tidy and verified
                   go.mod; current notices; govulncheck; go vet for macOS and Linux arm64; the commit
                   messages in RANGE
```

Locally `make check` reads the commits in `origin/main..HEAD`; CI reads those of the push or pull request, and none for a tag, whose commits were checked on `main`. A push whose previous commit is not in the history, as after a force push, has its last commit checked. The linters are pinned in the `Makefile` and fetched into `bin/` on first use: the Go tools built by `go install`, shellcheck checked against the checksums in `scripts/fetch-release.sh`. The weekly run catches vulnerabilities published since the last push; a fix in the standard library means raising the `go` line of `go.mod`, which the release builds then use.
