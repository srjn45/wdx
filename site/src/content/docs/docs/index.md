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

## Reference

- [The CLI](/wdx/docs/reference/cli/): every command, its flags, examples, output and errors.
- [Settings](/wdx/docs/reference/settings/), [Errors and exit codes](/wdx/docs/reference/errors/), [JSON output](/wdx/docs/reference/json/) and the [Changelog](/wdx/docs/reference/changelog/).

These pages are generated from wdx itself, so they say what `wdx <command> --help` says.

More guides, and how wdx is built, are on their way.
