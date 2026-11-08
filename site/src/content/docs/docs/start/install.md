---
title: Install
description: Install wdx and its daemon with one command, update it, or remove it.
---

On Linux or macOS (amd64 or arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh
```

The installer:

- puts `wdx` in `~/.local/bin`;
- adds shell completions for bash, zsh and fish;
- runs the daemon as a user service: systemd on Linux, launchd on macOS, so it starts with your session.

Check that the daemon runs:

```console
$ wdx daemon status
daemon running (pid 2458007)
  version: 0.4.0 (31f674f)
  started: 2026-11-08 01:05:28 (up 4s)
  home:    /home/you/.wdx
  socket:  /home/you/.wdx/wdx.sock
```

## What else wdx uses

wdx needs **git 2.31 or later**. Everything else adds a feature when it is there:

| Program | For |
|---|---|
| `claude`, `codex`, `cursor-agent`, `agy` | agents running Claude Code, Codex, Cursor or Antigravity |
| `wl-copy` or `xclip` (macOS has `pbcopy`) | copying from TIDE |
| `gh` | landing a worktree through a GitHub PR |
| vi, vim, nvim, nano, hx or emacs | editing files from TIDE |
| Docker or Podman | `wdx observe`, the logs and metrics in Grafana |

`wdx deps list` shows what is ready:

```console
$ wdx deps list
git           mandatory  ready  git 2.53.0     projects, terminals
clipboard     optional   ready  wl-copy        TIDE copy
gh            optional   ready  gh 2.46.0      landing by PR
claude        optional   ready  claude         agents: claude_code
codex         optional   ready  codex          agents: codex
...
```

`wdx deps install --name <dependency>` installs one with apt (Ubuntu, Debian) or brew (macOS).

## Choosing a release

The installer installs the newest stable release. Two variables change that:

- `WDX_CHANNEL=dev` installs the newest dev release, built more often and less tested, and keeps you on the dev channel for updates.
- `WDX_VERSION=0.4.0` installs that release.

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | WDX_CHANNEL=dev sh
```

## Updating

```sh
wdx update               # the installed version and the newest on your channel
wdx update --apply       # install the newest; a step that fails is undone
wdx update --rollback    # go back to the version the last update replaced
```

Agents and terminals keep running through an update: their holders do not depend on the daemon.

## Uninstalling

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh -s -- --uninstall
```

This stops the daemon, removes `wdx`, its completions and its service, and keeps `~/.wdx`, where your projects' records, docs and plans live. Remove that directory too to forget everything.

Next: the [Quickstart](/wdx/docs/start/quickstart/).
