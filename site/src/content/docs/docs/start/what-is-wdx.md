---
title: What is wdx
description: A daemon, a CLI and a terminal IDE for running AI coding agents side by side.
---

Running one AI coding agent is easy: open a terminal and start it. Running several at once is not. One waits on a question in a tab you closed, two edit the same files, a laptop that slept takes a session with it, and at the end of the day you piece together what each changed.

wdx runs them as a team instead of a pile of terminals.

## What it does

- **Agents that outlive your terminal.** Every agent and every shell runs in a pseudo-terminal owned by a small process of its own, a *holder*. Close the window, restart the daemon or update wdx, and they keep running; attach again from any terminal.
- **The AI CLIs you already use.** An agent runs Claude Code, Codex, Cursor or Antigravity, with the same wdx features on each.
- **Who needs you, at a glance.** wdx reads each agent's screen and tells working, waiting and idle apart. An agent that asks a question is marked until you answer it.
- **A worktree per agent.** An agent can work in a git worktree on a branch of its own. You read its diff and land it on the default branch, without a merge commit, or through a PR.
- **Plans.** A feature split into tasks that wait for each other: wdx starts an agent for each task that is ready and lands what it finishes.
- **Docs with revisions.** Designs live in wdx, with a status from `wip` to `approved`, so people and agents read the same thing as soon as it is written.
- **tide**, the terminal IDE: a project's agents, worktrees, docs, plans and code in one window. [Try it in your browser](/wdx/#tide).
- **The same features for scripts.** Every command takes `--json`, prints one JSON object with a stable error code, and never prompts. Agents use wdx this way too.

## What it is not

- **Not an AI CLI.** wdx runs the ones you install and pay for; it adds no model of its own.
- **Not a cloud service.** Everything runs on your machine, in `~/.wdx`. A hub to reach agents from another machine is being designed.
- **Not tied to an editor.** tide is a terminal program; any terminal and any editor work with it.

## Where it runs

Linux and macOS, on amd64 and arm64. wdx needs git; everything else is optional. Next: [Install](/wdx/docs/start/install/).
