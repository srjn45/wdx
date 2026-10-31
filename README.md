# wdx

wdx manages AI coding agents on your machine: a daemon that runs as a user service, and a CLI that serves people and scripts alike, with `--json` on every command. Today it manages projects (git repositories and their checkouts), terminals and agents. Terminals are shells in PTYs that outlive the daemon, which you attach to from any terminal, with their history to scroll back through. Agents run an AI coding agent, Claude Code for now, the same way, and go on in the same session when started again; an agent can have child agents and work in its own git worktree. `wdx tide`, the terminal IDE, shows a project with its agents and terminals in one window.

**Status: early development.** wdx is the successor of [warden](https://github.com/srjn45/warden) and runs side by side with it (its own `wdx` binary and `~/.wdx` home), but it does not replace warden yet.

## Install

Linux and macOS, amd64 and arm64. The installer puts `wdx` in `~/.local/bin`, adds the shell completions and runs the daemon as a systemd user service or a launchd agent:

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | WDX_CHANNEL=dev sh
```

There are only dev releases so far, which may be buggy; without `WDX_CHANNEL=dev` the installer looks for a stable release and stops. `WDX_VERSION=0.2.0-dev.156` installs one release. From a clone, `make install` builds and installs the current commit (needs Go). The same script with `sh -s -- --uninstall` removes wdx and keeps `~/.wdx`.

wdx needs git 2.31 or later for its projects; a clipboard tool (`wl-copy` or `xclip`; macOS has one) for tide's copy, and Docker or Podman for `wdx observe`, are optional. `wdx deps list` shows what is ready, and `wdx deps install --name <dependency>` installs one with apt (Ubuntu, Debian) or brew (macOS); without one, only its features are missing.

## Quick start

```sh
wdx daemon status                          # the installer started it; wdx daemon start otherwise
wdx project create --name app              # a new git repository under ~/.wdx/projects
wdx project open --local ~/code/api        # a repository you already have
wdx project open --git https://github.com/acme/web.git
wdx project list

cd ~/code/api
wdx agent create --name reviewer           # Claude Code in the project you are in; needs claude installed
wdx agent create --name fix --worktree fix # the same in a new worktree, on a new branch
wdx agent attach --name reviewer           # talk to it
wdx terminal create --name server          # a shell in the project
wdx terminal attach --name server

wdx tide                                   # all of it in one window
wdx help                                   # every command; wdx <command> --help for one
```

Scripts and agents add `--json`: stdout then holds one JSON object, a result or a problem with a stable error `code`, and nothing prompts (`wdx help json`).

## Updating

```sh
wdx update               # the installed version and the newest on the channel
wdx update --apply       # install it; any failed step is undone
wdx update --rollback    # go back to the version the last update replaced
```

The channel is the `update.channel` setting, which `WDX_CHANNEL=dev` saves when installing. For a wdx installed before that, run `wdx settings write --set update.channel=dev`. See [docs/RELEASING.md](docs/RELEASING.md).

## Docs

- [docs/DAEMON.md](docs/DAEMON.md): the daemon, the service and the shell completions
- [docs/DEPS.md](docs/DEPS.md): the programs wdx needs besides itself (`wdx deps`)
- [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md): logs and metrics in Grafana (`wdx observe`)
- [docs/RELEASING.md](docs/RELEASING.md): versions, channels, releasing and updating
- [docs/TERMINALS.md](docs/TERMINALS.md): terminals and the holder process that keeps each shell running
- [docs/AGENT.md](docs/AGENT.md): agents, which run an AI coding agent on the same process machinery; [docs/CHILD_AGENTS.md](docs/CHILD_AGENTS.md): child agents
- [docs/ATTACH.md](docs/ATTACH.md): attaching to a terminal (`wdx terminal attach`) and its history; [docs/VT.md](docs/VT.md): the terminal emulator it uses and what to fix in it
- [docs/TIDE.md](docs/TIDE.md): the terminal IDE (`wdx tide`), what is built and what comes next
- [AGENTS.md](AGENTS.md): how the code is organized, for people and AI agents working on it
- The backlog: [docs/V1_FEATURES.md](docs/V1_FEATURES.md)

## License

Apache License 2.0: see [LICENSE](LICENSE) and [NOTICE](NOTICE). The licenses of Go and of the modules wdx is built with are in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
