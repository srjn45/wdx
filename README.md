<p align="center">
  <a href="https://srjn45.github.io/wdx/">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="assets/brand/wdx-wordmark-light.svg">
      <img src="assets/brand/wdx-wordmark.svg" alt="wdx" height="72">
    </picture>
  </a>
</p>

<p align="center"><b>Run a team of AI coding agents from one terminal.</b></p>

<p align="center">
  <a href="https://srjn45.github.io/wdx/">Website</a> ·
  <a href="https://srjn45.github.io/wdx/docs/">Docs</a> ·
  <a href="https://github.com/srjn45/wdx/releases">Releases</a>
</p>

<p align="center">
  <a href="https://github.com/srjn45/wdx/releases/latest"><img src="https://img.shields.io/github/v/release/srjn45/wdx?label=release" alt="Latest release"></a>
  <a href="https://github.com/srjn45/wdx/actions/workflows/ci.yml"><img src="https://github.com/srjn45/wdx/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/srjn45/wdx" alt="License: Apache-2.0"></a>
</p>

wdx keeps Claude Code, Codex, Cursor and Antigravity agents running on your machine, each in its own terminal and often its own git worktree, long after the window you started them from has closed. TIDE, its Terminal IDE, shows all of them in one window, with their diffs, docs, plans and code.

<p align="center"><img src="assets/readme/tide.png" alt="TIDE showing a project: its agents, worktrees, docs, plans and code on the left, an agent's conversation on the right, and a terminal below" width="880"></p>

## Why wdx

- **Agents that stay.** Each runs in a process of its own that outlives your terminal, the daemon, and updates of wdx itself.
- **A branch per agent.** An agent in a worktree never edits the files another is editing; you read its diff, then land it with no merge commit, after your check passes.
- **Know who needs you.** wdx reads each agent's screen: working, idle, or waiting for you, with the question it asks.
- **Plans that run themselves.** Split a feature into tasks for agents, with the tasks each waits for; wdx starts them, lands each finished one, and asks you only when it must.
- **For scripts and AIs too.** Every command takes `--json` and never prompts then, and `wdx help agents` teaches the agents wdx runs to use it.

## Install

On Linux or macOS, amd64 or arm64:

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh
```

It puts `wdx` in `~/.local/bin`, installs the shell completions and runs the daemon as a user service. [Install](https://srjn45.github.io/wdx/docs/start/install/) has the dependencies, the dev channel and uninstalling.

## Quick start

In a repository you already have, with Claude Code and Codex installed:

```sh
wdx project open --local ~/code/api
cd ~/code/api
wdx agent create --name reviewer --prompt "review the open changes"
wdx agent create --name fix-auth --worktree fix-auth --aca codex --prompt "fix the session expiry bug"
wdx agent list
wdx tide
```

The [Quickstart](https://srjn45.github.io/wdx/docs/start/quickstart/) goes on from there: talking to an agent, its diff, and landing its work.

## AI CLIs

wdx runs each the same way, as far as each allows; [the AI CLIs](https://srjn45.github.io/wdx/docs/acas/) compares them side by side.

| AI CLI | Its program | |
|---|---|---|
| Claude Code | `claude` | [page](https://srjn45.github.io/wdx/docs/acas/claude-code/) |
| Codex | `codex` | [page](https://srjn45.github.io/wdx/docs/acas/codex/) |
| Cursor | `cursor-agent` | [page](https://srjn45.github.io/wdx/docs/acas/cursor/) |
| Antigravity | `agy` | [page](https://srjn45.github.io/wdx/docs/acas/antigravity/) |

## Documentation

On [the website](https://srjn45.github.io/wdx/docs/):

- [Start](https://srjn45.github.io/wdx/docs/start/what-is-wdx/): what wdx is, installing it, the quickstart and its concepts.
- [Guides](https://srjn45.github.io/wdx/docs/guides/projects/): a page per feature, from projects and agents to worktrees, docs and plans.
- [TIDE](https://srjn45.github.io/wdx/docs/tide/layout/): the Terminal IDE, screen by screen, with its screens live.
- [Scripting with --json](https://srjn45.github.io/wdx/docs/guides/scripting/) and [Working with AI agents](https://srjn45.github.io/wdx/docs/guides/ai-agents/).
- [Running wdx](https://srjn45.github.io/wdx/docs/guides/daemon/): the daemon, settings, updating, dependencies, observability.
- [Architecture](https://srjn45.github.io/wdx/docs/architecture/): how wdx works inside, each page around a diagram.
- [Reference](https://srjn45.github.io/wdx/docs/reference/cli/): every command, setting and error code, generated from wdx itself.

Working on wdx itself: [`docs/`](docs/) has a file per part of it, and [`AGENTS.md`](AGENTS.md) the rules the code follows.

## Status

wdx has had stable releases since 0.4.0, with dev releases between them for those who want features early; the [changelog](https://srjn45.github.io/wdx/docs/reference/changelog/) says what each brought. It is the next generation of [warden](https://github.com/srjn45/warden), written anew; warden is deprecated.

Coming next, on the [roadmap](https://srjn45.github.io/wdx/docs/project/roadmap/): each AI CLI's usage and limits, being built, and wdx hub, in design, to reach your agents from another machine and share docs and plans with a team.

## Contributing

Issues are welcome. Design comes before code here: [Contributing](https://srjn45.github.io/wdx/docs/project/contributing/) says how a change is made, built and tested.

## License

Apache License 2.0: see [LICENSE](LICENSE) and [NOTICE](NOTICE). The licenses of Go and of the modules wdx is built with are in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
