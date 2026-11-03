# wdx-hub: commands

Status: the commands stage, started 2026-11-03. Each group is worked out here once agreed; docs/HUB.md has the requirements and the approved list of commands.

## `wdx hub login`, `logout`, `status`

Agreed on 2026-11-03.

### How login flows

Two flows (docs/HUB_ARCH.md, "Login"):

- **With a browser on the machine,** the default: the CLI opens the browser at the hub's login page and listens on loopback for the browser to come back. The user signs up or logs in, passes the second factor, and approves the node, changing its name on the page if the account has that name already. Only the machine that started the login can finish it, so a forwarded link gets an attacker nothing.
- **`--no-browser`,** for a shell over SSH: the CLI prints the hub's device page and a code. The user opens the page on any device and types the code, as `gh` has it; the link never carries it.

The browser is opened with `xdg-open` or `open`, an optional entry in `wdx deps`. Without it, or with `--no-browser`, nothing is opened. The CLI waits on a stream from the daemon until the login is approved, denied or expired.

### Which hub

- **No `hub.url` setting.** The hub's address is kept with the login, in `<home>/hub/`, and `wdx hub status` shows it. A setting could be changed while logged in to another hub, which nothing could make sense of.
- **Exactly one of `--hub-central` or `--url <url>`:** `--hub-central` is the public hub, named as Maven Central is, its address built into the binary so that nobody changes it; `--url` is any other. Neither, or both, is a `usage` error, so that a company's engineer never ends up on the public hub by leaving a flag out. `--url` completes the public hub's address.
- `--hub-central` ships with the public hub, after the first release. Until then `--url` is required, and the MVP runs its hub locally (docs/HUB_ARCH.md).
- A release that changes the public hub's address changes what `--hub-central` means for later logins only; a node already logged in keeps its address, and the hub tells its nodes of a move over the connection they trust.

### `wdx hub login`

| Flag | |
|---|---|
| `--hub-central` / `--url <url>` | the hub, exactly one |
| `--name <name>` | this node's name in the account; the hostname by default; unique in the account |
| `--no-browser` | print the link and code; open no browser |

- What the user must open, and with `--no-browser` the code, go to stderr while it waits. On approval stdout's first line is `logged in to <url> as <user>; this machine is node <name>`, with the hint `run wdx hub code setup to allow remote access` when no authenticator is set up.
- `--json` never prompts; the link, and the code, still go to stderr, so that a script or an AI can hand them to a person. stdout: `{"result": "logged_in", "hub": {"url", "user", "node": {"id", "name"}}}`.
- Results: `logged_in`.
- Errors: `hub_unreachable` (3); `hub_version_unsupported` (1); `already_logged_in` (1), hinting `wdx hub logout`; `name_taken` (1) only with `--json` and `--no-browser`, where the page cannot ask, hinting `--name`; `login_denied` (1), when Deny was clicked; `login_expired` (1), when not approved within about 10 minutes.

### `wdx hub logout`

- Tells the hub to revoke this node, deletes `<home>/hub/`, and ends remote sessions in and out.
- With the hub unreachable it still deletes everything here: the node's day-long certificate dies without its key. The result says so, hinting `remove it from another node: wdx hub node remove --name <name>`.
- No confirmation: nothing on the machine is lost. Projects, agents and docs stay, and docs already on the hub stay there.
- Results: `logged_out`, with `"hub_told": true|false`. Errors: `not_logged_in` (1).

### `wdx hub status`

Read-only: `{"hub": {...}}` with `url` and `user` (`null` when logged out), `node` (`{id, name}`), `connection` (`connected`, `connecting`, `offline`, `logged_out`, `revoked` when another node removed this one, hinting `wdx hub logout`, and `disabled` when the admin disabled the account), `remote_access` (`on`, `off`), `code_setup`, `sessions` (`{incoming, outgoing}`), and the hub's version with whether this wdx is in its range. Text's first line states the state, such as `connected to <url> as <user>, node <name>`, then hints for what is missing. Not being logged in is a state, not an error.

## `wdx hub code setup`, `recover`

Agreed on 2026-11-03.

Only the host checks a code, so each node that allows remote access has an authenticator entry of its own, such as `wdx: srjn45@desktop`, set up while at that node. A secret shared by several nodes would have to travel between them, and one node's leaking would open the others.

### `wdx hub code setup`

- Shows a QR code in the terminal and the secret as text, for an app that cannot scan; asks for one code back, to prove the entry works; then prints a recovery key, once.
- Interactive only: with `--json` or without a terminal it fails with a `usage` error, as `tide` does. The secret opens sessions into the node, so it is never printed where an AI's transcript would keep it.
- Results: `set_up`. Errors: `code_exists`, hinting `--replace`, which asks first: the old entry stops working.
- Until a code is set up, sessions into the node are refused with `code_not_set_up`, and `wdx hub status` hints at this command.

### `wdx hub code recover`

- Asks for the recovery key, hidden as it is typed, then sets up anew as `setup` does.
- Interactive only, on the node itself.
- Results: `recovered`. Errors: `recovery_key_wrong`; wrong keys slow down and are logged.

## `--node` and `wdx hub session`

Agreed on 2026-11-03.

### `--node <name>`

- On `agent`, `terminal`, `project`, `doc`, `worktree`, `message`, `tide` and `version`. Never on `daemon`, `setting`, `update`, `observe`, `deps`, `completion` or `hub`, which manage this machine: there it is a `usage` error.
- The CLI asks its own daemon for a stream to the node; the daemon opens it through the hub, and it ends, encrypted end to end, at the host daemon's API. The CLI speaks the API over it, so output, `--json` and exit codes are the host's.
- Node names complete from the account's nodes, through the daemon, within the completion timeout.
- One `debug_id` is logged by the CLI, its daemon and the host's daemon; the host also logs the session and the node it came from.
- In text mode a `--node` command writes `on node <name>` to stderr, in the cyan of a remote tide's bar, so that a remote result is never read as a local one. `--json` is unchanged.
- With no session to the node open: with a terminal it asks `code for <name>:` and opens one; with `--json` or `--no-input` it fails with `session_required`, hinting `wdx hub session open --node <name> --code <code>`.
- Refused for agents: where `WDX_AGENT_ID` is set, `--node` fails with `remote_not_for_agents`, so that an agent on the client never works on another node through a session its human opened. A person in a wdx terminal may use it. In a remote tide, agents work on the host with no `--node`.
- Errors: `not_logged_in`, `node_not_found`, `node_offline`, `remote_access_off` (the host's setting), `code_not_set_up`, `code_wrong` (slowed down and logged at the host), `node_version_unsupported`, `route_not_allowed`, `session_required`, `session_closed` (the session ended while the command ran: the host closed it, its daemon restarted, or it went idle), `remote_not_for_agents`.

### `wdx hub session open --node <name> [--code <code>]`

Opens a session ahead of time, for scripts and `--json`, which needs `--code`; otherwise the code is asked for. `--code` is never logged, where every other flag is. A code is single use at the host. Results: `opened`; `unchanged` when one is open already. A session ends when the host's daemon restarts.

### `wdx hub session list`

Read-only, `sessions`, incoming and outgoing: `id`, `direction`, `node`, `opened_at`, `last_used_at`, `idle_expires_at`.

### `wdx hub session close --id <id> | --all`

Closing an outgoing session ends one's own access; closing an incoming one, on the host, cuts off whoever is connected. Turning `hub.remote_access` off closes every incoming session at once. Results: `closed`. Errors: `session_not_found`.

### Idle timeout

`hub.session_idle_timeout`, 1 hour by default. The host's setting counts, since the host enforces it.

### `wdx tide --node <name>`

- Asks for the code in the terminal before tide's window starts, when no session is open.
- Its bottom bar is cyan and its title names the node.
- When the host closes the session, or the network is gone too long, a full-window message says `session to <name> closed` and offers to type a code again to reconnect, or quit.
- The host's own tide shows that a remote session is open, and from which node.

## `wdx hub node`, `wdx project sync`, the settings

Agreed on 2026-11-03.

### `wdx hub node list`

Read-only, `nodes`: `id`, `name`, `this` (the machine it runs on), `online`, `last_seen_at`, `version`, `os`, and `remote_access` and `code_setup` as the node last reported them. Text is a table with this node marked. Errors: `not_logged_in`, `hub_unreachable`.

### `wdx hub node get --id <id> | --name <name>`

One node, with its key's fingerprint, `key_known` (whether this node pinned it in an earlier session) and the sessions open with it.

### `wdx hub node remove --id | --name [--yes]`

- Revokes the node: the hub cuts its connection, ends its sessions and renews no certificate for it; the account's other nodes forget its key.
- Asks first, as every removal does; `--yes` answers, and `--json` needs it.
- The node it runs on is refused with `node_is_this`, hinting `wdx hub logout`, which cleans up here too.
- Results: `removed`. Errors: `node_not_found`.

### `wdx hub node rename --id | --name --new-name <name>`

Not in the MVP. Results: `renamed`. Errors: `name_taken`.

### `wdx project sync --id | --name [--on | --off]`

- With neither, shows `sync` (on or off), `pending` (changes not yet on the hub), `failed` (changes the hub refused, each with its doc and code, such as `doc_name_taken` when another of one's own nodes made a doc of that name in this repository; renaming the doc pushes it), `last_pushed_at` and the project's repository key.
- `--on` pushes every doc of the project with all its revisions, then each change: a new doc, a revision, a change of status, labels or name, a removal. It works logged out or offline; changes wait for the connection.
- `--off` stops pushing. What is on the hub stays there: teams will rely on it once docs are distributed. Removing it from the hub is a later command.
- A doc removed on the node is removed on the hub too.
- Needs a git remote, which is how a project is matched across machines: `no_remote` (422).
- Results: `changed`; `unchanged` when it was so already.
- Not in tide in the MVP; `docs/CLI_TIDE.md` gets the row.

### Settings

| Setting | Default | |
|---|---|---|
| `hub.remote_access` | `false` | Off closes every incoming session at once. On with no code set up is allowed; sessions are refused until one is, and `status` hints at it. |
| `hub.session_idle_timeout` | `1h` | The host's counts. |

No remote session can change a `hub.*` setting, through tide's Settings tab or its API or otherwise: they are changed only at the machine, so that a session cannot loosen the host's protections.

## `wdx-hub`, on the server

Agreed on 2026-11-03.

### The admin is whoever can reach the socket

- The admin commands talk to the running hub through a Unix socket in its home, mode 0600, as `wdx` talks to its daemon; never over the network. On Fly: `fly ssh console -C "wdx-hub user list"`.
- Reaching the socket is the admin's authority, and the MVP has no admin role. It gives nothing new: whoever has a shell on the server as the hub's user, or root, can already read its database and its CA's key. Every admin command is in the audit log.
- Later, when someone must administer the hub without a shell on its server (support staff, a web page for admins), admin becomes a role of an account, with its second factor, over the API. The socket stays for the server's own operator.
- There is no setup token and no `wdx-hub setup`: `wdx-hub user invite` makes the first account as it makes any other. One way rather than two, a link made when asked rather than a secret printed in logs that a log service keeps, and no race to be the first to register.

### `wdx-hub serve`

Runs the hub in the foreground, as a container's entry point. It listens on two addresses: the sign-in pages and the hub's API (plain HTTP behind a proxy's HTTPS, as on Fly, or TLS itself), and the node endpoint, always TLS of the hub's own requiring the node's client certificate, which a proxy passes through untouched. Its first start creates its CA and its database.

### `wdx-hub user`

| Command | |
|---|---|
| `invite [--expires <duration>]` | a one-time sign-up link, 24 hours by default, that works while registration is closed. Results: `invited` |
| `list` | `users`: `username`, `created_at`, `disabled`, `nodes` (a count) |
| `get --username <name>` | one user, with their nodes |
| `disable --username`, `enable --username` | disabling refuses their logins and cuts off their nodes. Results: `disabled`, `enabled` |
| `reset --username [--second-factor]` | a one-time link to set a new password, since there is no email; `--second-factor` also clears their passkeys and TOTP. Results: `reset` |
| `remove --username [--yes]` | deletes the account, its nodes and its private docs, asking first. Docs shared with a team stay with the team, marked as a removed user's, and its admins manage them. The username is never given again, so `<user>/<doc>` keeps meaning one person. Results: `removed` |

### The rest

`wdx-hub setting list`, `get`, `set` (the running hub reads the file again) and `write`, on the same engine as wdx's, with settings marked secret shown as `[secret]`; `wdx-hub audit verify`, which walks the audit chain and reports the first broken row; `wdx-hub backup --out <path>`, online through the socket, result `backed_up`; `wdx-hub version`, with the running hub's and the range of wdx versions it accepts.

### The hub's settings

Each also from `WDX_HUB_<KEY>`.

| Setting | Default | |
|---|---|---|
| `public_url` | required | the address nodes and browsers use, put in login links |
| `listen.web` | `:8080` | sign-in pages and API |
| `listen.nodes` | `:8443` | the node endpoint |
| `web.tls` | `off` | `off` behind a TLS proxy, as on Fly; `acme` (Let's Encrypt); `files`, with `web.tls_cert` and `web.tls_key` |
| `registration` | `closed` | `closed` or `open`; invites work either way |
| `trusted_proxies` | empty | addresses whose forwarding header gives the client's address, as Fly's proxy |
| `docs_key` | required | the master key sealing docs at rest; secret |
| `log.level`, `otel.endpoint` | `info`, empty | |
