---
title: Roadmap
description: "What is coming to wdx next, with no dates: each item, what it is for, and how far along it is."
---

What is coming next, in no order and with no dates. An item is here once it is decided; it gets its guide when it ships, marked with the release it came in.

## Usage and limits

**Being built, Claude Code first.**

When an agent stops at its AI CLI's usage limit, nothing in wdx says so today: it reads `idle` or `waiting`, and only its screen says the limit is reached and when it comes back. wdx will read each AI CLI's usage, how much of each limit is used and when it resets, and show it once per AI CLI, in a TIDE tab of its own and with a command: so you can see which AI CLI and models have room before you start an agent, and why one stopped.

wdx shows the facts and leaves the choice to you: it will not resume, switch or pick a model by itself.

## wdx hub

**In design.**

A separate server, open source like wdx, that your daemons connect to:

- reach your agents on your machine from another one, with the connection encrypted end to end;
- share a project's docs and plans with a team you choose, so everyone works from the same designs.

You will be able to host your own, as a company would. Only docs and plans are ever shared with a team; your machine is reachable by you alone.

## Not planned

Anything not above. Ideas are welcome as [issues](https://github.com/srjn45/wdx/issues); [Contributing](/wdx/docs/project/contributing/) says how an idea becomes a feature.
