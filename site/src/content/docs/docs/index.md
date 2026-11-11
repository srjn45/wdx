---
title: wdx documentation
description: Run a team of AI coding agents from one terminal. Start here.
---

wdx runs AI coding agents (Claude Code, Codex, Cursor and Antigravity) on your machine, each in a terminal that outlives the window you started it from, and often in a git worktree of its own. A daemon keeps them running; the `wdx` CLI and TIDE, its Terminal IDE, are how you and your scripts reach them.

## Start here

- [What is wdx](/wdx/docs/start/what-is-wdx/): what it does and what it is not.
- [Install](/wdx/docs/start/install/): one command on Linux or macOS.
- [Quickstart](/wdx/docs/start/quickstart/): a project, an agent and TIDE in five minutes.
- [Concepts](/wdx/docs/start/concepts/): the words the rest of the docs use.

## Guides

One per feature, each with a session to try, how it shows in TIDE, how it behaves and its commands:

- [Projects](/wdx/docs/guides/projects/): register, clone, close and reopen repositories.
- [Terminals](/wdx/docs/guides/terminals/): shells that outlive the window and the daemon.
- [Agents](/wdx/docs/guides/agents/): AI CLIs wdx runs, reads and resumes.
- [Child agents](/wdx/docs/guides/child-agents/): agents that start agents of their own.
- [Messages and sending](/wdx/docs/guides/messages/): tasks for agents from scripts and other agents.
- [Worktrees and landing](/wdx/docs/guides/worktrees/): a branch per agent, a diff, and landing with no merge commit.
- [Docs](/wdx/docs/guides/docs/): designs kept in wdx with a revision per change.
- [Plans](/wdx/docs/guides/plans/) and [Running plans](/wdx/docs/guides/running-plans/): a feature as tasks for agents, run and landed by wdx.
- [Hub](/wdx/docs/guides/hub/) and [Running a hub](/wdx/docs/guides/running-a-hub/): reach your machines from each other, and host your own.

## TIDE

The Terminal IDE, page by page, with its screens live: [Layout and keys](/wdx/docs/tide/layout/), [Agents](/wdx/docs/tide/agents/), [Worktrees and diffs](/wdx/docs/tide/worktrees/), [Code](/wdx/docs/tide/code/), [Docs and plans](/wdx/docs/tide/docs-and-plans/), [The menu](/wdx/docs/tide/menu/), [Settings](/wdx/docs/tide/settings/) and [Stats](/wdx/docs/tide/stats/).

## AI CLIs

[The AI CLIs](/wdx/docs/acas/) wdx runs agents on, side by side, and a page each: [Claude Code](/wdx/docs/acas/claude-code/), [Codex](/wdx/docs/acas/codex/), [Cursor](/wdx/docs/acas/cursor/) and [Antigravity](/wdx/docs/acas/antigravity/).

## Scripts and AI agents

- [Scripting with --json](/wdx/docs/guides/scripting/): one JSON object per command, stable error codes, no prompts.
- [Working with AI agents](/wdx/docs/guides/ai-agents/): what wdx tells the agents it runs.

## Running wdx

- [The daemon and its service](/wdx/docs/guides/daemon/), [Settings](/wdx/docs/guides/settings/), [Updating](/wdx/docs/guides/updating/), [Dependencies](/wdx/docs/guides/dependencies/), [Observability](/wdx/docs/guides/observability/) and [Shell completion](/wdx/docs/guides/completion/).

## Architecture

How wdx works inside, each page around a diagram: [the overview](/wdx/docs/architecture/), [Daemon and service](/wdx/docs/architecture/daemon/), [Holders and PTYs](/wdx/docs/architecture/holders/), [State in one SQLite file](/wdx/docs/architecture/state/), [The API](/wdx/docs/architecture/api/), [Reading an agent's state](/wdx/docs/architecture/agent-state/), [Worktrees and watchers](/wdx/docs/architecture/worktrees/) and [The hub](/wdx/docs/architecture/hub/).

## Reference

- [The CLI](/wdx/docs/reference/cli/): every command, its flags, examples, output and errors.
- [Settings](/wdx/docs/reference/settings/), [Errors and exit codes](/wdx/docs/reference/errors/), [JSON output](/wdx/docs/reference/json/) and the [Changelog](/wdx/docs/reference/changelog/).

These pages are generated from wdx itself, so they say what `wdx <command> --help` says.

## Project

The [Roadmap](/wdx/docs/project/roadmap/), the [FAQ](/wdx/docs/project/faq/), [Contributing](/wdx/docs/project/contributing/) and the [License](/wdx/docs/project/license/).
