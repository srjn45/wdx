# wdx-hub: architecture

Status: approved 2026-11-03, after a review end to end the same day whose findings are under "Decided while drafting"; reviewed again against the code on 2026-11-07, whose findings are under "Decided in the review against the code" and folded into the sections they touch. docs/HUB.md has the requirements, docs/HUB_COMMANDS.md the commands this serves.

## The picture

```
 client node                                hub                                  host node
┌──────────────────────┐               ┌───────────────────────┐            ┌──────────────────────┐
│ cli / tide           │               │ web endpoint          │            │ daemon               │
│   │ unix socket      │               │  sign-in pages, API   │            │   ▲ the same API     │
│   ▼                  │   HTTPS       │  (public certificate) │   HTTPS    │   │ handler, behind  │
│ daemon ──────────────┼──────────────►│                       │◄───────────┼── │ remote-session   │
│   hublink            │   mTLS        │ node endpoint         │   mTLS     │   │ rules            │
│   ├ control WS ──────┼──────────────►│  (hub's own CA)       │◄───────────┼── hublink            │
│   └ stream WS ═══════╪══════════════►│  relay: joins two ════╪════════════╪═► stream WS          │
│      └ TLS between the two nodes, which the hub cannot read  ║            │                      │
└──────────────────────┘               └───────────────────────┘            └──────────────────────┘
```

- **Two endpoints on the hub.** The web endpoint serves the sign-in pages and the device flow, under an ordinary public certificate, before a node has any key the hub knows. The node endpoint is TLS of the hub's own, demanding a node's client certificate: everything a logged-in node does goes there.
- **One control WebSocket per node,** held open by the daemon, dialled out, reconnecting with backoff. It carries small messages: hello and versions, certificate renewal, "a stream is waiting for you", "docs changed", the node's reported state.
- **One WebSocket per stream.** A remote session's request and an attach each open their own WebSocket on the node endpoint (a doc push needs none: it is a batch per request, `POST /v1/docs/changes`), rather than many streams multiplexed in one connection. No multiplexing library (the common one, `hashicorp/yamux`, is MPL-2.0 anyway), no flow control of our own, and a stuck terminal never holds up a doc push. The cost is a TLS handshake per stream, a few milliseconds, which sessions keep few by reusing their streams.
- **The relay** pairs a client node's stream with a host node's, when both belong to one user, then copies bytes both ways and nothing else.

## Remote sessions, end to end

### What is inside a stream

```
cli ──unix socket──► client daemon ──TLS 1.3 between nodes, in a stream WS──► host daemon ──► API handler
     plain HTTP,                    node keys, pinned;                                    remote rules
     local                          CPace on the first stream of a session
```

- **The CLI never holds a node key.** It asks its own daemon to connect (`POST /v1/hub/nodes/{node}/connect`, upgraded to a byte stream), and speaks plain HTTP over that to the host's API. The client daemon does the encryption, with keys only it reads.
- **Between the nodes: TLS 1.3**, each side presenting a self-signed certificate of its own node key, the client the TLS client. A node that has met the other before checks its pinned key (`hub_known_nodes`), refusing any other with `node_key_mismatch`, recorded in the audit chain on both sides; it never takes the hub's word for it. Inside, the first message, a JSON frame with a 4-byte length, says what the stream is for: `open` (CPace, with the code's step and the protocol version, 1 for now), `stream` (a session the host has open) or `close`.
- **Opening a session: CPace with the code.** The first stream of a session runs CPace (a password-authenticated key exchange) keyed with the host's authenticator code, then binds it to the TLS channel: each side sends a MAC, under CPace's key, of the TLS exporter and both node keys. A hub in the middle cannot pass that without the code, and has one guess at it. Then each node pins the other's key.
- **The code's time step travels with the CPace message.** The host accepts the code of the current 30-second step and its neighbours, but CPace derives its generator from the one password both sides agreed on before either sends anything, so the host cannot try three. The client sends the step index it used, which is not secret, and the host uses that step's code if it is within tolerance. A code is single use at the host: two sessions opened within one step need two codes. The last step accepted is kept in `code.step`, so this holds across a restart too.
- **Wrong codes slow down at the host, per client node and in total:** after a wrong one, the next is refused unread for 1 second, doubling to 5 minutes, until one succeeds, as `code_wrong` saying how long to wait. The total counts because a hacked hub can issue certificates for as many new nodes as it likes; the cost is that someone guessing makes the owner wait too. The host takes one open at a time, so that slowing cannot be raced. Wrong codes never turn remote access off. Each is logged with the client node and recorded in the node's audit chain.
- **Later streams of a session** need no code: TLS with the pinned keys, plus the session's id, which the host checks in its table of open sessions.
- **A session ends when the host's daemon restarts:** at startup the host closes every session, and the client enters a code again; tide's reconnect screen covers it. Streams end with the connection they ran in; a command cut off mid-way fails with `session_closed`.
- **Asking for a stream has a timeout:** when the host's control socket is down at that moment, the hub cannot tell it, and the request ends as `node_offline`.
- **Every stream the daemon serves is an HTTP Upgrade,** never a request whose body streams: the connect stream and the attach relay hijack their connection. The daemon's `logRequests` reads every request body whole before a handler runs, and `oneChangeAtATime` holds the changes lock for every request that is not a GET until it returns, so a streaming body would never arrive and a stream would hold every change up. These routes are exempt from the lock as the view paths are, and the response recorder implements `http.Hijacker`. (Review of 2026-11-07.) The login's state stream, which has no body and changes nothing, is instead `GET /v1/hub/login` with server-sent events, as `streamProject` is; `streamOperations` is the set of these, whose response is not logged (with step 9's `GET /v1/hub/sessions/events`, three), and every `/v1/hub` route skips the changes lock (`isHubPath`), since none may wait behind a change, nor a change behind the hub (step 5, the wdx doc HUB_LOGIN).
- **CPace:** `filippo.io/cpace` (BSD-3-Clause) is small, but is marked experimental, not maintained, and follows a 2021 draft the IRTF has since restructured, so its test vectors may not match the current draft's. It is copied into `third_party/cpace`, a module of its own that `go.mod` uses in its place as `cancelreader` is and `make test` also tests, with its README saying why; the copy is unchanged but for its `go.mod`, which moves `gtank/ristretto255` to v0.2.0, on `filippo.io/edwards25519` (both BSD-3-Clause), and its test vector still matches (step 7). CPace's context binds both node keys, the TLS exporter value and the step. It is the one piece of cryptography wdx carries itself rather than taking from Go's standard library, and is named as such in the scope of the outside security review.

### The host's rules for a remote session

The host serves a session with its usual API handler, behind a layer that:

- serves only the operations `remoteOperations` allows: one table classifying every operation of `api/openapi.yaml`, served through a session or refused with its reason, and a test fails on one in neither, so that a new operation is a decision rather than a silent refusal or a silent opening. Refused: every `/v1/hub/...` operation (so no session opens a session onward to a third node, no code setup, no login or logout), the settings (so no `hub.*` setting), the daemon's own (`getDaemon`, `reloadDaemon`; stopping the daemon and updating wdx are no operations), and tide's state (`/v1/tide/view`, `/v1/projects/{project}/view` and `/v1/projects/{project}/tabs`; see "Tide's state in a remote session"). A refused operation, or a route the host does not have, is `route_not_allowed` (403), the latter hinting to update the host. The end-to-end test running every `--node` command's examples against a host checks that what the CLI calls is served (step 8);
- refuses a request without a live session, checking it at every request, and moves the session's idle time on; a session that closes cuts its streams, and a command cut off fails with `session_closed`. Bytes on an attach stream are activity too: a person typing in a remote terminal makes no request;
- logs each request with the session and the node it came from (`session_id`, `from_node`), under the client's `debug_id`;
- closes every incoming session when `hub.remote_access` goes off.

### Attach

Locally, clients dial the holder's socket, so that a session outlives a daemon restart. Remotely, the host daemon relays: `POST /v1/terminals/{terminal}/attach` (`attachTerminal`, the one operation step 9 moved to the allowed side of `remoteOperations`) upgraded to a stream that speaks the holder's protocol, which the daemon passes to the holder's socket, for any kind of terminal by `id:`, so agents and editors too. ATTACH.md planned this relay for the web; it serves remote attach first. The holder learns the client's pid from the socket (`SO_PEERCRED`), which for a relayed client is the daemon's, so the relay's `hello` says the client is remote and from which node, and the holder logs that. That `hello` is the holder protocol's version 5: the relay refuses an older holder with `holder_version_unsupported` (409) until its terminal restarts. Through a session, the relay moves the session's idle time on at most once a minute while bytes flow either way; its log counts the bytes, never shows them.

TIDE is handed a dial function, `Options.DialTerminal` (locally the holder's socket under its home, remotely `client.AttachStream` then `holder.AttachConn`), and `Options.Node`, the host's name; `holder.AttachConn` attaches over the connection it is handed, and `holder.Attach` dials the socket and calls it; `client.AttachStream` asks for the Upgrade through whatever the client dials. `terminal attach --node` and `agent attach --node` attach the same way. A session that ends while attached closes the stream, which attach reports as `connection_lost`. (Review of 2026-11-07; built in step 9.)

### Tide's state in a remote session

docs/TIDE_STATE.md, built the day this design was written: a daemon has one tide, and tide's tabs and view are rows in that daemon's database. A remote tide that used the host's routes would overwrite the tabs and cursor of the host's own tide. So those routes are not on the list, and in the MVP a remote tide keeps its view in memory, starting with the default view each time; `C-q r` keeps it through `--resume` as before. Keeping it in the client's daemon per node, as TIDE_STATE.md foresees, is a later step: a tab would then point at another daemon's resources, which no cascade reaches. A remote tide also takes the client's `tide.lock`, which is per home, so one tide runs per machine, local or remote; a lock per node can come later. (Decided 2026-11-07.)

What else differs in a remote tide (decided 2026-11-08, the wdx doc HUB_REMOTE_TIDE):

- **No Settings tab:** settings are refused through a session, so the menu's entry says `settings are changed on <name> itself`, and `--resume` drops a settings tab.
- **A typed path for a new project:** a field for the host's absolute path, checked by the host, in place of the directory browser, which reads the client's files; no API lists the host's directories.
- **The reconnect rule:** `session_closed` replaces the window at once with `session to <name> closed` and why, from the reason in its details; `node_offline` or `hub_unreachable` show `reconnecting to <name>` on the bar, and the message after 30 seconds without a stream opening (an option, `cli.Env.Timeouts.TideSessionLost` in tests). On the message, a code typed opens a new session and tide goes on with its view; `C-q` quits.
- **The cyan bar and the title:** the bar takes the style `Remote` in every `tide.theme` and starts with the node; the window's title is `wdx tide · <name>`. The daemon version check is not made, the API version standing in for it.
- **The host's indicator:** the host's own tide shows `remote: <node>, ...` at the start of its bar while any incoming session is open. It follows `GET /v1/hub/sessions/events` (`streamHubSessions`), server-sent events of the incoming sessions, one event with the list at start and one on every open and close; empty when logged out or remote access is off. The stream is refused through a session, as every `/v1/hub` operation is, and a remote tide does not follow it.

### Paths and the working directory with `--node`

- A relative path the daemon receives (`project create --path`, `project open --local`), given with `--node`, is a `usage` error: `path must be absolute on node <name>`. Absolute paths go as they are, and mean the host's.
- Files the CLI reads or writes itself (`doc` and `plan`'s `--path` and `--out`) stay the client's, made absolute against the client's working directory: only their content travels (step 8).
- Commands that take their project from the working directory (`default: the one the working directory is in`) need the project flags with `--node`, since this directory is not the host's.

### Versions between nodes

The API carries `Wdx-Version` already, but releases are not what nodes must agree on: the API is. `info.version` of `api/openapi.yaml` is an integer, `api.CurrentVersion` (1), raised by every `feat!:` that changes the API, and `api.OldestCompatible` beside it is the oldest this release still talks to, both kept by hand in `internal/api/headers.go`. Through a session the CLI sends its version in `Wdx-Api-Version` on every request and the host answers with its own: the host refuses a client older than its `OldestCompatible`, and the CLI a host older than its own, or one that sends none; either is `node_version_unsupported`, naming which side to update in its details (step 8). The check is end to end, between the CLI and the host's daemon; the client daemon only carries the stream. The client's warning that the daemon runs another version (`VersionMismatch`) would fire on every remote command when the two machines run different releases; with `--node` this check replaces it. `wdx version --node <name>` shows the host's release and API version.

## The hub's certificate authority

- **The CA:** an Ed25519 key and a self-signed certificate valid 10 years, made at the hub's first start, in its home with mode 0600.
- **The node endpoint's certificate** is issued by the CA for the node endpoint's host name, `nodes_url`'s, which by default is `public_url`'s host with `listen.nodes`' port. Nodes trust the CA they were given at login, never the system's store, for this endpoint: a rogue public CA cannot stand in for it.
- **A node's identity is its Ed25519 key,** made on the node at login. Its certificate (`ca.IssueNode`) names the node (`URI:wdx-node:<node-id>`) and lasts a year; the hub issues a new one over the control socket once two thirds of its life have passed, checked on `welcome` and daily, from `nodes.cert_expires_at`.
- **Revocation is the hub's check, not the certificate's life.** The hub is the only party that verifies node certificates and it has the database, so it checks `revoked_at` on every connection and cuts any open one at once. A short-lived certificate would add nothing to that, would not limit a stolen key, which can renew, and would need a way for a node offline longer than its certificate to renew without one. (The day-long certificates of the approved security list were dropped for this on 2026-11-03.)
- **Offline logout** deletes the key, so the node can never connect again; the hub's row stays until `wdx hub node remove` from another node.
- **Between nodes,** certificates are self-signed by each node's key and verified by pinning and CPace, never through the CA: a node never takes the hub's word for who the other node is.

## Login

Two flows, since the two cases differ in what can be trusted.

### With a browser on the machine: the authorization code flow

The way Claude Code and `gh` log in: only the machine that started the login can finish it, so a login link forwarded to someone else is useless.

1. The daemon makes the node key. The CLI listens on `127.0.0.1` on a port the system picks, for this login only, and opens the browser at the hub's login page with the node's name, OS and public key, a PKCE challenge, and the callback address.
2. In the browser: sign up or log in, second factor, then the approval page, which names the node and lets the user change its name if the account has one already.
3. The hub sends the browser back to the callback with a one-time authorization code; the CLI hands it to the daemon.
4. The daemon exchanges it at the web endpoint, with the PKCE verifier, for the node's id, its first certificate, the hub's CA certificate and the node endpoint's address, the hub's setting `nodes_url`.
5. The daemon writes `<home>/hub/`, connects the control socket, and the CLI prints `logged_in`.

The daemon opens no network port: the listener is the CLI's, on loopback, for the minutes of the login.

### Without a browser (`--no-browser`): the device flow

For a shell over SSH, where the browser is on another device.

1. The daemon calls `POST /v1/node-logins` with the node's name, OS and public key, and gets a device code (secret, for the daemon) and a user code to show (`WDX-4F7K-9QPA`).
2. The CLI prints the hub's device page's address and the code. The link does not carry the code: the user types it on the page, as `gh` has it, so that a link sent by an attacker to have their node approved gets them nothing without also talking the victim into typing a code. The page says what approving means.
3. In the browser: sign up or log in, second factor, the code, then the same approval page.
4. The daemon polls `POST /v1/node-logins/token` with the device code, as RFC 8628 has it, until approved, denied or expired (10 minutes), then as above.

### Trust

The hub's CA certificate and the node endpoint's address come over the web endpoint's TLS, which the system's trust store verifies (or plain HTTP to localhost); that is where trust in the hub begins.

## Docs going to the hub

- **An outbox on the node.** Each change of a doc in a synced project (created, a revision, status, labels, folder, name, removed) adds a row to `hub_outbox` in the same transaction as the change, so that no change is ever lost or pushed twice in the wrong order. `project sync --on` adds every doc and revision the project has; `--off` deletes its rows waiting. The table has the project's id, so a project removed takes its rows with it, and `projects.hub_pushed_at` says when a change of it last reached the hub.
- **The doc store writes the outbox itself,** inside the transaction of each change, when the project's `hub_sync` is on (an `INSERT … SELECT` over `projects`). No other package can join that transaction, and the store's one hook, `OnChange`, runs after the commit, where a crash between the two writes would lose a change. `hublink` only reads and deletes the rows. (Decided 2026-11-07.)
- **Plans share the table.** Since docs/PLANS.md, plans are rows of `docs` with `kind` (`doc` or `plan`), `parent_id` and a stored name `plan:<name>`, and since docs/FOLDERS.md every row has a `folder`. The pusher sends only `kind = doc` in the MVP, as agreed; each change carries the doc's `kind` and `folder`, and the hub's `docs` table holds both, so a folder arrives with its doc and plans need no change of the hub's tables when they come. `parent_id` comes with plans. (Decided 2026-11-07.)
- **The pusher** in hublink sends the outbox in order, in batches of up to 100 changes or 8 MiB over `POST /v1/docs/changes`, answered change by change, while the control connection is up: at connect, when the doc store's `OnChange` kicks it, and after a failure with the control connection's backoff. It deletes each row once the hub acknowledges it; a row refused with a code of its doc's own (`doc_name_taken`, `doc_too_large`, `revision_gap`) is marked failed and holds up that doc's later rows only, until the doc changes again. The hub applies each change idempotently, by the doc's id and revision, so a batch retried after a lost acknowledgement does nothing twice. No stream: a batch retries simply. `hublink` declares the `Outbox` it needs, and the daemon hands it `doc.Outbox`. (Step 6, the wdx doc HUB_DOCS_PUSH.)
- **Logout clears the outbox, and a login enqueues every synced project anew**, in place of what waited, before the control connection starts: rows written for one account never reach another, and a hub logged in to anew gets every doc.
- **Offline**, rows wait; `project sync` shows how many.
- **On the hub**, docs are stored by owner, by the project's repository key (`git.RepoKey` of the remote), and by the doc's id, which is random and global already. Each change carries the project's current key, and the hub moves the doc to it: a project whose remote moved has its docs moved as each next changes, and `--off` then `--on` moves them all at once; rare, and `project sync` shows a project's key.
- **A project removed, or `--off`, leaves its docs on the hub**: `wdx project remove` is about this machine. Removing them from the hub is a later command; `wdx-hub user remove` deletes a user's docs.
- **Nothing comes back down** in the MVP, and plans are not pushed.
- **Two of one user's nodes can each create a doc of the same name in one repository**, with different ids, and the MVP brings nothing down that would let either know. The hub refuses the second with `doc_name_taken`; the node marks the outbox row failed, `project sync` shows it under `failed`, and the user renames the doc, which pushes it. Nothing is overwritten or dropped in silence.

### Encrypted at rest

- Each doc has a data key; its revisions are gzipped, then sealed with AES-256-GCM under it. The data key is stored wrapped by a master key.
- The master key is the setting `master_key`, which also seals the TOTP secrets (step 4, the wdx doc HUB_ACCOUNTS): on Fly a secret (`WDX_HUB_MASTER_KEY`), self-hosted from the environment or the config file. Fly has no key service; a secret is what it offers.
- So a stolen database or Litestream copy in object storage shows no doc's content. Someone with the running server has the key, which is why end-to-end docs remain the later step.
- Rotating the master key is later; wrapping each doc's data key is what makes it cheap when it comes, since only the wrapped keys are rewritten.

## The hub's tables

STRICT, unix-millisecond timestamps, prefixed random ids, as AGENTS.md says for wdx. MVP only; teams come with sharing. The accounts' tables, `users` to `web_sessions` and `audit`, are built (step 4, migration `0002_accounts`, the wdx doc HUB_ACCOUNTS), with the columns it found missing; `nodes`, `node_logins` and `audit.node_id` too (step 5, migration `0003_nodes`, the wdx doc HUB_LOGIN); the rest come with their steps.

| Table | Holds |
|---|---|
| `users` | `id` (`usr-`), `username` (unique, `COLLATE NOCASE`, never given again), `password_hash` (argon2id; NULL once removed), `created_at`, `last_login_at`, `disabled_at`, `removed_at` |
| `invites` | `id` (`inv-`), `token_hash`, `created_at`, `expires_at`, `used_by`, `used_at` |
| `password_resets` | `id` (`rst-`), `user_id`, `token_hash`, `second_factor`, `created_at`, `expires_at`, `used_at` |
| `totp_factors` | `user_id` (primary key), `secret` (sealed under the master key, the user's id as additional data), `pending_secret` (a replacement being enrolled, so the factor in use works until it is confirmed), `confirmed_at` (NULL while being enrolled), `last_step` (the last 30-second step accepted, so a code is used once), `created_at` |
| `passkeys` | `id`, `user_id`, `name`, `credential_id`, `public_key`, `sign_count`, `created_at`, `last_used_at`; comes with passkeys, not created in step 4 |
| `recovery_codes` | `user_id`, `code_hash`, `used_at` |
| `web_sessions` | `id` (`ws-`), `user_id`, `token_hash`, `second_factor_at` (NULL until the second factor passed), `address`, `user_agent`, `created_at`, `last_seen_at`, `expires_at` |
| `node_logins` | `id`, `flow` (`browser`, `device`), `code_hash` (the device code's from the start, the authorization code's once a browser login is approved), `pkce_challenge`, `callback`, `user_code_hash`, `name`, `os`, `version` (of wdx), `public_key`, `status` (`pending`, `approved`, `denied`), `user_id`, `node_id`, `address` (the one that started it, shown on the approval page), `created_at`, `expires_at`, `polled_at` (the device flow's last poll, for `slow_down`) |
| `nodes` | `id` (`node-`), `user_id`, `name` (unique per user among the nodes not revoked, `NOCASE`), `public_key`, `os`, `version`, `remote_access`, `code_setup`, `created_at`, `last_seen_at`, `cert_expires_at` (when its current certificate ends, so renewal is decided without reading it), `revoked_at`; a removed node is kept, revoked, so its certificate is refused as revoked |
| `docs` | `id` (the node's doc id), `owner_id`, `repo_key`, `kind`, `name`, `folder`, `format`, `labels`, `status`, `revision` (the latest stored), `data_key` (random, sealed under `master_key` with the doc's id as additional data), `created_at`, `updated_at`; unique on owner, repository and name |
| `doc_revisions` | `doc_id`, `revision`, `content` (gzip, sealed under the doc's data key with its id and revision as additional data), `size` (of the text unpacked), `created_at` (the hub's clock); primary key on `doc_id` and `revision` |
| `audit` | `id`, `at`, `user_id` (no foreign key, as a cascade would delete rows of the chain), `node_id` (with step 5), `actor` (`user`, `admin` or `hub`), `action`, `address`, `details` (JSON), `hash`: each row's hash covers the one before and the row's own fields, so a row changed, removed or inserted breaks the chain |

Tokens, codes and device codes are stored hashed, never as given. Secrets the hub must read back (TOTP) are sealed under the master key.

## The node's side

### Files in `<home>/hub/`, mode 0700, each file 0600

| File | |
|---|---|
| `login.json` | hub address, `nodes_url` (the node endpoint's), user, node id and name; written last, so a crash part way leaves no login |
| `node.key`, `node.crt` | the node key and its current certificate |
| `hub-ca.crt` | the hub's CA, from login |
| `code.secret`, `code.step`, `recovery.hash` | the authenticator secret (TOTP, SHA-1, 6 digits, 30 seconds), the last step accepted, so a code is used once, and the SHA-256 of the recovery key, 20 base32 characters shown as five groups of four; `internal/authcode` reads and writes them |

Keys and secrets are files, not rows: the database is backed up at every `wdx update`, and its SQL is logged at debug level. `logout` deletes the directory and clears `hub_known_nodes` and `hub_sessions`, so that a key pinned under one account never carries over to the next.

### Tables in `wdx.db`, added by migrations that only add

| Table or column | |
|---|---|
| `projects.hub_sync` | `INTEGER NOT NULL DEFAULT 0` |
| `projects.hub_pushed_at` | when a change of the project last reached the hub, NULL if never |
| `hub_outbox` | `seq` (the order), `project_id` (`ON DELETE CASCADE`), `doc_id` (plain text, no foreign key: a `removed` row must outlive its doc), `change` (`revision`, `metadata`, `removed`), `revision` (of a `revision` row), `created_at`, `failed` (the hub's error code, or NULL); a row says what changed, and the content and metadata are read when it is sent |
| `hub_known_nodes` | `node_id`, `name` (`NOCASE`, its name when pinned), `public_key`, `first_seen_at`: keys pinned in sessions |
| `hub_sessions` | `id`, `direction` (`incoming`, `outgoing`), `node_id` (no foreign key: a closed session outlives the pin), `node_name` (the other node's name when it opened, for the list while the hub cannot be asked), `opened_at`, `last_used_at`, `idle_timeout_ms` (the host's timeout as it said at the open, on an outgoing row; an incoming one goes by the setting), `closed_at`, `closed_reason` (`closed`, `idle`, `restart`, `remote_access_off`, `logged_out`, `node_removed`) |
| `hub_audit` | the node's audit chain: `id`, `at`, `action`, `node_id` (the other node of a session, NULL for an act of this one alone), `details` (JSON), `hash`, the same chain as the hub's; `internal/nodeaudit` appends inside the act's transaction and verifies |

### Daemon endpoints in `api/openapi.yaml`

`GET /v1/hub` (status); `POST /v1/hub/login` (start, returning the login's id and the link and code), `GET /v1/hub/login?login=<id>` (its state, as server-sent events), `DELETE /v1/hub/login?login=<id>` (cancel) and `POST /v1/hub/login/code` (the browser flow's code, which the CLI's callback received); `POST /v1/hub/logout`; `POST /v1/hub/code` (setup: a pending secret goes back once to the CLI, which draws the QR; the response is logged with the secret left out, as `requestLogged` and the response switch in `internal/daemon/http.go` do, with `internal/api/secrets.go`, for prompts and file contents), `POST /v1/hub/code/verify` (one code back, which commits it and returns the recovery key once, also left out of the log) and `POST /v1/hub/code/recover`; `GET /v1/hub/audit/verification` (the node's audit chain walked); `GET /v1/hub/nodes`, `GET` and `DELETE /v1/hub/nodes/{node}`; `POST /v1/hub/nodes/{node}/connect` (the stream); `GET`, `POST` and `DELETE /v1/hub/sessions` (all) and `DELETE /v1/hub/sessions/{id}`; `GET /v1/hub/sessions/events` (the incoming sessions, as server-sent events); `GET` and `PUT /v1/projects/{project}/sync`; `POST /v1/terminals/{id}/attach` (the relay).

## The hub's API, `api/hub.yaml`

Each operation has one tag, `web`, `nodes` or `admin`, saying which endpoint serves it; another endpoint answers it 404. One path has one operation, so what each endpoint serves alike has a path per endpoint: the version is `GET /v1/version` on the web endpoint, `GET /v1/node/version` on the node endpoint and `GET /v1/admin/version` on the socket (step 3, the wdx doc HUB_SKELETON).

- Web endpoint: `POST /v1/node-logins` and `POST /v1/node-logins/token` (the device flow), `POST /v1/node-logins/exchange` (the authorization code for the node's credentials), and the sign-in pages' own routes.
- Node endpoint, mTLS, every request's certificate checked against the database (a revoked node, or a disabled or removed user, is refused with `node_revoked` or `account_disabled`): `GET /v1/control` (the control WebSocket, with `github.com/coder/websocket`, which also carries certificate re-issuance; the messages `hello`, `welcome`, `certificate`, `certificate_saved` and `bye`), `GET /v1/streams/{id}` (a stream WebSocket; the first to join waits for the other, and gets `node_offline` when it does not come within 10 seconds; an id is good for one join by each of its two nodes), `POST /v1/streams` (ask for a stream to a node of the same user with a control connection, `node_not_found` or `node_offline` otherwise; at most 32 open or waiting per user, `too_many_streams` past that), the control messages `stream_waiting` (to the host: the stream's id and the client node's id and name), `state` (from a node, at connect and on change: `remote_access` and `code_setup`, kept in `nodes` for display) and `node_removed` (hub to node: another node of the account was removed from it, by logout or by `wdx hub node remove`; sent to the user's other connected nodes, never to the removed one; an older node ignores it), `GET /v1/nodes`, `GET` and `DELETE /v1/nodes/{node}`, `POST /v1/docs/changes` (the outbox, in order, in batches; a body over 8 MiB is refused whole, and the request lines measure each change's `content`, never log it).
- Control messages are JSON objects with a `type`, described in the spec as schemas, so both sides are generated from one place.
- A hub states the range of `hub.yaml` versions it serves; the hello on the control socket checks it, and `hub_version_unsupported` says which side is behind.

## Deploying on Fly

- **The web endpoint** goes through Fly's proxy on the free shared IPv4: Fly terminates TLS with its certificate for `<app>.fly.dev` and passes plain HTTP to `listen.web`.
- **The node endpoint needs its TLS untouched,** since the hub must see the node's certificate. On Fly that is raw TCP, which a shared IPv4 does not carry, so the app has a dedicated IPv4 (about $2 a month; decided 2026-11-09 in the wdx doc HUB_FLY). IPv6 alone is free, but many offices, cafés and hotels have none, and those are where remote access is wanted. `deploy/hub/fly.toml` serves it as a `[[services]]` of `protocol = "tcp"` on 8443 with no handlers, so the bytes pass untouched.
- **One machine, always on,** with the volume `wdx_hub_data` at `/data`: nodes hold a control connection open, and a stopped hub drops them all.
- **Backups** are Fly's daily volume snapshots, plus one `make hub-fly-deploy` takes before every deploy. **Litestream** to Tigris is a later step: the image runs it as the supervisor, `litestream replicate -exec "wdx-hub serve"`, streaming `hub.db`, with Tigris's credentials as two more secrets.
- **Secrets** (`WDX_HUB_MASTER_KEY`, and `WDX_HUB_PUBLIC_URL`, which names the app and so stays out of the repository) are Fly secrets.
- **Shipping:** `scripts/dist.sh` builds `wdx-hub` into release archives of its own beside `wdx`'s (step 3). On Fly the image is built by Fly's builder from a clean checkout of a release tag, with the Dockerfile in `deploy/hub`, and deployed with `make hub-fly-deploy APP=<name>` (`scripts/hub-fly-deploy.sh`), by hand, never in CI. A published image (multi-arch on GHCR, signed with cosign, with an SBOM) is a later step, for when others run the image.
- Self-hosted, the same image runs with a volume and the same settings; `web.tls` decides who terminates TLS on the web endpoint, and the node endpoint is always the hub's.
- **Installed on a Linux server** (the wdx doc HUB_INSTALL): `curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install-hub.sh | sudo sh` reads the same release archives and `checksums.txt` as `install.sh`, makes the static user `wdx-hub` (home `/var/lib/wdx-hub`, the hub's home `~/.wdx-hub`), asks only `public_url` and the web endpoint's TLS (a proxy in front, or certificate files), makes the master key, and installs `/usr/local/bin/wdx-hub` and `/etc/systemd/system/wdx-hub.service`, the unit `wdx-hub service unit` prints (`User=wdx-hub`, hardened, `CAP_NET_BIND_SERVICE` only for a port below 1024); `/usr/local/lib/wdx-hub/` keeps the previous version and the update's staging. It ends with an invite for the first user. `wdx-hub update --apply` updates it after, with a database backup, putting everything back when the new release does not answer; run again, the script does the same. `sudo wdx-hub <command>` runs as the hub's user.

## The sign-in pages

Server-rendered HTML, a little plain JavaScript for passkeys, a strict CSP, no third-party scripts.

| Page | Shows | Does |
|---|---|---|
| Sign up | username, password twice | from an invite link, or open registration; then the second factor |
| Second factor | passkey, or TOTP: QR, secret as text, one code to confirm | then recovery codes, shown once, with "I have saved them" |
| Log in | username, password; then passkey, TOTP code or a recovery code | |
| Device code | a field for the code the terminal showed, and what approving a node means | the `--no-browser` flow only |
| Approve a node | "wdx on `<name>` wants to join your account", its OS, IP and when; the name, editable when the account has one already | Approve, Deny; logs in first if needed |
| Result | approved: back to your terminal (the browser flow sends the browser to the CLI's callback, which shows this); denied; expired | |
| Reset password | new password twice; with a second-factor reset, the second factor page after | from an admin's link |
| Security | change password; add or remove passkeys, replace TOTP; new recovery codes; signed-in browsers, signing them out | after log in |
| Errors | invite expired or used, registration closed, link expired | |

Later: plans and billing.

### The pages' protections

- Cross-site requests are refused by the standard library's `http.CrossOriginProtection`, from the `Sec-Fetch-Site` or `Origin` header every current browser sends, with `public_url`'s origin trusted; there is no per-form CSRF token (decided in the wdx doc HUB_ACCOUNTS, 2026-11-08). Cookies are `Secure`, `HttpOnly` and `SameSite=Lax`.
- Rate limits, per account and per address: passwords, TOTP and recovery codes, the device page's user code, and the device flow's polling (RFC 8628's `slow_down`).
- Behind a proxy, as on Fly, the client's address is taken from the forwarding header only from addresses in `trusted_proxies`, a setting; the approval page and the audit log show the real address.
- Passkeys are bound to the hub's domain. Moving the public hub from `fly.dev` to a domain of its own invalidates every passkey: the move's plan has users re-enrol them, with TOTP as the factor that keeps working.

## Logging and secrets

- `wdx` logs every flag in `command started` and the daemon every request body: so `--code` and the request field `code` are redacted, where `promptFlag` in `internal/cli/logs.go` keeps a prompt out of the CLI's log, and `requestLogged` in `internal/daemon/http.go` with `internal/api/secrets.go` keep prompts and file contents out of the daemon's and the CLI's, and the authenticator secret and recovery key never appear in a response that is logged.
- Hub settings have a `secret` attribute: `master_key`, the object storage credentials and the like are shown as `[secret]` by `wdx-hub setting list` and in `command received`, which logs settings.
- The hub never logs a doc's content, a token, a code or a password hash; the relay logs that a stream opened and closed, with its bytes counted, never its bytes.
- Never logged on the node either: the code, the recovery key, the TOTP secret, CPace's messages and key, the sessions' MACs, a stream's bytes. The daemon's request lines measure `code` and `recovery_key`, and `--code` is measured in `command started`, as a prompt is.
- The node keeps an audit chain of its own, `hub_audit`, of what matters about access to it (the code set up, replaced or recovered, wrong codes and recovery keys, remote access on and off, sessions opened and closed with the reason, a key mismatch), which `wdx hub audit verify` walks.
- The audit chain has a verifier, `wdx-hub audit verify`, and the hub writes the chain's latest hash to its log once an hour, so that a tampered chain can be caught against a copy the server does not hold.

## What `remote_not_for_agents` is

A guard against accident, not a security boundary: `--node` is refused where `WDX_AGENT_ID` is set, so that an agent on the client never works on another node through a session its human opened by mistake. Any process running as the user on the client, agents included, can call the daemon's socket directly while a session is open; that is what running as the user means, and nothing in wdx pretends otherwise.

## Testing

- A test runs the hub in its process on `127.0.0.1` on a port the system picks, with a CA made in the test, and two daemons in the process as two nodes, each with a home of its own.
- Login is tested without a browser: the test calls the login page's routes as a browser would, with a cookie jar, through to the callback or the device code.
- The whole path is covered: a session opened with a code the test computes from the host's secret, a wrong code refused and slowed, a request through the session, the route list, an attach through the relay, an idle expiry made short through `cli.Env.Timeouts`, a doc pushed and acknowledged, a push retried after a lost acknowledgement, `doc_name_taken`, and `wdx hub node remove` cutting a connection.
- The hub's own tests cover the pages, the rate limits, the scoping of every query to the account (one account's test reading another's data must fail), and `audit verify` on a chain with a row changed.

## Decided while drafting

- **The node endpoint on Fly is decided after the first release** (2026-11-03). Until then the hub runs locally and nodes connect with `--url`; the public hub, and with it a dedicated IPv4 for real mTLS, come later. (Decided 2026-11-09: a dedicated IPv4, the wdx doc HUB_FLY.)
- **The Security page is in the MVP** (2026-11-03): changing the password, passkeys and TOTP, new recovery codes, signed-in browsers. Without it, a lost passkey or a leaked recovery code could be fixed only by an admin's reset.
- **From the review of 2026-11-03**, agreed by the human: login with a browser is the authorization code flow with a loopback callback, and the device flow, with its code typed, is for `--no-browser` only, since a device-flow link can be forwarded to have an attacker's node approved; node certificates last a year, with revocation the hub's check; codes and keys are redacted from logs; the outbox keeps `removed` rows; CPace carries the code's time step; a session ends when the host's daemon restarts; idle counts terminal bytes; `doc_name_taken` for two of one's own nodes; the API gets a version of its own; the pages' protections, the logging rules and the tests above.

## Decided in the review against the code, 2026-11-07

The design was checked against main at 3369a87, 151 commits after it was written. Nothing in it was contradicted; what landed since (plans, folders, tide's state, the one-change lock, the release scripts) is folded into the sections above, and these were decided by the human:

- The hub's `docs` table and each pushed change carry `kind` and `folder`; the MVP pushes only `kind = doc`.
- A remote tide keeps its view in memory and never touches the host's tide state; one tide per machine, through the client's `tide.lock`.
- The doc store writes `hub_outbox` inside its own transaction; `hublink` reads and deletes.
- Every stream the daemon serves is an Upgrade, exempt from the changes lock.
- The MVP ships `wdx-hub` in the archives only; image, cosign and SBOM come with Fly. (On Fly, Fly's builder builds the image from a tag; the published image stays a later step, the wdx doc HUB_FLY.) `install-hub.sh` and `wdx-hub update` install and update it from those archives (the wdx doc HUB_INSTALL, built 2026-11-09).

Shapes found, no decision needed: the settings table already has a per-row `fromEnv`, so `WDX_HUB_<KEY>` for every hub setting is a table-wide switch, but `set` and `get` are closures over wdx's `Config`, so `internal/settings` is the engine made generic over the config type, and a `secret` attribute is new; `db.Open` embeds its own migrations, so `internal/sqlite` takes a migrations filesystem, with `Backup` and `Restore` moving as they are; `remote_not_for_agents` holds for every ACA, since the holder sets `WDX_AGENT_ID` for each agent terminal; projects already carry the repository key (`origin_key`, `git.RepoKey` of the remote, NULL for `no_remote`); `wdx deps` gets the browser opener; `daemon.Settings` gets the two `hub.*` settings for reload. docs/IDE.md's content hash per revision was never built; pushes are idempotent by id and revision and need none.

## Settled in step 5, 2026-11-08

The wdx doc HUB_LOGIN, built: the login's state is `GET /v1/hub/login` with server-sent events, not an Upgrade; the hub has a setting `nodes_url` for where nodes dial the node endpoint, sent at login and kept in `login.json`; a second login replaces a pending one, whose CLI ends with `login_cancelled`; `internal/hub/hubtest` is the one hub package wdx's tests import; the package is `internal/hub/node`. The daemon is the only user of `internal/hublink`, which uses the hub's API alone.

## Settled in step 6, 2026-11-08

The wdx doc HUB_DOCS_PUSH, built: batches over `POST /v1/docs/changes` in place of a stream; logout clears the outbox and a login enqueues every synced project; a moved remote moves docs as they change; a removed project leaves its docs on the hub; the new columns `projects.hub_pushed_at` and `hub_outbox.project_id`; `revision_gap` refused for good as the doc's own; the package is `internal/hub/docsync`.

## Settled in step 7, 2026-11-08

The wdx doc HUB_TUNNEL, built: the slowing of wrong codes is per client node and in total; the node keeps an audit chain, `hub_audit`, with `wdx hub audit verify`; `code.step` keeps the last step accepted; `hub_sessions` gains `node_name`, `idle_timeout_ms` and `closed_reason`; `too_many_streams` (32 per user) and `node_key_mismatch` are new codes; the terminal's QR code is drawn from `boombuler/barcode`'s matrix, with no other QR library; the packages are `internal/hub/relay`, `internal/remote`, `internal/authcode` and `internal/nodeaudit`, and only the daemon uses `remote`, which uses `hublink` for the daemon. A client learns that a session ended at its next stream, refused with `session_closed` and the host's reason, and closes its row for that reason. `remote` hands step 8 a `net.Conn` per stream of a session.

## Settled in step 8, 2026-11-08

The wdx doc HUB_NODE_COMMANDS, built: `--node` is on `agent`, `terminal`, `project`, `doc`, `plan`, `worktree`, `message` and `version`; every operation is classified in `remoteOperations`, `getDaemon` and `reloadDaemon` among those refused; `info.version` is an integer with `OldestCompatible`, checked between the CLI and the host through `Wdx-Api-Version`; `route_not_allowed` (403), `session_required` (409) and `remote_not_for_agents` (403) are new codes. The client daemon's `connect` finds the node among its open outgoing sessions only, never asking the hub, and answers `session_required` with none, or when the host has closed it; the CLI then asks the code, on the command's first stream only, and a later `session_required` is `session_closed`. The CLI's HTTP client keeps one connection to the host (`MaxConnsPerHost` 1), idle at most 5 seconds, under the host's 10, after which the host closes a stream with no request. The host serves the streams with a second `http.Server` reading one end of a `net.Pipe`, since the WebSocket's `net.Conn` closes when a read deadline passes, which `net/http` sets. The `--node` codes are declared once, on the flag, shown in help's "With --node" section and its JSON's `flag_errors`. Attach refuses `--node` until step 9.

## Settled in step 9, 2026-11-09

The wdx doc HUB_REMOTE_TIDE, built: `POST /v1/terminals/{terminal}/attach` relays the holder's protocol, the only operation moved to allowed; the holder protocol's version 5 carries the remote `hello` (`remote`, `node`), and `holder_version_unsupported` (409) is a new code; `terminal attach --node`, `agent attach --node` and `wdx tide --node` attach through it, and a session ending mid-attach is `connection_lost`. A remote tide keeps its view in memory, has no Settings tab and a typed path for a new project, follows the reconnect rule above, and takes the client's `tide.lock`; quitting it waits for the requests of both its clients, the host's and the local daemon's, before the command returns. A later `session_required` becomes `session_closed` with the host's `reason` in its details, as the client daemon had it, which tide's message shows. The host's indicator follows `GET /v1/hub/sessions/events`, the third stream in `streamOperations`, which the daemon feeds from `remote.Options.Changed`, called when a session opens or closes.

## Running the hub locally, until the public one

- `wdx-hub serve` on the machine, `web.tls: off`, `public_url: http://localhost:8080`; nodes log in with `--url http://localhost:8080`. Plain HTTP is allowed to localhost only. A browser treats `http://localhost` as secure, so passkeys work there.
- Two nodes on one machine, for trying remote access: two wdx homes, each its own daemon (`WDX_HOME`).
- A second machine on the network needs the web endpoint over TLS with a certificate it trusts, since there is no flag to skip checking one: `web.tls: files` with a certificate from a local CA installed on both machines (such as mkcert's), or a name and certificate from a private network such as Tailscale's.
- `--hub-central` ships only once the public hub has an address: a flag that can only fail is left out.
