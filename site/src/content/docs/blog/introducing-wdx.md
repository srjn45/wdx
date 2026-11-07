---
title: Introducing wdx
date: 2026-11-08
excerpt: Why a daemon, a CLI and a terminal IDE for AI coding agents, and what a day with four of them looks like.
tags: [launch]
authors: srajan
---

Running one AI coding agent is easy: open a terminal and start it. Running four at once is where it falls apart. One is waiting on a yes/no question in a tab you closed an hour ago. Two are editing the same files. The laptop went to sleep and took a session with it. And at the end of the day you have to piece together what each one actually changed.

wdx is what I built to run them like a team rather than a pile of terminals.

![tide, wdx's terminal IDE: a project's agents, worktrees, docs and plans on the left, an agent on the right, terminals below](/wdx/og.png)

## Agents that outlive your terminal

wdx is one binary with three faces: a daemon, a CLI and tide, its terminal IDE.

The daemon runs every agent and terminal in a pseudo-terminal of its own, owned by a small *holder* process. Close tide, close the terminal, restart or even update the daemon, and the agents keep working. Open tide again and everything is where you left it.

wdx runs the AI CLIs you already use, Claude Code, Codex, Cursor and Antigravity, with the same features on each. Its own features come first: whichever CLI an agent runs, it is created, watched, answered and resumed the same way.

## Knowing who needs you

The hardest part of running agents in parallel is knowing which one is stuck. wdx reads each agent's screen and tells working, waiting and idle apart, for every CLI it supports. When an agent asks a question, its row in tide turns amber and shows what kind of question it is. You can answer from tide without switching to it. Each row also shows how much of its context the agent has used.

## A worktree per agent

An agent given a worktree works on a branch of its own, so two agents never edit the same checkout:

```sh
wdx agent create --name fix-auth --worktree fix-auth --aca codex
```

tide lists every worktree with what it has committed and what it has not, and shows the diff file by file. When it looks right, `wdx worktree land` puts it on main without a merge commit, or opens a PR.

## Plans: a feature split into tasks

For a bigger change, a plan splits the work into tasks that depend on each other. wdx starts an agent for each task that is ready, each in a worktree of the plan's, and lands every finished task on the plan's branch. tide draws the plan the way `git log --graph` draws commits. A task that fails waits for you to retry it, land it or skip it.

## The same features for scripts and agents

Everything tide can do, the CLI can do too, and every command takes `--json`. It then prints one JSON object with a stable error code and never prompts. That is how agents use wdx themselves: `wdx help agents` teaches an agent to make child agents, worktrees, docs and plans through wdx, so you see all of their work in tide as well.

Designs live in wdx as docs with revisions and a status, from wip to approved, so people and agents read the same thing.

## Try it

wdx runs on Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh
wdx project open --local ~/code/api
wdx agent create --name fix --worktree fix
wdx tide
```

Or try tide right now in your browser on [the home page](/wdx/). It is a replica that runs nothing, but it has the same layout, keys and screens.

Next is the hub, to reach your agents from another machine and keep docs and plans in sync across machines. It is still in design, so it has no dates yet. Follow along here or through the [RSS feed](/wdx/blog/rss.xml).
