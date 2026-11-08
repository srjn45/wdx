# wdx-hub: commands

Status: the commands stage, started 2026-11-03. Each group is worked out here once agreed; docs/HUB.md has the requirements and the approved list of commands.

## `wdx hub login`, `logout`, `status`

Agreed on 2026-11-03.

### How login flows

Two flows (docs/HUB_ARCH.md, "Login"):

- **With a browser on the machine,** the default: the CLI opens the browser at the hub's login page and listens on loopback for the browser to come back. The user signs up or logs in, passes the second factor, and approves the node, changing its name on the page if the account has that name already. Only the machine that started the login can finish it, so a forwarded link gets an attacker nothing.
- **`--no-browser`,** for a shell over SSH: the CLI prints the hub's device page and a code. The user opens the page on any device and types the code, as `gh` has it; the link never carries it.

The browser is opened with `xdg-open` or `open`, the optional entry `browser` in `wdx deps`. Without it, or with `--no-browser`, nothing is opened and the device flow is used, saying so. The CLI waits on the daemon's `GET /v1/hub/login`, the login's state as server-sent events, until the login is approved, denied, expired or replaced; Ctrl-C cancels it. A second login replaces a pending one, whose CLI ends with `login_cancelled`. Built in step 5 (the wdx doc HUB_LOGIN).

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

- What the user must open, and with `--no-browser` the code, go to stderr while it waits. On approval stdout's first line is `logged in to <url> as <user>; this machine is node <name>`; the hint `run wdx hub code setup to allow remote access`, when no authenticator is set up, is `wdx hub status`'s, which login's one line leaves out (step 7, the wdx doc HUB_TUNNEL).
- `--json` never prompts; the link, and the code, still go to stderr, so that a script or an AI can hand them to a person. stdout: `{"result": "logged_in", "hub": {...}}`, the hub's status as `wdx hub status` gives it.
- A login enqueues every doc of every synced project anew, in place of what waited, so that the hub logged in to gets every doc.
- Results: `logged_in`.
- Errors: `hub_unreachable` (3); `hub_version_unsupported` (1); `already_logged_in` (1), hinting `wdx hub logout`; `name_taken` (1), hinting `--name`, which the hub as built never sends, since its approval page always asks for another name when the account has one; `login_denied` (1), when Deny was clicked; `login_expired` (1), when not approved within about 10 minutes; `login_cancelled` (1), when another login replaced it.

### `wdx hub logout`

- Tells the hub to revoke this node, deletes `<home>/hub/`, and ends remote sessions in and out.
- With the hub unreachable it still deletes everything here: the node's certificate is useless without its key, which is gone. The result says so, hinting `remove it from another node: wdx hub node remove --name <name>`.
- No confirmation: nothing on the machine is lost. Projects, agents and docs stay, and docs already on the hub stay there. The changes waiting for the hub are deleted, so that none reaches another account; a project stays synced, and the next login sends its docs again.
- Results: `logged_out`, with `"hub_told": true|false` and `hub`, the status as it was; telling the hub is best effort, with a short timeout. Errors: `not_logged_in` (1).

### `wdx hub status`

Read-only: `{"hub": {...}}` with `url` and `user` (`null` when logged out), `node` (`{id, name}`), `connection` (`connected`, `connecting`, `offline`, `logged_out`, `revoked` when another node removed this one, hinting `wdx hub logout`, and `disabled` when the admin disabled the account), the hub's version (`hub_version`) with whether this wdx is in its range (`in_range`) and, when it is not, which side is `behind` (`node` or `hub`), for the hint to update; `remote_access` (`on`, `off`), `code_setup` and `sessions` (`{incoming, outgoing}`, the sessions open), hinting `run wdx hub code setup to allow remote access` with no code set up and `run wdx setting set --name hub.remote_access --value true to allow sessions into this node` with a code and remote access off. Text's first line states the state, such as `connected to <url> as <user>, node <name>`, then hints for what is missing. Not being logged in is a state, not an error.

## `wdx hub code setup`, `recover`

Agreed on 2026-11-03.

Only the host checks a code, so each node that allows remote access has an authenticator entry of its own, such as `wdx: srjn45@desktop`, set up while at that node. A secret shared by several nodes would have to travel between them, and one node's leaking would open the others.

### `wdx hub code setup`

- Shows a QR code in the terminal, drawn with half-block characters from `boombuler/barcode`'s matrix for a dark terminal, and the secret as text, for an app that cannot scan; asks for one code back, to prove the entry works, three tries at most; then prints a recovery key, once, five groups of four base32 characters, with `write this down: it is the only way to set up a new authenticator here`.
- Interactive only: with `--json` or without a terminal it fails with a `usage` error, as `tide` does. The secret opens sessions into the node, so it is never printed where an AI's transcript would keep it.
- Results: `set_up`. Errors: `code_exists`, hinting `--replace`, which asks first: the old entry stops working, and sessions open stay; `code_wrong` after the third wrong code; `not_logged_in`.
- The files are `code.secret`, `code.step` (the last step accepted, so a code is used once, also across a restart) and `recovery.hash` in `<home>/hub/`, 0600; `wdx hub logout` deletes them.
- Until a code is set up, sessions into the node are refused with `code_not_set_up`, and `wdx hub status` hints at this command.

### `wdx hub code recover`

- Asks for the recovery key, hidden as it is typed, then sets up anew as `setup` does.
- Interactive only, on the node itself.
- Results: `recovered`, with a new recovery key; the old one is spent. Errors: `recovery_key_wrong`, `code_not_set_up`, `not_logged_in`; after a wrong key the next try waits, from 1 second doubling to 5 minutes, and each is logged and in the node's audit chain.

### `wdx hub audit verify`

Walks the node's audit chain, `hub_audit` in `wdx.db`: each row's hash is the SHA-256 of the one before and its own fields, for what matters about access to the node (`code.set_up`, `code.replaced`, `code.recovered`, `code.wrong`, `recovery_key.wrong`, `session.opened`, `session.closed` with the reason, `remote_access.on`, `remote_access.off`, `node_key.mismatch`). Read-only, and works logged out: `{"audit": {"rows", "head"}}`, `head` the last row's hash or `null` for an empty chain, as `wdx-hub audit verify` has it. Errors: `audit_broken`, with the first row that does not match (`id`, `at`) in its details.

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
- Errors: `not_logged_in`, `node_not_found`, `node_offline`, `remote_access_off` (the host's setting), `code_not_set_up`, `code_wrong` (slowed down and logged at the host), `node_version_unsupported`, `node_key_mismatch`, `too_many_streams`, `route_not_allowed`, `session_required`, `session_closed` (the session ended while the command ran: the host closed it, its daemon restarted, or it went idle), `remote_not_for_agents`.

### `wdx hub session open --node <name> [--code <code>]`

Opens a session ahead of time, for scripts and `--json`, which needs `--code`; otherwise the code is asked for. `--code` is never logged, where every other flag is. A code is single use at the host. Results: `opened`, with the session's id and the host's idle timeout; `unchanged` when one is open already, which asks nothing. A session ends when the host's daemon restarts. Errors: `not_logged_in`, `node_not_found`, `node_offline`, `remote_access_off` (hinting the setting on the host), `code_not_set_up`, `code_wrong`, `node_version_unsupported`, `node_key_mismatch` (the node showed a key other than the one pinned for it: audited on both nodes), `too_many_streams` (the account has 32 streams through the hub open or waiting), `hub_unreachable`. `--node` completes from the account's nodes.

Wrong codes slow down at the host, per client node and in total: after one, the next is refused unread, as `code_wrong` saying how long to wait, for 1 second, doubling to 5 minutes, until a right one. The total is because a hacked hub can make as many new nodes as it likes; the cost is that someone guessing makes the owner wait too. Wrong codes never turn remote access off.

### `wdx hub session list`

Read-only, `sessions`, the open ones, incoming and outgoing: `id`, `direction`, `node`, `opened_at`, `last_used_at`, `idle_expires_at`. An outgoing session's row stays open until the client learns it ended: at its next stream, refused with `session_closed` and the host's reason, or at `close`.

### `wdx hub session close --id <id> | --all`

Closing an outgoing session ends one's own access; closing an incoming one, on the host, cuts off whoever is connected. Turning `hub.remote_access` off closes every incoming session at once. Results: `closed`. Errors: `session_not_found`.

### Idle timeout

`hub.session_idle_timeout`, 1 hour by default. The host's setting counts, since the host enforces it.

### `wdx tide --node <name>`

- Asks for the code in the terminal before tide's window starts, when no session is open.
- Its bottom bar is cyan and its title names the node.
- When the host closes the session, or the network is gone too long, a full-window message says `session to <name> closed` and offers to type a code again to reconnect, or quit.
- The host's own tide shows that a remote session is open, and from which node.
- Its view is not saved: it starts with the default view each time, since tide's saved tabs and view are the host's own tide's (docs/HUB_ARCH.md, "Tide's state in a remote session"); `C-q r` keeps it as before. Keeping it in the client's daemon per node is later.
- It takes the same `tide.lock` as a local tide, so one tide runs per machine, local or remote: leave one to start the other, or `tide_running` says which is running. A lock per node can come later. (Decided 2026-11-07.)

## `wdx hub node`, `wdx project sync`, the settings

Agreed on 2026-11-03.

### `wdx hub node list`

Read-only, `nodes`: `id`, `name`, `this` (the machine it runs on), `online` (it has a control connection now), `last_seen_at`, `version`, `os`, `created_at`, and `remote_access` and `code_setup` as the node last reported them in its `state` control message, for display: the host decides every session itself. Text is a table with this node marked. Errors: `not_logged_in`, `hub_unreachable`.

### `wdx hub node get --id <id> | --name <name>`

One node, with its key's `fingerprint` (`SHA256:` and the key's SHA-256 in base64); `key_known` (whether this node pinned it in an earlier session) and `sessions`, those open with it both ways. Errors: `not_logged_in`, `node_not_found`, `hub_unreachable`.

### `wdx hub node remove --id | --name [--yes]`

- Revokes the node: the hub cuts its connection, ends its sessions and renews no certificate for it; the account's other nodes forget its key.
- Asks first, as every removal does; `--yes` answers, and `--json` needs it.
- The node it runs on is refused with `node_is_this`, hinting `wdx hub logout`, which cleans up here too.
- Results: `removed`. Errors: `node_not_found`.

### `wdx hub node rename --id | --name --new-name <name>`

Not in the MVP. Results: `renamed`. Errors: `name_taken`.

### `wdx project sync --id | --name [--on | --off]`

- With neither, shows `{"sync": {...}}`: `on`, `pending` (changes not yet on the hub), `failed` (docs the hub refused, each with its id, name and code, such as `doc_name_taken` when another of one's own nodes made a doc of that name in this repository; renaming the doc pushes it), `last_pushed_at`, `repo_key` and `logged_in`. Text: `sync on, 3 changes waiting, last pushed 2 minutes ago`, then each failed doc with its code; logged out, the hint `not logged in: changes wait until wdx hub login`, not an error. With `--on` or `--off` the same object, with `result`.
- `--on` pushes every doc of the project with all its revisions, in batches over `POST /v1/docs/changes` (docs/HUB_ARCH.md, "Docs going to the hub"), then each change: a new doc, a revision, a change of status, labels, folder or name, a removal. Docs only: plans share their table (docs/HUB_ARCH.md) and come later. It works logged out or offline; changes wait for the connection.
- `--off` stops pushing and deletes the changes waiting. What is on the hub stays there, as it does when the project is removed: teams will rely on it once docs are distributed. Removing it from the hub is a later command.
- A doc removed on the node is removed on the hub too.
- Each change carries the project's current repository key, so a project whose remote moved has its docs moved on the hub as each next changes; `--off` then `--on` moves them all at once.
- Needs a git remote, which is how a project is matched across machines: `no_remote` (422, exit 1), hinting to add one. `not_found` for a project not there. `--on` and `--off` together are a `usage` error.
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

Runs the hub in the foreground, as a container's entry point. It listens on two addresses: the sign-in pages and the hub's API (plain HTTP behind a proxy's HTTPS, as on Fly, or TLS itself), and the node endpoint, always TLS of the hub's own requiring the node's client certificate, which a proxy passes through untouched. Its first start creates its CA and its database. It also serves the admin socket, and logs JSON lines to stdout, where a container's or a service's log is kept; so it refuses `--json` with `usage`, as `wdx tide` does (step 3, the wdx doc HUB_SKELETON).

### `wdx-hub user`

Each is an operation of `api/hub.yaml` tagged `admin`, built in step 4 (the wdx doc HUB_ACCOUNTS). Every one also fails with `hub_unavailable` (3), hinting `run wdx-hub serve`. A user is `username`, `created_at`, `last_login_at` (`null` when they never logged in), `disabled` and `second_factor` (`totp`, or `null` while none is enrolled).

| Command | | Results | Errors |
|---|---|---|---|
| `invite [--expires <duration>]` | a one-time sign-up link, `<public_url>/signup?invite=<token>`, 24 hours by default and at most 30 days, that works while registration is closed: `{"result": "invited", "invite": {"link", "expires_at"}}` | `invited` | `invalid_argument` |
| `list` | `users`; removed users are left out; `nodes`, a count of their nodes not revoked | | |
| `get --username <name>` | one user, with `sessions`, the count of their browser sessions that have not ended, `nodes`, and `node_list`, each node's `id`, `name`, `os`, `version`, `created_at`, `last_seen_at` and `online` | | `not_found` |
| `disable --username`, `enable --username` | the user as they now are; disabling ends their browser sessions, refuses their logins, and cuts off their nodes at once | `disabled`, `enabled`, `unchanged` | `not_found` |
| `reset --username [--second-factor]` | a one-time link to set a new password, since there is no email, `<public_url>/reset?token=<token>`, 24 hours; a new reset voids an earlier one; `--second-factor` also removes their TOTP and recovery codes, and from the passkeys step their passkeys: `{"result": "reset", "reset": {"link", "expires_at", "second_factor"}}` | `reset` | `not_found` |
| `remove --username [--yes]` | the user as they were; asks first, and `--json` needs `--yes`. Keeps the row, so the username is never given again and `<user>/<doc>` keeps meaning one person, and deletes their password hash, factor, recovery codes, sessions, resets, node logins and nodes, cutting the nodes off at once; step 6 adds their private docs. Docs shared with a team stay with the team, marked as a removed user's, and its admins manage them | `removed` | `not_found`, `cancelled` (answered no) |

An invite or reset link is a secret: printed on a line of its own, since the admin must hand it on, but never logged; the server's `request returned` and the command's `command returned` measure it as `link_bytes`.

### The rest

`wdx-hub setting list`, `get`, `set` (the running hub reads the file again) and `write`, on the same engine as wdx's, with settings marked secret shown as `[secret]`; `wdx-hub audit verify`, which walks the audit chain: `{"audit": {"rows", "head"}}`, `head` the last row's hash or `null` for an empty chain, or `audit_broken` (exit 1) with the first broken row's `id` and `at` in `details`; `wdx-hub backup --out <path>`, online through the socket, result `backed_up`; `wdx-hub version`, with the running hub's and the range of wdx versions it accepts.

### The hub's settings

Each also from `WDX_HUB_<KEY>`. Without `public_url` every command fails at startup with `usage`, but `setting set` and `write`, so that a new hub can be given one.

| Setting | Default | |
|---|---|---|
| `public_url` | required | the address nodes and browsers use, put in login links |
| `listen.web` | `:8080` | sign-in pages and API |
| `listen.nodes` | `:8443` | the node endpoint |
| `nodes_url` | empty | where nodes reach the node endpoint, `https://<host>[:<port>]`, when a proxy or a port mapping makes it differ from `listen.nodes`; empty is `public_url`'s host with `listen.nodes`' port. Sent at login and kept in the node's `login.json`; the node endpoint's certificate names its host |
| `web.tls` | `off` | `off` behind a TLS proxy, as on Fly; `acme` (Let's Encrypt); `files`, with `web.tls_cert` and `web.tls_key` |
| `registration` | `closed` | `closed` or `open`; invites work either way |
| `trusted_proxies` | empty | addresses or CIDR ranges, comma-separated, whose `X-Forwarded-For` gives the client's address, as Fly's proxy |
| `master_key` | required | 32 bytes as base64, secret; seals the TOTP secrets and, in step 6, the docs' data keys. `serve` refuses to start without it, hinting `make one with: head -c 32 /dev/urandom | base64` |
| `log.level`, `otel.endpoint` | `info`, empty | |
