---
title: Quickstart
description: Open a project, start agents in worktrees, and watch them in tide.
---

This takes a repository you already have to a few agents at work and TIDE showing them. It assumes wdx is [installed](/wdx/docs/start/install/) and at least one AI CLI is: Claude Code (`claude`) here, the default.

## 1. Open a project

A project is a git repository wdx knows. Open the one you are going to work on:

```console
$ wdx project open --local ~/code/api
registered project api (proj-91ff39ca)
  origin:    (none)
  worktrees: /home/you/code/api
```

`--git <url>` clones one instead, and `wdx project create --name <name>` starts a new repository under `~/.wdx/projects`.

From inside the repository, commands pick the project by itself; elsewhere, add `--project-name api`.

## 2. Start an agent

```sh
cd ~/code/api
wdx agent create --name reviewer --prompt "review the open changes"
```

The agent starts Claude Code in the project and gives it the prompt. It runs in the daemon: close this terminal and it keeps going. To talk to it, attach:

```sh
wdx agent attach --name reviewer     # Ctrl-\ then d detaches
```

## 3. Give an agent a branch of its own

An agent with a worktree works on a new branch in a checkout of its own, so it never edits the files another agent is editing:

```sh
wdx agent create --name fix-auth --worktree fix-auth --aca codex --prompt "fix the session expiry bug"
```

`--aca` picks the AI CLI: `claude_code`, `codex`, `cursor` or `antigravity`. `wdx agent acas` lists those installed.

## 4. See what they are doing

```sh
wdx agent list
```

Each agent is `working`, `idle`, or `waiting` for you, with the question it asks. `wdx worktree list` shows what each worktree has committed and what it has not, and `wdx worktree diff --name fix-auth` the diff.

## 5. Open TIDE

```sh
wdx tide
```

TIDE shows the project in one window: agents, worktrees, docs and plans on the left, the one you pick on the right, terminals below. Arrows and Enter move and open; `C-q` shows every key; `C-]` zooms a pane. Quit it with `C-q q`: the agents keep running, and TIDE comes back as you left it.

## 6. Land the work

First say once how the project lands: the check to run before anything moves, or none.

```sh
wdx project landing --name api --check "make test"     # or --no-check
```

Then, when a worktree's diff looks right:

```sh
wdx worktree land --name fix-auth
```

wdx rebases the worktree if main moved on, runs the check, and moves main forward to the commit checked, with no merge commit. If anything fails, main stays where it was. `--method pr` opens a GitHub PR instead.

## Also in a minute

A shell that outlives the window, for a server or a test run:

```console
$ wdx terminal create --name server
created terminal server in api
  dir    /home/you/code/api
  shell  /usr/bin/zsh
  id     term-0e5e09e2
$ wdx terminal attach --name server
```

A design that you and your agents read and change, with a revision per change:

```console
$ wdx doc create --path checkout.md --name CHECKOUT
doc CHECKOUT created
  project  api
  folder   /
  status   wip
  revision 1
  format   md
  id       doc-4d3703a2
```

## For scripts and agents

Add `--json` to any command for one JSON object on stdout and no prompts:

```sh
wdx agent list --json
```

Agents wdx runs learn all of this from `wdx help agents`.

Next: the [Concepts](/wdx/docs/start/concepts/) behind these commands.
