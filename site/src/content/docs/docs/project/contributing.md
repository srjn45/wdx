---
title: Contributing
description: "How to build and test wdx and its site, where its developer docs are, and how a change goes from an idea to a commit."
---

wdx is developed in the open at [github.com/srjn45/wdx](https://github.com/srjn45/wdx). Issues are welcome: a bug with what you ran and what you saw, or an idea with the problem it solves.

## How a change is made

Design comes before code, for everyone, people and AIs alike:

1. **An issue** says the problem: who has it and what happens if it is never solved.
2. **A design** is agreed in the issue: the whole solution, its commands and names, and how the CLI and TIDE each show it. A large change is split into small steps.
3. **A pull request** builds one agreed step, with its tests and its docs. A pull request with no agreed design is likely to be closed and turned into an issue.

[`AGENTS.md`](https://github.com/srjn45/wdx/blob/main/AGENTS.md) holds the rules the code follows: the package layout, the API and CLI conventions, logging, the database, and how tests are written. It is written for the AI agents working on wdx, and every person working on it follows it too.

## Building and testing

You need Go (the version in `go.mod`), git, and make, on Linux or macOS.

```sh
git clone https://github.com/srjn45/wdx && cd wdx
make build      # bin/wdx
make check      # the linters, vet, vulnerabilities, notices and commit messages
make test       # every test, with the race detector, as CI runs them
```

- **Tests use real things:** a real SQLite database, the real git, real PTYs, in temporary directories. They never touch `~/.wdx`, and every test runs in parallel.
- **Run a wdx you built** in a home of its own, so it leaves yours alone: `WDX_HOME=/tmp/wdx-dev bin/wdx daemon start`.
- **Commits** follow Conventional Commits (`feat:`, `fix:`, `docs:`): the next version and the changelog are worked out from them.

## The site

The site you are reading is in `site/`, built with Astro and Starlight. You need Node (the version in `site/package.json`).

```sh
make site-dev   # serve it at http://localhost:4321/wdx/, reloading as you edit
make site-ref   # write the reference pages again from wdx's own help
```

A change a user can notice updates its page here in the same commit. The tests fail when a reference page is stale, or a command is on no page.

## The developer docs

[`docs/`](https://github.com/srjn45/wdx/tree/main/docs) has a file per part of wdx: how it works, why it was built so, and what was measured on the way. The [Architecture](/wdx/docs/architecture/) pages are the way into them.
