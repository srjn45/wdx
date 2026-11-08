---
title: Concepts
description: Projects, the daemon and holders, terminals, agents and their AI CLIs, worktrees, docs, plans and tide.
---

The words the rest of the docs use, and how the pieces fit.

## The daemon

One background process per user, run as a systemd or launchd service. It keeps wdx's state in one SQLite file, `~/.wdx/wdx.db`, watches git for changes, and serves an API on a Unix socket, `~/.wdx/wdx.sock`. The CLI and TIDE are its clients. `wdx daemon status` says whether it runs.

## Projects

A **project** is a git repository wdx knows, with its checkouts: the main one and any worktrees. `wdx project open` registers one you have, clones one, or opens one closed earlier. Closing a project stops its terminals and agents and keeps its records.

## Terminals and holders

A **terminal** is a shell running in a pseudo-terminal. Each is owned by a **holder**, a small process of its own that keeps the shell, its screen and its scrollback. Holders do not depend on the daemon: restart or update the daemon and every terminal keeps running. You attach to one from any terminal window (`wdx terminal attach`), as with tmux, and detach with `Ctrl-\` then `d`.

## Agents and AI CLIs

An **agent** is a terminal that runs an AI CLI: Claude Code, Codex, Cursor or Antigravity, which wdx calls an ACA (AI coding agent). wdx starts it, reads its screen to tell whether it is **working**, **idle** or **waiting** for you (and with what question), and follows its session, so that an agent started again goes on in the same conversation.

An agent can have **child agents**, which it starts itself through wdx, and messages can be **sent** to any agent or terminal (`wdx agent send`), delivered when it can take them.

## Worktrees

A **worktree** is a git checkout of its own on a branch of its own. An agent given one (`--worktree <name>`) works there, never in the files another agent is editing. wdx shows what each worktree has committed and what it has not, and **lands** it on the default branch, without a merge commit, or through a PR. Landing goes through the project's **landing check**, a command you set that must pass first.

## Docs

A **doc** is a document kept in wdx rather than in the repository, with a revision per change and a **status**: `wip`, `review`, `approved` or `rejected`. People and agents read the same doc as soon as it is written, and a change names the revision it was based on, so two writers never overwrite each other.

## Plans

A **plan** splits a feature into **tasks**, each with the tasks it waits for. Running it, wdx starts an agent for each task that is ready, each in a worktree of the plan's, and lands every finished task on the plan's branch. A task that fails waits for you to retry, land or skip it. A **plan of plans** runs bigger work as a series of plans, and can open them as stacked PRs.

## TIDE

`wdx tide` is the Terminal IDE: one window per project with its agents, worktrees, docs, plans and code on the left, what you open on the right, and terminals below. It is a client of the daemon like the CLI: quit it and everything keeps running.

## The CLI, for people and scripts

Every input is a flag, every entity is named by `--name` or `--id`, and every command takes `--json`: stdout then holds one JSON object, a result or an error with a stable `code`, and nothing prompts. Exit codes are 0 (ok), 1 (rejected: a conflict, not found, cancelled), 2 (usage) and 3 (a system failure). An agent that wdx runs learns all of this from `wdx help agents`.

## Where things live

| Path | What |
|---|---|
| `~/.local/bin/wdx` | the binary |
| `~/.wdx/wdx.db` | every record: projects, terminals, agents, docs, plans |
| `~/.wdx/config.yaml` | the settings (`wdx setting list`) |
| `~/.wdx/logs/` | the logs of the daemon, the CLI and the holders |
| `~/.wdx/projects/` | repositories created with `wdx project create` |

`WDX_HOME` moves all of `~/.wdx` elsewhere.
