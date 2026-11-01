# wdx-hub

Status: notes from an open discussion on 2026-11-01, far from being built. Nothing here is final: decisions are worked out when the hub is worked on. "Agreed" is what the human said; "Proposed" came from the AI and was not agreed. docs/IDE.md has the wider picture.

## Agreed

- `wdx-hub` is a separate app in this repository, open source like the rest.
- It serves a web portal where a user logs in and registers their machines.
- The projects they work on are shared with their team. Docs and plans sync across teammates, so that everyone is aligned and can reuse what others published.
- It comes with a web IDE, to reach a machine from the same machine or a remote one.
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

Static files built into `wdx-hub`. Using it on the same machine means running a hub locally, or going through a remote one. tide remains the way with no hub at all.

### Order

Each part is useful alone, and access does not depend on docs or plans existing:

1. Accounts, pairing, machines and whether they are online
2. The tunnel and the web IDE's terminals and agents, for the owner's machines
3. Teams, then docs and plans syncing
4. The rest of the web IDE, tab by tab as in tide

## Open

- Who may use the public instance.
- Whether one project can have several teams, each with its own docs and plans.
- Whether the web IDE on the same machine needs a hub.
- Whether a doc can stay private to its author in a shared project.
- What the ecosystem holds.
- Limits on the public relay: bandwidth, machines per user.
