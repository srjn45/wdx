# wdx

wdx manages AI coding agents on your machine: a daemon that runs as a user service, and a CLI that serves people and scripts alike, with `--json` on every command. Today it manages projects (git repositories and their checkouts); terminals and agents in PTYs come next.

**Status: early development.** wdx is the successor of [warden](https://github.com/srjn45/warden) and runs side by side with it (its own `wdx` binary and `~/.wdx` home), but it does not replace warden yet.

## Install

Linux and macOS, amd64 and arm64. The installer puts `wdx` in `~/.local/bin`, adds the shell completions and runs the daemon as a systemd user service or a launchd agent:

```sh
curl -fsSL https://github.com/srjn45/wdx/releases/latest/download/install.sh | sh                   # stable
curl -fsSL https://github.com/srjn45/wdx/releases/latest/download/install.sh | WDX_CHANNEL=dev sh   # dev
```

No release is published on GitHub yet. From a clone, `make install` builds and installs the current commit (needs Go).

## Quick start

```sh
wdx daemon status                          # the installer started it; wdx daemon start otherwise
wdx project create --name app              # a new git repository under ~/.wdx/projects
wdx project open --local ~/code/api        # a repository you already have
wdx project open --git https://github.com/acme/web.git
wdx project list
wdx help                                   # every command; wdx <command> --help for one
```

Scripts and agents add `--json`: stdout then holds one JSON object, a result or a problem with a stable error `code`, and nothing prompts (`wdx help json`).

## Updating

```sh
wdx update               # the installed version and the newest on the channel
wdx update --apply       # install it; any failed step is undone
wdx update --rollback    # go back to the version the last update replaced
```

The channel is `stable` or `dev` (the `update.channel` setting). See [docs/RELEASING.md](docs/RELEASING.md).

## Docs

- [docs/DAEMON.md](docs/DAEMON.md): the daemon, the service and the shell completions
- [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md): logs and metrics in Grafana (`wdx observe`)
- [docs/RELEASING.md](docs/RELEASING.md): versions, channels, releasing and updating
- [AGENTS.md](AGENTS.md): how the code is organized, for people and AI agents working on it
- Designs of what comes next: [docs/TERMINALS.md](docs/TERMINALS.md), [docs/TIDE.md](docs/TIDE.md), and the backlog in [docs/V1_FEATURES.md](docs/V1_FEATURES.md)

## License

Apache License 2.0: see [LICENSE](LICENSE) and [NOTICE](NOTICE). The licenses of Go and of the modules wdx is built with are in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
