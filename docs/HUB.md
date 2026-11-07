# wdx-hub

Status: notes from an open discussion on 2026-11-01, far from being built. Nothing here is final: decisions are worked out when the hub is worked on. "Agreed" is what the human said; "Proposed" came from the AI and was not agreed. docs/IDE.md has the wider picture.

## Agreed

- `wdx-hub` is a separate app in this repository, open source like the rest.
- ~~It serves a web portal where a user logs in and registers their machines.~~ No portal (2026-11-03).
- The projects they work on are shared with their team. Docs and plans sync across teammates, so that everyone is aligned and can reuse what others published.
- ~~It comes with a web IDE, to reach a machine from the same machine or a remote one.~~ No web IDE (2026-11-03).
- The daemon and the hub talk over REST and a persistent two-way WebSocket.
- The human hosts one public instance, to reach their own machine remotely. A company deploys its own and points its daemons at it.
- Not soon: making wdx right on the local machine, with the CLI and tide, comes first.

Security, agreed on 2026-11-01:

- Communication between the hub and the daemon is encrypted.
- A user logs in to the hub to reach their account.
- A daemon is registered once on a hub account, and can then be used remotely by that account's holder.
- Only that holder can open the web IDE on that daemon. Teammates never can.
- Only docs and plans are shared. A team is created by hand and holds chosen people: others working on the same project are not in it.
- Two-factor authentication is mandatory, once at login and once when opening the web IDE. A hacked account would otherwise give access to the machine.

Agreed on 2026-11-03, in requirements gathering. This overrides anything above or below it that it contradicts.

### What the hub is

- **wdx always runs on the user's own machine,** a node: daemon, CLI and tide. The hub, on a central server, does three things:
  - it connects one user's nodes to each other, for remote access: `(u1) wdx ⇄ hub ⇄ wdx (u1)`;
  - it shares docs and plans between users, through teams: `(u1) wdx ⇄ hub ⇄ wdx (u2)`;
  - it manages accounts, teams, and the registering and removing of nodes.
- **There is no web IDE and no terminal in a browser.** Work is local, or remote from one's own node, always with the wdx installed there. (This replaced, the same day, a tide shown in the browser through xterm.js, and with it the browser-key question.)
- **There is no portal, only sign-in pages.** The hub serves web pages for registering and logging in, and later for buying a plan, nothing else. `wdx` starts a login and the browser completes it, as Claude Code's and GitHub's CLIs do. That brings passkeys back. Everything else, from registering a node to turning on sync or remote access, is done with the wdx CLI.
- **Logging in on a node makes it one of the account's nodes,** reachable by that user until they log out there.
- **Remote access runs everything on the host.** The client's CLI or tide goes through its own daemon, over the network, to the host's daemon: `cli/tide → daemon (client) → hub → daemon (host)`. Commands run on the host, and the client never keeps a copy of the host's code. Paths are the host's.
- **A wdx works with one daemon at a time,** its own or a remote one, never both at once. Remote is chosen per session, never as a switch on the whole local wdx, so that agents running on the client, which call `wdx` themselves, are never sent to another node:
  - `wdx tide --node <name>` routes all of that tide's requests to the host. It shows only the host's agents, terminals and projects, never the client's. Its terminals are shells on the host, where `wdx` is the host's own. Leaving it and starting `wdx tide` with no `--node` works locally, as today.
  - The CLI works locally unless a command is given `--node <name>`.
  - Agents and terminals running on the client go on running, untouched, while a remote tide is open.
  - A remote tide's bottom bar is cyan, so remote is never mistaken for local. The colour comes from a semantic style in `internal/style`.
- **Proving it is you to a node, from anywhere:** each node that allows remote access shows a QR code for an authenticator app (Google Authenticator or any TOTP app), an entry of its own beside the hub login's (agreed 2026-11-03: only the host checks a code, and a secret shared by several nodes would have to travel between them). Its secret stays on that node and never reaches the hub. Opening a remote session asks for the current code, and the host checks it. The two nodes use the code in a password-authenticated key exchange (such as CPace) rather than sending it, so a hub in the middle gets one guess at it and cannot use a code it sees. After the first session each node keeps the other's key. There is no separate approval of a new node from an existing one, since that needs both machines at hand. A recovery key, printed at setup, enrols a new authenticator on a node one is at; remote access stays closed until then. A session lasts until it is idle.
- **Remote wdx through the hub uses the same protocol as a local daemon.** Pointing tide or the CLI at another of the account's nodes works as if it ran locally. A user may reach every node in their account, and never another user's.
- **The same experience wherever the hub runs:** on the user's own machine, on a server in a VPN, or on the public network. A local hub follows the same authentication as any other.
- **Four ways to run wdx:** local only, as today; a local hub on the same machine (possible, if of little use); our public hub; a hub someone else hosts, which behaves the same.
- **The public hub is open to anyone, with limited free usage and a paid plan.** Usage tracking and billing are deferred. At first, registration is closed to everyone but the human, for testing.
- **Phones and tablets** are not built for. If they are ever needed, it will be a native app.

### Teams and docs

- **Teams hold both people and projects.** A user may be in any number of teams, and a project, such as a monorepo, in any number of teams. When several teams match a user and a project, a doc goes to every one of them.
- **Sync is turned on per project.** With sync on, docs go to the hub, so that a member added later has them ready.
- **Who sees a doc:** private (the author's own nodes), team (teams the author picks among those holding the project), or project (every team holding it, later ones included).
- **What they can do:** viewer, commenter (the default), editor, owner, in the way Google Docs shares. Editor is given to named people or teams. There are no share links, which were a browser's idea.
- **Others' docs are namespaced by their owner with `/`:** `srjn45/hub-sync`. One's own docs show without it. Usernames are unique on a hub and never change.
- **Sync is hybrid:** a node keeps the latest revision of every doc it can see, and fetches older ones from the hub when asked. Search is local.
- **Approvals and comments on docs come later.**

### Security

- **mTLS** between daemon and hub. Very sensitive code and docs pass between them, so what more is needed is still to decide.
- **The hub never decodes remote traffic.** It sets up a remote connection between two of a user's nodes, then passes the bytes through the channels the nodes already hold. The two nodes encrypt end to end between themselves, since the traffic crosses the public network.

### Building and running it

- **The public hub runs on Fly.io,** on Fly's free `<app>.fly.dev` domain at first: one machine, SQLite on a volume, Litestream streaming it to object storage. Its SQL is written so that Postgres can be added when one instance is no longer enough, with requests routed to the instance holding a node's tunnel.
- **The hub has a database of its own,** apart from the wdx home's one database. AGENTS.md is to say so.
- **The hub takes any setting from the environment** as `WDX_HUB_<KEY>`, over its config file, for containers and secrets. wdx itself keeps reading only its home from the environment.
- **Code layout:** one Go module, two binaries. `cmd/wdx-hub`. `api/hub.yaml`, generated into `internal/hubapi`, the only package both sides import about each other. `internal/hub/` holds `hubcli`, `hubconfig`, `hubdb`, `server`, `account`, `ca`, `machine`, `relay`, `docsync` and `audit`. `internal/hublink` is the daemon's link to a hub. `deploy/hub/` holds the Dockerfile, `fly.toml`, Litestream's config, a compose file and a systemd unit. depguard keeps `internal/hub/...` and wdx apart. The names are agreed.
- **Shared code is extracted, not copied:** `internal/sqlite` (opening, migrations, backup) from `internal/db`, and `internal/settings` (the settings engine) from `internal/config`, each a step of its own.
- **No email in the MVP:** registration is closed, ~~the admin is created from a setup token~~ the first account is made with `wdx-hub user invite` like any other (the setup token was dropped in the commands stage, docs/HUB_COMMANDS.md), and passwords are reset on the server with `wdx-hub user reset`. SMTP comes when registration opens.
- **Stack and shipping:** the hub is in Go. One version and tag for wdx and the hub. Every release has `wdx-hub` archives and a multi-arch image on GHCR, signed with cosign and with an SBOM. (The MVP ships the archives only; the image, cosign and the SBOM come with the Fly deployment: decided 2026-11-07, docs/HUB_ARCH.md.)

MVP: registering and logging in, nodes, remote access between one's own nodes (in place of the web terminal), and docs going up to the hub (nothing comes back down to nodes yet). Later: the hub sending docs to other members.

### Security, approved

On top of mTLS, end-to-end encryption between nodes and the authenticator code checked by the host:

- ~~Client certificates last about a day and renew themselves.~~ Dropped in the review of 2026-11-03: the hub checks revocation on every connection, so a short certificate added nothing (docs/HUB_ARCH.md). Certificates last a year; a node logged out offline has deleted its key and cannot connect.
- A node accepts only a fixed list of API routes from a remote session.
- Docs are encrypted at rest on the hub, with keys from a key management service on the public instance.
- Every query on the hub is scoped to the account and its teams, with tests proving one account cannot read another's data.
- A strict CSP, no third-party scripts, and HSTS on the sign-in pages.
- Signed releases with an SBOM.
- Tamper-evident audit logs on the node and on the hub.
- A security review by someone else before registration opens to others.
- Later: end-to-end encrypted docs. Nothing in the design may rule it out.

### Commands, approved as the list

Each command's flags, output and errors are worked out in the commands stage, in docs/HUB_COMMANDS.md. MVP marks the first release.

| Command | | |
|---|---|---|
| `wdx hub login --hub-central \| --url [--name] [--no-browser]` | the browser signs up or logs in; this machine becomes a node | MVP |
| `wdx hub logout` | removes this node and its keys; works offline | MVP |
| `wdx hub status` | hub, account, node, connection, remote access, sessions | MVP |
| `wdx hub code setup` | the authenticator QR code for remote access, once | MVP |
| `wdx hub code recover` | a new authenticator from the recovery key, on a node one is at | MVP |
| `wdx hub node list`, `get`, `remove`, `rename` | the account's nodes | MVP but `rename` |
| `--node <name>` on every command the daemon serves | runs it on that node; never on `daemon`, `setting`, `update`, `observe` or `hub` | MVP |
| `wdx hub session open [--code]`, `list`, `close` | remote sessions | MVP |
| settings `hub.remote_access` (off by default), `hub.session_idle_timeout`; the hub's address is kept with the login (docs/HUB_COMMANDS.md) | through `wdx setting`, local to the node | MVP |
| `wdx project sync --name [--on \| --off]` | whether a project's docs go to the hub | MVP |
| `wdx team create`, `list`, `get`, `rename`, `remove` | teams | |
| `wdx team member add` (an invite), `remove`, `set --role` | members; `owner` transfers ownership | |
| `wdx team invite list`, `accept`, `decline`; `wdx team leave` | | |
| `wdx team project add`, `remove`, `list` | | |
| `wdx doc share --audience`, `grant`, `revoke`, `access` | sharing; `doc list` shows others' docs as `owner/name` | |
| `wdx-hub serve`, `user …` (`invite` makes the first account), `setting …`, `backup`, `version` | on the server, through its socket | MVP |
| tide: `--node`, the node in its title, the cyan bar | teams and sharing stay in the CLI at first | MVP |

Sign-in pages on the hub: register, log in, the second factor (passkey or TOTP, recovery codes), approving the CLI's login, and later buying a plan.

## Proposed

### Two jobs, kept apart

| | Sharing | Remote access |
|---|---|---|
| Gives | docs, plans | a shell and agents on a machine |
| Who | a team | the machine's owner only |
| If the hub is compromised | documents leak | code runs on every connected machine |
| Default | on for a project once shared | off, per machine |

Team membership never grants access to a machine.

### Rules

- The daemon works the same without a hub. No local command waits on the hub or fails because of it.
- The daemon only dials out, which works behind NAT. It keeps its Unix socket and opens no network port.
- Registering a machine shares nothing. A project is shared with a team once, explicitly; after that its docs and plans sync by themselves.
- A project on the hub belongs to a team, and team membership is what gives access to it. The remote URL only matches a local project to it: knowing a URL proves nothing.
- The daemon decides what reaches it: it can turn remote access off locally, and it keeps its own log of remote sessions. Terminal bytes are never logged, on either side.
- The hub can read what passes through it when a browser uses it, since it also serves that browser's code. Encrypting past it would protect little, so the protections are below, and self-hosting.

### Security

The aim: neither a hacked account nor a hacked hub is enough to open a machine.

- **Two factors with two different checkers.** The hub checks the login's. The daemon checks the one for opening the web IDE: its secret is created by `wdx hub connect`, shown once as a QR code on the machine, and never reaches the hub, which only passes the code along. So a stolen hub database opens nothing. It is a second entry in the authenticator app.
- **Losing the second factor** is fixed only at the machine, by a local command. Remote access is gone until then, which is the price of an attacker being unable to do the same.
- **Wrong codes** slow down at the daemon, which reports them.
- **The machine's credential** is a key pair made on the machine. The private key never leaves it and the hub keeps the public key, so a machine cannot be impersonated from what the hub stores. It is not the user's password, and can be revoked from either side.
- **Encryption** is TLS, with certificates verified and no flag to skip that. A company's own CA comes from the system's trust store. Plain connections are allowed only to localhost.
- **A remote session is visible:** it expires when idle, tide shows that one is open, the daemon logs each one, and `wdx hub remote off` ends it and refuses new ones, from the machine, whatever the hub says.
- **Shared content is untrusted.** A doc or plan from a teammate can reach agents on another machine and steer them, which is prompt injection. Nothing a teammate published runs by itself: running a plan copied from someone is a person's explicit action, showing what it will do.
- **Removing a team member** stops further sync, but cannot take back the copies they already have.
- **A security review by someone else** comes before the public instance is opened to anyone but its owner.

### The connection

- The daemon holds one outbound WebSocket, carrying streams over one connection.
- Hub to daemon: the web IDE's requests and terminal attaches. A request runs on the daemon's existing API handlers, limited to a list of allowed routes.
- Daemon to hub, sync: REST with a cursor, "what changed since revision N". A WebSocket message only says "something changed, come and look", so a missed message costs nothing and a reconnect needs no replay.
- The daemon's terminal attach over WebSocket is new, and is the same for any browser.
- The hub and daemons upgrade at different times, so the protocol has its own contract, `api/hub.yaml`, a version exchanged on connect, and a stated range of versions supported.

### Pairing

`wdx hub connect --url <hub>` prints a code, and the user approves it in the portal. The credential goes in a file of its own with mode 0600, since settings are logged. `wdx hub disconnect` works even when the hub cannot be reached. `hub.url` is the setting that points a daemon at a hub.

### Sync

- Each machine has a full copy, so reading works offline.
- The hub orders each document's revisions. A push names the revision it was based on. If that is no longer the latest, both are kept and the document is marked as conflicted, for a person to resolve. Nothing is overwritten.
- A plan has one owner, who runs it and updates its status. Teammates read it and can copy it.

### The hub itself

- `cmd/wdx-hub`, `internal/hub/...`, in the same release. It has a database of its own, separate from the daemon's rule of one database per wdx home.
- Self-hosting is one binary or one container image, with SQLite by default.
- Login is OpenID Connect, so that a company uses its own SSO, plus passwords for a small install with none. The first user of a new hub is its admin.

### The web IDE

Superseded on 2026-11-03: the web IDE is tide running on the machine, shown in the browser by a terminal built into `wdx-hub`'s static files. Using it on the same machine means running a hub locally; tide in one's own terminal remains the way with no hub at all.

### Order

Each part is useful alone, and access does not depend on docs or plans existing:

1. Accounts, pairing, machines and whether they are online
2. The tunnel and the web IDE's terminals and agents, for the owner's machines
3. Teams, then docs and plans syncing
4. ~~The rest of the web IDE, tab by tab as in tide~~ (dropped 2026-11-03: the web IDE is tide)

## Open

- Who may use the public instance.
- Whether one project can have several teams, each with its own docs and plans.
- Whether the web IDE on the same machine needs a hub.
- Whether a doc can stay private to its author in a shared project.
- What the ecosystem holds.
- Limits on the public relay: bandwidth, machines per user.
