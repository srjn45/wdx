---
title: FAQ
description: "Short answers to the questions people ask first about wdx: cost, systems, API keys, the network, reboots, and which AI CLI to use."
---

## Is wdx free?

Yes. It is open source under the [Apache License 2.0](/wdx/docs/project/license/). The AI CLIs it runs are your own, on your own plans with their vendors.

## Which systems does it run on?

Linux and macOS, on amd64 or arm64. On WSL and in containers, where there is no systemd user session, the installer starts the daemon directly rather than as a service: start it again after a reboot. See [Install](/wdx/docs/start/install/).

## Does it need tmux?

No. wdx runs each terminal in a process of its own, a [holder](/wdx/docs/architecture/holders/), and TIDE is its own Terminal IDE. Run TIDE inside tmux if you like; wdx does not need it.

## Do I need API keys?

No. Each AI CLI runs as you run it yourself, with its own login and plan. wdx starts it, reads its screen and types into it; it never sees your credentials.

## Does wdx send anything anywhere?

No telemetry. The daemon sends metrics only to `otel.endpoint`, `http://localhost:4318` by default, where [Observability](/wdx/docs/guides/observability/) listens, and nothing when it is empty. wdx uses the network only for what you ask of it: cloning a project, landing with a push or a pull request, and `wdx update`. The AI CLIs talk to their own vendors, as they do without wdx.

## What happens when I reboot?

Terminals and agents end with the machine, and show as `lost`. `wdx agent start` brings an agent back in the same conversation, and `wdx terminal start` a terminal, with its history. See [Agents](/wdx/docs/guides/agents/) and [Terminals](/wdx/docs/guides/terminals/).

## Can I use it without TIDE?

Yes. Everything is a command, and every command has `--json` for scripts and agents. TIDE is a client of the daemon like any other. See [Scripting with --json](/wdx/docs/guides/scripting/).

## Which AI CLI should I use?

The one you already use and pay for: wdx gives each the same features, as far as each allows. [The AI CLIs](/wdx/docs/acas/) compares them side by side.

## What became of warden?

wdx is its next generation, written anew. warden is no longer developed.
