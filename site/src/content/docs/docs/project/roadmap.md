---
title: Roadmap
description: "What has shipped lately and what is coming to wdx next, with no dates: each item and what it is for."
---

What is coming next, in no order and with no dates. An item is here once it is decided; it gets its guide when it ships, marked with the release it came in.

## Planned

- **Teams:** share a project's docs and plans with people you choose. A team never gives access to a machine.
- **Docs and plans on all your nodes:** pushed docs coming back down to your other nodes and a team's, with a conflict kept for a person to resolve, never overwritten.
- **Single sign-on** (OpenID Connect) for an organisation's hub.
- **Signed releases.**
- **Agents' prompts on every AI CLI:** reading the question a waiting agent asks on Codex, Cursor and Antigravity, as on Claude Code, and wdx answering them.
- **Agents resumed by themselves:** an agent running when the machine went down comes back on its own, in the same conversation.
- **A plan's autonomous mode:** a plan that settles what stops it (a waiting agent, a conflict, an agent that ended) without waiting for a person.

## Shipped

- **The hub**, in 0.5.0: your machines as nodes of one account, with who is online; remote access with an authenticator code on each machine; `--node` for commands, attach and TIDE; a project's docs pushed to the hub; all self-hosted, on Linux or Fly.io, for yourself or an organisation. See [Hub](/wdx/docs/guides/hub/) and [Running a hub](/wdx/docs/guides/running-a-hub/).
- **Usage and limits**, in 0.5.0: each AI CLI's usage in `wdx usage` and TIDE's stats tab, and an agent marked `limited` while a limit its model draws on is reached. See [Agents](/wdx/docs/guides/agents/).

## Not planned

Anything not above. Ideas are welcome as [issues](https://github.com/srjn45/wdx/issues); [Contributing](/wdx/docs/project/contributing/) says how an idea becomes a feature.
