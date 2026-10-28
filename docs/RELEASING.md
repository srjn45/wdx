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

## Tags and the changelog

Tags are annotated, their message holding the release's notes, and are never moved or deleted: a broken release is fixed by the next one. `CHANGELOG.md` is generated: change a commit message's wording before releasing, never the file.
