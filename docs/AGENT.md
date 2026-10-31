# Agents

Status: implemented. Checked against the real Claude Code 2.1.314 (see Tests).

An agent is an AI coding agent (an ACA: Claude Code for now, Codex, Cursor, Antigravity later) running in a terminal of a project. It has everything a terminal has, its holder included: it outlives the daemon and the window that showed it, and you attach to it and detach from it the same way. On top it knows which ACA it runs and which session of that ACA is its own.

Child agents and tombstones are in docs/CHILD_AGENTS.md. Not in this step: what the ACA is doing (idle, busy, needs input and the like), choosing a model, an initial prompt, the agent pane in tide. `state` below is only whether the process is alive. Two agents in one checkout share its files.

## Architecture

```
 wdx agent create ──HTTP/unix──► daemon ─► agent ─► terminal supervisor ──starts──► holder (wdx holder)
                                            │                                         └─ $SHELL -l -i -c "exec 'claude' ..."
                                            └─► aca: the command to run
```

- An agent is a row in `agents` pointing at one row in `terminals` of kind `agent`, which keeps the name, dir, pid, exit code and process state. The holder, its protocol, attach, history, reconciliation and stopping are the terminal's, unchanged.
- That is storage: an agent is always read joined with its terminal row, and the terminal API and `wdx terminal` deal only with kind `shell`. The one trace outside is `terminal_id` in an agent's JSON, which attach needs to reach the holder and which no terminal path accepts.
- The ACA is the holder's child, not something typed into a shell: typing races with rc files and leaves a live shell behind when the ACA ends, hiding its exit. It starts through the user's login shell, interactive, so that the `PATH` and keys their rc files set apply; `exec` then puts the ACA in the shell's place. The command is one string of single-quoted words rather than `"$@"`, which fish does not parse. When the ACA ends the terminal is `exited` with its code, and no shell is left.
- The supervisor asks for the command at each launch, through a function in its options that the daemon wires to the agent package; `terminal` knows nothing of agents. Terminals of kind `shell` run the shell as today.
- Dependencies: `daemon -> agent -> aca, terminal`.

## The AiCodingAgent

```go
package aca // internal/aca

// AiCodingAgent is what differs between the AI CLIs wdx runs.
type AiCodingAgent interface {
	ID() string // "claude_code"
	Command(Launch) ([]string, error)
}

type Launch struct {
	Dir       string
	SessionID string
	UserHome  string // holds what an AI CLI keeps
}
```

`aca.ClaudeCode`:

- `claude --resume <id>` when `~/.claude/projects/*/<id>.jsonl` exists, else `claude --session-id <id>`.
- The check is needed: Claude Code saves no transcript until something is typed, and `--resume` of such a session fails with `No conversation found with session ID` (checked on 2.1.314). So an agent created and left untouched, then relaunched, starts that session afresh.
- wdx generates the session id, a UUID, when it creates the agent.

## The table

`internal/db/migrations/0004_agents.sql` only adds, so it needs no `breaks-older`. An older binary sees an agent's terminal as a plain terminal, and relaunching it there starts a shell.

```sql
ALTER TABLE terminals ADD COLUMN kind TEXT NOT NULL DEFAULT 'shell' CHECK (kind IN ('shell', 'agent'));

-- An agent is an AI coding agent running in a terminal row of kind 'agent',
-- which holds its name, dir and process. Always read joined:
--   SELECT ... FROM agents a JOIN terminals t ON t.id = a.terminal_id WHERE a.id = ?
-- Removing the terminal row removes the agent.
CREATE TABLE agents (
	id          TEXT PRIMARY KEY,
	terminal_id TEXT NOT NULL UNIQUE REFERENCES terminals (id) ON DELETE CASCADE,
	aca         TEXT NOT NULL,
	session_id  TEXT NOT NULL
) STRICT;
```

- `id` is `agent-` and 8 hex digits. The terminal row has its own `term-` id, which names the holder and its socket as for any terminal.
- `name` is the terminal row's: a generated codename unless `--name` is given. Names are unique per project across agents and terminals: both are on screen together, so a duplicate would be ambiguous anyway.
- A project's removal cascades through its terminal rows to its agents.

## Lifecycle

`state` is the terminal's, with the same meanings and transitions (docs/TERMINALS.md): `starting`, `running`, `exited`, `stopped`, `failed`, `lost`.

| Action | Effect |
|---|---|
| `agent create` | inserts both rows, then starts the holder; `start_failed` leaves the row `failed` |
| `agent start` | from `exited`, `failed` or `lost`: starts it again, in the same session, so the conversation comes back |
| `agent remove` | stops a live process (`--stop-running`), deletes both rows; the ACA's own transcript stays on disk |
| `project close` | stops its agents along with its terminals, under the same `--stop-running` |
| `project open` | relaunches its `stopped` agents, in the same session |
| `project remove` | stops them and deletes the rows |

Never relaunched automatically otherwise, as with terminals: after a reboot an agent is `lost` until `agent start`.

`claude` not being installed shows as `exited` with code 127: the login shell starts, then cannot exec it.

## Terminals

Two sets of queries, one per flow:

- **The terminal flow**: `terminal.Store`'s exported queries filter on `kind = 'shell'`: lists, lookups by name and by id, a project's `terminals`, `details.terminals` and `stopped` / `started`. An agent's `terminal_id` given to them is `not_found`, by the same path as any unknown id.
- **Rejected at every entry, not only by the lookups**: `Supervisor.Start`, `Remove` and `History` take a bare id today, so a caller that already holds one would skip a lookup's filter. Each of these checks the row's kind first and returns the same `not_found` when it is not `shell`, before it changes anything. The agent flow reaches the same process work through its own methods, which likewise reject kind `shell`. `not_found` rather than a code of its own, so that the answer does not reveal that an agent's terminal exists. `wdx terminal attach` asks the API for the terminal before it dials a holder, so it fails the same way.
- **The agent flow**: `agent.Store`'s queries join `agents` with `terminals`, by agent id, by name in a project, or by project. One query, so a row is never read half-changed and a list needs no fetch per agent. Kind `shell` terminals never appear, having no `agents` row.

Below both, the supervisor's process work takes a terminal row of either kind: reconciliation at startup, the socket sweep, watching holders, recording exits, stopping and relaunching a project's. If it filtered, agents would be missed at startup and left stale, and project close would not stop them. Its queries are unexported, so neither flow can reach them.

## API

| Request | | Errors |
|---|---|---|
| `POST /v1/agents` `{"project": ref or null, "dir": "/abs", "name": null}` | 201 `{"result": "created", "agent": {...}}` | `invalid_name`, `name_taken`, `not_found`, `project_closed`, `start_failed` |
| `GET /v1/agents?project=<ref>` or `?dir=/abs`, neither for all | 200 `{"agents": [...]}`, by project then name | `not_found` |
| `GET /v1/agents/{agent}` | 200 `{"agent": {...}}` | `not_found` |
| `POST /v1/agents/{agent}/start` | 200 `{"result": "started", "agent": {...}}` | `not_found`, `project_closed`, `agent_running`, `start_failed` |
| `DELETE /v1/agents/{agent}?stop_running=true` | 200 `{"result": "removed", "agent": {...}}` | `not_found`, `agent_running` |
| `GET /v1/agents/{agent}/history?tail=` or `?from=&count=` | 200 `{"history": {...}}`, as for terminals; what attach scrolls through | `not_found` |

`{agent}` is `id:` and its id, or a name together with `?project=<ref>` or `?dir=/abs`, as for terminals.

```json
{"id": "agent-1a2b3c4d", "name": "swift-falcon", "project_id": "proj-…", "project_name": "wdx",
 "parent_agent_id": null, "children": [], "terminal_id": "term-5e6f7a8b", "aca": "claude_code", "session_id": "…", "dir": "/home/me/dev/wdx", "worktree": "main",
 "state": "running", "pid": 4242, "exit_code": null, "error": null,
 "created_at": "…", "updated_at": "…", "started_at": "…", "ended_at": null, "removed_at": null}
```

`terminal_id` is there because attaching dials that terminal's holder. Text output never shows it.

Projects:

- `GET /v1/projects/{project}` and its event stream set `agents` beside `terminals`.
- `project_in_use` counts live agents too, and lists them under `details.agents`.
- Results list them under `stopped_agents` and `started_agents`, left out when empty as `stopped` and `started` are.

New codes in `internal/api/codes.go`:

| Code | Status | Exit | |
|---|---|---|---|
| `agent_running` | 409 | 1 | the agent is already starting or running |
| `agent_not_running` | | 1 | attach's, reported by the CLI, as `terminal_not_running` |
| `agent_removed` | 409 | 1 | the agent is a tombstone, kept only for its children |
| `branch_exists` | 409 | 1 | a new worktree's branch already exists |
| `worktree_failed` | 422 | 1 | git could not add the worktree |

`start_failed` carries the row under `details.agent`.

## CLI

```
wdx agent create [--project-id <id> | --project-name <name>] [--name <name>] [--parent-id <id> | --parent-name <name>] [--worktree <name>]
wdx agent list   [--project-id <id> | --project-name <name> | --all]
wdx agent get    (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx agent start  (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx agent attach (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx agent remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--stop-running | --cancel]
```

The same rules as `wdx terminal`: the project defaults to the working directory's, `--json` needs `--stop-running` or `--cancel` on `remove`, completions ask the daemon. Attach uses the terminal's view and keys.

```
$ wdx agent create
created agent swift-falcon in wdx
  dir    /home/me/dev/wdx
  aca    claude_code
  id     agent-1a2b3c4d

$ wdx agent list
NAME          STATE       ACA          DIR                ID
calm-otter    exited (0)  claude_code  /home/me/dev/wdx   agent-5e6f7a8b
swift-falcon  running     claude_code  /home/me/dev/wdx   agent-1a2b3c4d
```

## Tests

CI has no `claude`, and tests never touch the network, so no test runs a real ACA. Everything else runs in CI as it does for terminals, with real holders and real processes:

- **Agent, daemon and cli tests** use a test ACA, `aca/acatest`, a second implementation of `AiCodingAgent` handed in through the daemon's `ACA` option. Its command is `/bin/sh`, and it records the session of each start in a file, so a test can assert which session a relaunch used. One test per outcome of each command and code; close, open, remove and restart of a project with agents; reconciliation with an agent's holder alive across a daemon restart.
- **`aca.ClaudeCode`** has unit tests of `Command` against a temporary home: with and without a transcript.
- **The real thing** is checked by hand before a change here lands: create, attach and see Claude Code, detach, a daemon restart, close and open the project, `agent start` after `/exit`, remove. It sends no prompt unless typed. Nothing automated notices if Claude Code changes its flags or where it keeps transcripts.

Limits known so far: `aca.ClaudeCode` looks for transcripts under `~/.claude`, so a `CLAUDE_CONFIG_DIR` set in the user's rc file would make it start a session that exists instead of resuming it; and resuming a session with a conversation is covered by unit tests of the command, not yet by a run against Claude Code, since that sends a prompt.

## Implementation plan

Each step builds and passes `make test` on its own. All of them are done.

Work happens in one worktree, `.worktrees/agent-design`, its branch renamed `agent` and rebased onto `main` first; it lands by rebase. No step changes what a user can already see, so none is breaking.

1. **`internal/aca`**: `AiCodingAgent`, `Launch`, `ClaudeCode` with the directory of its transcripts as a field, and `aca/acatest` with the test ACA. Unit tests of `Command`, with and without a transcript. `feat:`.
2. **The holder runs a command**: a repeated hidden `--command` on `wdx holder` and `holder.StartOptions.Command`. With one, the holder starts `$SHELL -l -i -c 'exec "$@"'` and the command; without one, the login shell as today. Holder tests: a command's exit code is the terminal's, and no shell stays behind.
3. **Terminal rows of two kinds**: migration 0004 with `kind` and `agents`; `Terminal.Kind`. `Store.Get`, `GetByName` and `List` filter on `kind = 'shell'`; the supervisor's work moves to unexported queries of either kind. `Supervisor.Start`, `Remove` and `History` check the kind first. `Options.AgentCommand`, a function asked by `launch` for rows of kind `agent`; an error there leaves the row `failed`. The sweep, reconciliation, `Live`, `StopProject` and `StartProject` handle both kinds and return them with their `Kind`. Tests with rows of kind `agent` inserted directly: reconciliation adopts them, the sweep leaves them, the terminal entries answer `ErrNotFound`.
4. **`internal/agent`**: `agent.Store` with its joined queries, by id, by name in a project, by project, and `agent.Manager` with `Create`, `Start`, `Remove`, `History` and `Command`, which `Options.AgentCommand` is wired to. The supervisor gets `CreateAgent`, `StartAgent`, `RemoveAgent` and `AgentHistory`, rejecting kind `shell`. `CreateAgent` inserts the terminal row and the agent row in one transaction, so a crash leaves both or neither. A row whose ACA this wdx does not know ends `failed`. Tests with the test ACA and real holders: first launch and relaunch use the same session; a session with a transcript resumes and one without starts afresh; `exited`, `failed` and `lost` start again. `.golangci.yml` gets the `agent` and `aca` directions.
5. **API and daemon**: `api/openapi.yaml` with `/v1/agents` and its history, `Agent`, the project's `agents`, `stopped_agents`, `started_agents` and `details.agents`, `agent_running` and `agent_not_running`; `make generate`. Handlers in `internal/daemon/agents.go`; domain errors mapped in the one place. `stopTerminals` and project open split what the supervisor returns by `Kind`. `project_in_use` says "terminals or agents". The ACA is a daemon option, `aca.ClaudeCode` by default and the test ACA in tests. Daemon tests: one per outcome, a project closed, opened and removed with agents, an agent's holder alive across a daemon restart, an agent's `terminal_id` `not_found` on every terminal path.
6. **CLI**: `internal/cli/agent.go` with `wdx agent create / list / get / start / remove`; `wdx agent attach`, with `attach` split so terminals and agents differ only in how they read a state and a history, and in the holder's id. Help annotations, examples, completions, the prompt on `remove`, `--stop-running` and `--cancel`. Integration tests per outcome with `--json`, a text smoke test per command, the remove prompts through a PTY, attach through a PTY.
7. **Docs and the real thing**: README, AGENTS.md's layout and dependency lines, docs/TERMINALS.md, V1_FEATURES.md, this doc's status. Then by hand with the real `claude`: create, attach and see Claude Code, detach, close and open the project with and without a conversation, `agent start` after `/exit`, a daemon restart while attached, remove. `make check`, `make test` and `make test-stress` on `terminal`, `holder` and `agent`.
