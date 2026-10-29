# Terminals

Status: implemented. Attaching to a terminal and its history are in docs/ATTACH.md.

A terminal is a shell running in a PTY that belongs to a project. It keeps running when the daemon restarts, is upgraded or crashes, and when the window that showed it is closed. Agents will later run in the same kind of PTY; terminals come first because they need no AI CLI.

## Architecture

```
 wdx terminal create ──HTTP/unix──► daemon ──starts──► holder (wdx holder, own session)
                                      │                  ├─ PTY master (internal/pty)
                                      │◄─control conn────┤─ $SHELL -l, the PTY slave as its tty
                                      │                  ├─ spool, vt screen, history (docs/ATTACH.md)
                                  SQLite: terminals      └─ socket <home>/run/<terminal id>.sock ◄── attached clients
```

- **The holder** is one process per terminal: the `wdx` binary run as the hidden command `wdx holder`. It opens the PTY, starts the shell as its child, always reads the PTY master (a shell whose output nobody drains blocks once the kernel buffer fills), reaps the shell and keeps its exit code, and serves a Unix socket.
- **The daemon** never holds a PTY. It keeps the `terminals` table, starts holders, and keeps one control connection open to each. A daemon that stops leaves the holders and their shells running; the next one adopts them.
- **The DB records intent and history**: which terminals exist, their name and dir, what wdx was asked to do. **The holders are the truth about processes**: whether a shell is alive and how it ended. Reconciliation syncs the DB to the holders, except where the DB holds an intent (`stopped`), which is carried out on the holder.
- Nothing polls. A holder learns of its shell's exit from the kernel (it is the parent) and pushes `exited` to the daemon at once. The daemon learns of a holder's death from EOF on the control connection.

### Surviving the service manager

- systemd kills every process in a service's cgroup when the service stops, `setsid` or not. The unit gets `KillMode=process`, so stopping or restarting the daemon stops only the daemon.
- launchd: the holder's `setsid` takes it out of the job's process group; the plist also gets `AbandonProcessGroup` to be safe.
- A holder keeps running the binary it was started from, also after an upgrade. The holder protocol carries a version; a daemon speaks its own and the previous one. The daemon speaks version 2, which brought attach (docs/ATTACH.md), and drives holders of version 1, started before it; the release before it already drove version 2, so rolling back keeps the terminals. Attaching needs version 2: a terminal whose holder is older answers `attach_unsupported` until its shell is started again.

## The table

`internal/db/migrations/0003_terminals.sql`:

```sql
-- A terminal is a shell in a PTY, owned by a holder process. The row outlives
-- the shell: it stays, whatever its state, until the terminal is removed.
CREATE TABLE terminals (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	name       TEXT NOT NULL COLLATE NOCASE,
	dir        TEXT NOT NULL,
	shell      TEXT NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('starting', 'running', 'exited', 'stopped', 'failed', 'lost')),
	pid        INTEGER,
	holder_pid INTEGER,
	exit_code  INTEGER,
	error      TEXT,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	started_at INTEGER,
	ended_at   INTEGER,
	UNIQUE (project_id, name)
) STRICT;

CREATE INDEX terminals_project_id ON terminals (project_id);
```

| Column | |
|---|---|
| `id` | `term-` and 8 hex digits, like `proj-` |
| `name` | unique per project; a generated adjective-noun codename (`swift-falcon`, v1's word lists) unless `--name` is given, retried when taken; same rules as project names (no `:`) |
| `dir` | where the shell starts, now and on every relaunch; a `cd` inside the shell does not change it |
| `shell` | absolute path of the program exec'd |
| `pid`, `holder_pid` | while a holder lives; `holder_pid` is internal and never in the JSON |
| `exit_code` | only `exited`; a shell killed by a signal gets 128 + the signal, as shells report it |
| `error` | only `failed`: why it did not start |
| `started_at` | set on every (re)launch; `ended_at` when it last stopped running |

A name stays taken while its row exists, whatever the state.

## States

| State | Meaning |
|---|---|
| `starting` | the row is inserted, the holder is being started |
| `running` | the holder is ready and the shell alive |
| `exited` | the shell ended by itself; `exit_code` says how |
| `stopped` | wdx stopped it on purpose (project close); relaunched when the project opens again |
| `failed` | the holder or shell did not start; `error` says why |
| `lost` | the shell should be running, but its holder is gone and nobody stopped it: a reboot, `kill -9`, OOM; how it ended is unknown |

```
                create
                  │
                  ▼
   ┌────────► starting ──────────────► failed ──┐
   │              │                              │
   │              ▼                              │
   │           running ──► exited ───────────────┤
   │            │    │                           │
   │            │    └───► lost ─────────────────┤
   │            ▼                                │
   ├──────── stopped                             │
   │  (project open)                             │
   └─────────────────────────────────────────────┘
                         start

   terminal remove or project remove: any state ──► row deleted
```

| From → to | Trigger | Noticed by |
|---|---|---|
| none → `starting` | `terminal create` inserts the row before anything is started | daemon |
| `starting` → `running` | the holder writes "ready" once the PTY is open and the shell started | daemon, on the ready pipe |
| `starting` → `failed` | the shell cannot be exec'd, the PTY cannot be opened, or the holder is not ready within 5 s | daemon |
| `starting` → `failed` / `running` | the daemon died during the start: at startup a holder that answers makes it `running`, none makes it `failed` ("interrupted while starting") | daemon, at startup |
| `running` → `exited` | the shell ends: the holder reaps it and sends `exited`, or keeps the code until the daemon reconnects | daemon, live or at startup |
| `running` → `lost` | the control connection drops and a redial gets no answer, or at startup the socket does not answer | daemon, live or at startup |
| `running` → `stopped` | `project close` with `--stop-running`; the state is written **before** the shell is signalled | user |
| `stopped` → `starting` | opening the project relaunches its `stopped` terminals | user |
| `exited` / `failed` / `lost` → `starting` | `terminal start`; never automatic, not even on project open | user |
| any → deleted | `terminal remove`, `project remove` with `--stop-running`; a live shell is stopped first | user |

- Stopping writes `stopped` first, so a daemon that dies before the shell is gone finishes the stop at startup instead of recording `exited`.
- A relaunch keeps id, name and dir, starts a fresh shell, and clears `pid`, `exit_code` and `error`. Its screen and running programs are gone; its history (docs/ATTACH.md) goes on after a marker line, and the shell's own history file is kept.
- `start` or `create` in a closed project fails with `project_closed`; `start` on a `starting` or `running` terminal fails with `terminal_running`.
- `stopped` is reached only by closing the project and left only by opening it again (or by removal): `start` on a closed project fails with `project_closed`.
- After a reboot every terminal of an open project is `lost` and stays so until started: nothing is relaunched automatically. A closed project's terminals stay `stopped`, and opening it relaunches them.

## Reconciliation at daemon startup

Before serving, the daemon dials the socket of every terminal in `starting`, `running` or `stopped`. A holder that answers `hello` with the same terminal id is adopted.

| DB | Holder | Daemon |
|---|---|---|
| `running` | shell alive | keeps `running`, opens its control connection |
| `running` | shell ended | `exited` with the code, then releases the holder |
| `running` | no answer | `lost` |
| `starting` | answers | `running` |
| `starting` | no answer | `failed`, "interrupted while starting" |
| `stopped` | alive | finishes the stop |
| `stopped` | no answer | stays `stopped` |

## The holder

Started by the daemon as

```
wdx holder --id <terminal id> --project <name> --dir <dir> --shell <path> --history-lines <lines>
```

in its own session (`setsid`), stdin/stdout/stderr on `/dev/null`, with a pipe as fd 3. It:

1. creates `<home>/run/` (0700) and listens on `<home>/run/<id>.sock`, accepting only peers of its own uid;
2. opens the PTY at 80×24, starts the shell as a login shell (`argv[0]` `-zsh`) in `dir` with the clean environment below, and writes `ready` with the shell's pid to fd 3, or the error, then closes it;
3. reads the PTY master into its spool and to attached clients until the shell ends, while a vt follows the spool to keep the screen and the history (docs/ATTACH.md);
4. reaps the shell, sends `exited` to the daemon if connected, and waits until the daemon `release`s it; then removes its socket and exits.

Stopping: on `stop` the holder sends SIGHUP to the shell and to the PTY's foreground process group, waits up to 5 s, then SIGKILLs both groups, reaps the shell and answers `exited`. Programs started with `nohup` or `setsid` in the shell are not touched.

### The shell's environment

Built, never inherited whole, and never taken from the CLI that asked: the terminal outlives that command, and is relaunched by others.

| Variables | From |
|---|---|
| `HOME`, `USER`, `LOGNAME`, `SHELL` | the user database |
| `TERM=xterm-256color`, `WDX_TERMINAL_ID`, `WDX_PROJECT`, `WDX_HOME` | wdx; `WDX_HOME` so that `wdx` run in the shell reaches the daemon that owns it |
| `PATH`, `LANG`, `LC_*`, `SSH_AUTH_SOCK`, `DISPLAY`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS` | the daemon's environment, those set |

- The last row is a fixed allowlist: what a login shell's profile assumes is already there. Without it the shell runs in the C locale, `git push` over ssh cannot reach the agent, and GUI and clipboard tools cannot reach the desktop. The login shell then reads the user's profile and rc, which build the rest.
- `main` reads the allowlist once into `cli.Env.ShellEnv`, keeping it the only place that reads the environment. The daemon starts each holder with exactly those variables, plus `WDX_HOME`; the holder's `main` reads them the same way and hands them to the shell. Never in arguments, where `ps` shows them, and never logged.
- The values are the daemon's from when it started: after logging in again with a new ssh agent, `wdx daemon restart` picks up the new socket. Shells already running keep the old one, as in tmux.

Logs: the holder logs to the same daily files, with `terminal_id` on every line: started, shell started, exited, stopped, released, and what docs/ATTACH.md lists for attach. Never PTY bytes.

### Protocol

Frames on the socket: one byte of type, a 4-byte big-endian length, the payload; control payloads are JSON, data payloads raw bytes.

| Frame | Direction | Payload |
|---|---|---|
| `hello` | both, first | `{"version": 2, "role": "daemon"}` from the daemon; `{"version": 2, "terminal_id": ..., "pid": ..., "holder_pid": ..., "exited": null or {"code": ...}, "history_lines": ...}` from the holder |
| `exited` | holder → daemon | `{"code": ..., "at": ...}` |
| `stop` | daemon → holder | `{"grace_ms": 5000}`; answered by `exited` |
| `release` | daemon → holder | none; the holder exits |

Version 2 adds clients beside the daemon, with `attach`, `history`, `snapshot`, `output`, `input` and `resize`: docs/ATTACH.md.

## Settings

| Key | Default | |
|---|---|---|
| `terminal.shell` | empty: the user's login shell from the user database, `/bin/sh` when it names none | absolute path of the shell terminals run |

The login shell is looked up when a terminal starts, not when wdx loads its settings: every command would pay for the lookup, and the user database can change while the daemon runs.

## API

| Request | | Errors |
|---|---|---|
| `POST /v1/terminals` `{"project": ref or null, "dir": "/abs", "name": null}` | 201 `{"result": "created", "terminal": {...}}` | `invalid_name`, `name_taken`, `not_found`, `project_closed`, `start_failed` |
| `GET /v1/terminals?project=<ref>` or `?dir=/abs`, neither for all | 200 `{"terminals": [...]}`, by project then name | `not_found` |
| `GET /v1/terminals/{terminal}` | 200 `{"terminal": {...}}` | `not_found` |
| `POST /v1/terminals/{terminal}/start` | 200 `{"result": "started", "terminal": {...}}` | `not_found`, `project_closed`, `terminal_running`, `start_failed` |
| `DELETE /v1/terminals/{terminal}?stop_running=true` | 200 `{"result": "removed", "terminal": {...}}` | `not_found`, `terminal_running` |
| `GET /v1/terminals/{terminal}/history?tail=` or `?from=&count=` | 200 `{"history": {...}}`: docs/ATTACH.md | `not_found` |

- `{terminal}` is `id:` and its id, or a name together with `?project=<ref>` or `?dir=/abs`, since names are unique only within a project.
- `project` is resolved like everywhere (name, or `id:`); `dir` finds the project whose registered worktree contains it, the longest path winning. Without `project`, `create` starts the shell in `dir`. With it, in `dir` when `dir` is inside the project, else in the project's clone path.
- `start_failed` carries the row, now `failed`, under `details.terminal`.

A terminal in JSON:

```json
{"id": "term-1a2b3c4d", "name": "swift-falcon", "project_id": "proj-…", "project_name": "warden",
 "dir": "/home/me/dev/warden", "shell": "/usr/bin/zsh", "state": "running", "pid": 4242,
 "exit_code": null, "error": null, "created_at": "…", "started_at": "…", "ended_at": null}
```

Projects:

- `PATCH /v1/projects/{project}` takes `stop_running` with `state: closed`; `DELETE` takes `?stop_running=true`. With a terminal `starting` or `running` and without it: 409 `project_in_use`, the live terminals under `details.terminals` (agents later under `details.agents`).
- Close marks those terminals `stopped` and stops them, then closes the project; the result lists them under `stopped`. Remove stops them, lists them under `stopped` too, then the cascade deletes every row. `on_missing_checkout: remove` on `POST /v1/projects` removes the same way, without `stop_running`: live terminals refuse it with `project_in_use`.
- Opening a project (`PATCH` to open, `POST /v1/projects` reopening a known one, or recloning it) relaunches its `stopped` terminals and lists them under `started`, each with the state it reached: one whose dir is gone ends `failed`.
- `started` and `stopped` are left out when empty, as `dropped` is.
- `GET /v1/projects/{project}` sets `terminals` on the project: every terminal in it, in any state, by name; `[]` when it has none. Other results carry no `terminals`, as they carry no `orphaned`.

New codes in `internal/api/codes.go`:

| Code | Status | Exit | |
|---|---|---|---|
| `project_closed` | 409 | 1 | the project is closed |
| `project_in_use` | 409 | 1 | terminals are running in the project |
| `terminal_running` | 409 | 1 | the terminal is already starting or running |
| `start_failed` | 500 | 3 | the holder or the shell did not start |
| `terminal_not_running`, `attach_unsupported`, `connection_lost` | | | attach's, reported by the CLI: docs/ATTACH.md |

`not_found`, `name_taken` and `invalid_name` now cover terminals too, so their descriptions no longer say "project": "nothing has this name or id", "something else has this name", "the name is not allowed".

## CLI

```
wdx terminal create [--project-id <id> | --project-name <name>] [--name <name>]
wdx terminal get    (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx terminal list   [--project-id <id> | --project-name <name> | --all]
wdx terminal start  (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx terminal remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--stop-running | --cancel]
wdx terminal attach / history: docs/ATTACH.md
wdx project close  ... [--stop-running | --cancel]
wdx project remove ... [--stop-running | --cancel]
```

- Without a project flag, the project is the one the working directory is in; outside every project that is `not_found`, with a hint naming the project flags (and `--all` for `list`). The two project flags exclude each other.
- `--id` needs no project; a project flag given with it must be the terminal's project, else `not_found`.
- Removing a live terminal, and closing or removing a project with live terminals, warns with what will be stopped and asks; `--stop-running` and `--cancel` answer in advance, and with `--json` one of them is required. The daemon refuses with `terminal_running` / `project_in_use` without the answer, so nothing changes between the warning and the act.
- Completions: terminal names and ids from the daemon, for the project already on the command line or the working directory's.
- `main` reads the working directory into `Env.WorkDir`, which names the project when no flag does; tests set it instead of changing directory.
- Requiring `--stop-running` or `--cancel` with `--json` on `project close` and `project remove` breaks scripts that ran them with `--json` alone.

Text output:

```
$ wdx terminal create
created terminal swift-falcon in warden
  dir    /home/me/dev/warden
  shell  /usr/bin/zsh
  id     term-1a2b3c4d

$ wdx terminal list
NAME          STATE         DIR                    ID
calm-otter    exited (0)    /home/me/dev/warden    term-5e6f7a8b
swift-falcon  running       /home/me/dev/warden    term-1a2b3c4d
```

`--all` adds a PROJECT column.

## Tests

- The holder's tests run it against real PTYs with `/bin/sh`: ready, exit codes, signals, stop with a grace, a shell that will not exec, refusing another uid's peer where it can be tested.
- Daemon and cli tests start holders from the `wdx` binary built in `TestMain` (the daemon takes the binary to run as an option, since in tests it runs inside the test binary), with `terminal.shell` `/bin/sh`.
- One test per outcome of each command and each new error code; reconciliation tests stop the in-process daemon, kill or keep holders, start a new daemon and assert every row of the reconciliation table.
- Every test removes its terminals, so no holder outlives it.

## Implementation plan

Each step builds and passes `make test` on its own. All of them are done.

1. **Table and settings**: migration `0003_terminals.sql`, which only adds a table and so does not break older binaries; the `terminal.shell` setting; the terminal allowlist read by `main` into `cli.Env.ShellEnv`; AGENTS.md's Config section names it as the third exception.
2. **`internal/holder`**: the process (`Run`), the protocol, the client (`Dial`, `Stop`, `Release`, watching for `exited`) and `Start`, which launches `wdx holder` with the ready pipe. Tests with real PTYs.
3. **`internal/terminal`**: the Store (rows, transitions, names with codenames, lookup by id or by name in a project) and the supervisor that starts, watches, stops and reconciles holders. It does not import `project`: the daemon passes the project's id and the dir. `holder/holdertest` makes a test binary its own holder. Tests with a temp DB and real holders.
4. **API and daemon**, one step since the daemon must implement every operation of the spec to build: the terminal paths and schemas, `stop_running`, `started` / `stopped` on project results, the new codes, `make generate`; `GetByPath` (the project whose worktree holds a dir, the deepest winning) in the project Store; the terminal handlers; reconciliation before serving; project close, open and remove going through the supervisor; shutdown leaving holders alone. The daemon takes the binary holders run, the shells' variables and `terminal.shell` as options.
5. **CLI**: `wdx terminal create / list / start / remove`, the hidden `wdx holder`, `--stop-running` / `--cancel` and their prompt on project close and remove, completions, help, examples; integration tests per outcome.
6. **Install and docs**: `KillMode=process` and `AbandonProcessGroup` in `scripts/install.sh`; DAEMON.md; AGENTS.md's layout and dependency lines (`cli -> holder` for the hidden command, `daemon -> terminal -> holder`); V1_FEATURES.md checkboxes.
