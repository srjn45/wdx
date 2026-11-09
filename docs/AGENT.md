# Agents

Status: implemented. Checked against the real Claude Code 2.1.314 (see Tests).

An agent is an AI coding agent (an ACA: Claude Code for now, Codex, Cursor, Antigravity later) running in a terminal of a project. It has everything a terminal has, its holder included: it outlives the daemon and the window that showed it, and you attach to it and detach from it the same way. On top it knows which ACA it runs and which session of that ACA is its own.

Child agents and tombstones are in docs/CHILD_AGENTS.md. What the ACA is doing, `activity` and `draft`, the prompt it asks while waiting, `prompt`, and the model it shows, `model`, are in docs/AGENT_STATE.md. Not in this step: choosing a model, an initial prompt, the agent pane in tide. `state` below is only whether the process is alive. Two agents in one checkout share its files.

## Architecture

```
 wdx agent create ──HTTP/unix──► daemon ─► agent ─► terminal supervisor ──starts──► holder (wdx holder)
                                            │                                         └─ $SHELL -l -i -c "exec 'claude' ..."
                                            └─► aca: the command to run
```

- An agent is a row in `agents` pointing at one row in `terminals` of kind `agent`, which keeps the name, dir, pid, exit code and process state. The holder, its protocol, attach, history, reconciliation and stopping are the terminal's, unchanged.
- That is storage: an agent is always read joined with its terminal row, and the terminal API and `wdx terminal` deal only with kind `shell`, and not with the editors' rows of that kind, which have an `editor_path` (docs/TIDE_CODE.md). The one trace outside is `terminal_id` in an agent's JSON, which attach needs to reach the holder and which no terminal path accepts.
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
- wdx generates the session id, a UUID, when it creates the agent. A `/clear` starts another session, and the agent's `session_id` follows it: a start goes on with the latest conversation (docs/AGENT_RESUME.md).
- A `--settings` file adds a hook that tells wdx each session, and a transcript Claude Code has deleted comes back from wdx's backup.

### Context size

How full each agent's context is (the wdx docs CONTEXT_SIZE, CODEX_CONTEXT, CURSOR_CONTEXT and ANTIGRAVITY_CONTEXT), read through `ContextSize(Launch)`, which returns the tokens in the context and the model's window, 0 when the ACA does not tell, or `ErrNoContextSize` before it has told anything, and the files outside the backup it read, which the daemon follows too, named even with `ErrNoContextSize` when known.

- **Claude Code** tells it through its status line. The `--settings` file also sets a `statusLine` whose command writes the JSON Claude Code gives it to `claude-status.json` in the backup, whole, through a file of its own renamed into place. `ContextSize` reads `context_window.total_input_tokens` and `context_window_size` from it. Before the first message and after `/clear` the tokens are 0; no file is `ErrNoContextSize`, and one that does not parse an error, logged.
- **The user's own status line:** one given with `--settings` replaces theirs (checked on 2.1.319), so at each start wdx reads `statusLine.command` from their `~/.claude/settings.json` and runs it after its own, in braces, on the same input: what it prints still shows in the agent's terminal. A project's own `.claude/settings*.json` status line is replaced.
- **Codex** tells it in its session file (`~/.codex/sessions/.../rollout-<time>-<session id>.jsonl`), which it appends to as a turn goes: after every model response an `event_msg` line of type `token_count`, whose `info.last_token_usage.total_tokens` is the last request, input (cached included) and output, and `info.model_context_window` the window; a session's first such line has `info` null and says nothing, and `total_token_usage` adds up every request, so it is not the context. `ContextSize` reads the agent's session, `Launch.SessionID`, where `codex-sessions.json` in the backup says Codex keeps it, else where `BackUp`'s glob finds it, from the end in 64 KiB blocks back to the last `token_count` line whose `info` is not null, never whole: the file can be several MB. It returns that file to follow. Before the first turn nothing is known, since Codex names the session and wdx learns it from the backup at the turn's end; after a compaction the size is the summary's request, until the next response. Subagents are not counted. A session Codex deleted shows nothing until `Command` puts the backup's copy back at the next start (checked on 0.163.0).
- **Cursor** tells it through its status line too, in Claude Code's JSON: the `statusLine` of the agent's own `cli-config.json` (`cursorWriteSettings`) writes what Cursor gives it to `cursor-status.json` in the backup, whole, renamed into place, and `ContextSize` reads the same two fields from it. Its `total_input_tokens` is the footer's share of the window rounded to tokens, so it moves in steps of about a thousandth of the window. Cursor splits the command on whitespace and quotes as a shell would but runs no shell (string-argv, spawned without one), so the command is `sh <backup>/cursor-status.sh`, a script holding the same text as Claude Code's command (`statusLineCommand`, `internal/aca/statusline.go`), and the script's path is given raw, or in the kind of quote it does not hold; one holding both kinds fails the start. Before the first message and after `/clear` every field is null, read as 0 tokens and no window, shown as `0`: the file cannot tell the two apart. In a chat whose size it knows, the size stays through a turn and changes with each response, so after a compaction it is the summary's request's only at the next response; it runs about every 300 ms while a turn streams (checked on 2026.11.05).
- **The user's own Cursor status line:** the agent's `cli-config.json` replaces theirs, so at each start wdx reads `statusLine` from their `~/.cursor/cli-config.json`: when it is a command, it runs after wdx's in the script, in braces, on the same input, and their `padding`, `updateIntervalMs` and `timeoutMs` are kept; what it prints still shows in the agent's terminal. One that is not a command gives wdx's alone.
- **Antigravity** tells it through its status line too, in Claude Code's JSON: `Command` passes a `statusLine` in `--settings` whose command writes what Antigravity gives it to `antigravity-status.json` in the backup, whole, renamed into place, and `ContextSize` reads the same two fields from it. Antigravity runs the command through a shell, so it is the same text as Claude Code's (`statusLineCommand`). Its `statusLine` has `command`, `padding`, `timeout_ms` and `stack_with_default`, no `type`; wdx always passes `stack_with_default: true`, as without it the command's output replaces Antigravity's own footer, which `AntigravityScreenParser` reads. Before the first message and after `/clear` the tokens are 0 and the window known, as for Claude Code; after `/compact` the size is the compaction's own request, until the next response. It runs on each change, about 7 times at startup and a dozen in a short turn, never at rest (checked on 1.5.2).
- **The user's own Antigravity status line:** one given with `--settings` replaces theirs (checked on 1.5.2 in a scratch home), so at each start wdx reads `statusLine` from their `~/.gemini/antigravity-cli/settings.json`: when it has a command, it runs after wdx's, in braces, on the same input, and their `padding` and `timeout_ms` are kept. Antigravity draws what it prints under its footer, which the parser does not read (docs/AGENT_STATE.md).
- **When it is read:** the daemon watches each running agent's backup folder, through its one inotify instance (`internal/fswatch`, the wdx doc WATCHERS) (`agent.StatusReads`, told by the terminal supervisor's `AgentFollowed` when a holder is followed and when it no longer is), and the files `ContextSize` returned, those new added and those gone dropped after each read, and reads it once when the watch starts, then after each change in any of them, at most once a second (`DefaultStatusInterval`) plus once at the end of a burst. Before each read the agent's row is read again, for its session: a backup changes it when the ACA names a new one, after the first turn of a Codex agent or a `/new`, and asks for a read then (`Backups.OnSessionChange`, `StatusReads.SessionChanged`), so that the new session's file is followed at once rather than at the next change of the backup. A file followed only now is read again at once, for what was written between the read and the follow. The same read takes the usage below. An ACA that answers `ErrNotSupported` to both is not watched.
- **Stored** in `agents.context_tokens` and `agents.context_window` (`0026_agent_context.sql`), written only when they change, and sent to the project stream as any change of the agent. They stay once the terminal ends, and a restart into a new session clears them, while `BeginSession` removes `claude-status.json`, Cursor's `cursor-status.json` and Antigravity's `antigravity-status.json`, or the old session's size would be read back; Codex's `BeginSession` has nothing to remove, as the old session's file is no longer the agent's.
- **Logged** at debug only, as it changes often: `context size read` with `agent_id`, `tokens` and `window`.

### Usage

How much of each ACA's account's limits is used (the wdx doc USAGE), read through `Usage(Launch)`, which returns a row per window and bucket of models, each with the window's key and the bucket's name as the ACA gives them, the percentage used, when the window resets, when the ACA told it, and the files outside the backup it read; `ErrNoUsage` while it has told nothing, as before the first answer or on a plan without limits. Usage is the account's, not the agent's: every agent of an ACA on this machine draws on the same one.

- **Claude Code** tells it through its status line too: `rate_limits` in `claude-status.json`, a window per key, `five_hour` and `seven_day` on a claude.ai Pro, Max or Ultra plan, `spend_limit` behind a gateway, each with `used_percentage` and `resets_at` in epoch seconds, none naming a bucket. The block appears only after the first API response and Claude Code drops a window once its reset passes; a key wdx does not know is a row like any other (checked on 2.1.319). The Fable weekly its `/usage` screen shows is not in the JSON.
- **Antigravity** tells it through its status line too, since 1.5.x: `quota` in `antigravity-status.json`, an entry per id, each with `remaining_fraction` and `reset_time` (`internal/aca/antigravity_usage.go`). An id is split at its last `-` into bucket and window, `gemini-5h` the bucket `gemini`'s `5h`, `gemini-argon-wtu-weekly` the bucket `gemini-argon-wtu`'s `weekly`, as `agy -p /usage` lists them; every limit is a group of models', so no row has an empty bucket. The percentage used is rounded down, so that 100 means nothing is left, not 0.4 % left; a fraction left out beside a `reset_time` is 100, an entry with neither is skipped. agy fetches the quota about a second after start and again at most about once a minute while it works, so the file's time, taken as when it was told, may be a minute late; untouched windows come with their reset as given. The status line now also carries the user's `email`: nothing logs the status JSON whole, only counts and fields (checked on 1.5.3, the wdx doc USAGE_ANTIGRAVITY).
- **Codex** tells it in the session file the context size is read from: each `token_count` line carries `rate_limits`, a snapshot of one bucket (`limit_id`) with a `primary` and a `secondary` window, slots told apart by `window_minutes` (`internal/aca/codex_usage.go`). The newest snapshot of `codex`, the bucket every model draws on and so `""`, is read back at most 1 MiB from the file's end, with the newest of each other bucket after it, under its own name. Windows are keyed by their period, `300m`, `10080m`, `43200m`; one of 0 minutes, or a snapshot without windows, gives no row. At a limit Codex may stop at 99 % and refuse each prompt with a `task_complete` whose error is `usage_limit_exceeded`: one after the newest snapshot marks the `codex` rows `Limited`, observed when the error was written. The file to follow is the session file, as for the context (checked on 0.142.3 to 0.167.0, the wdx doc USAGE_CODEX).
- **Cursor** writes it nowhere wdx can read, so the daemon asks Cursor's API, the one place wdx uses the network for an ACA, the human's choice (the wdx doc USAGE_CURSOR): `Cursor.FetchUsage` (`aca.UsageFetcher`, `internal/aca/cursor_usage.go`) posts `GetCurrentPeriodUsage`, as Cursor's `/usage` screen does, with the access token Cursor's CLI keeps in `~/.config/cursor/auth.json`, read at each call and never stored, logged or refreshed by wdx, and writes the answer whole to `cursor-usage.json` in the backup. `Usage` reads that file as any other ACA's: the billing month, window `month`, of the included allowance (bucket `""`), of `auto` and of `api`, each a whole percent, reset at the cycle's end; on-demand spend is left out, and an answer without the plan's usage is none. The fetch runs on a change to a Cursor agent's backup, so only while it works, at most once a `daemon.Options.UsageFetchInterval` per agent (5 min by default), one at a time in a goroutine of the agent's status watch with a 10 s timeout, so that no read waits for the network. No login file asks nothing; a 401, logged out or a token the CLI has not refreshed yet, writes nothing and is logged once per agent, as `usage not fetched` with its `status` and `code`, never the body. On macOS the CLI keeps its login in the keychain, which wdx does not read (checked on 2026.11.05 on Linux, a Pro plan).
- **The gate:** a row is `limited` when its percentage is 100 or more or when the ACA said so (`aca.Usage.Limited`, only Codex's error so far), whatever the percentage, and a bucket is available only while none of its windows is limited (`api.UsageBuckets`).
- **When it is read:** with the context size, by `agent.StatusReads`, from the same changes of the same files, at the same pace; Cursor's answer arrives as a change of its backup.
- **Stored** in `aca_usage` (`0028_aca_usage.sql`), a row per ACA, window and bucket, the latest observation only, with `limited` as the gate above says and the agent it was read from. A row replaces the window's when its observation is newer; one whose reset has passed is skipped, so that a stale file read late brings no window back. A window no longer told is dropped once its reset has passed, or at once when it had none, and kept until then as of its last observation. A read that tells nothing changes nothing. Every change notifies the project streams.
- **Logged** at debug on each read, `usage read` with `aca` and `rows`, and at info when a row changed, `usage recorded`, or a window was dropped; Cursor's fetch at info, `usage fetched` with `status` and `duration_ms`, and at error when it failed, once per agent for a 401.
- **Served** as `GET /v1/usage` and `wdx usage`, the rows as stored. What follows from them, a bucket available while none of its windows is limited and until when otherwise, the labels of the windows and buckets wdx knows and the time left to a reset, is derived in `internal/api` (`UsageBuckets`, `UsageLabel`, `UsageBucketLabel`, `ResetText`), so that the CLI and tide say the same from the same rows and the daemon stores nothing derived.
- **Joined to agents** as `Agent.limited` (API, below): which buckets a model draws on is `ModelBuckets`, per ACA (docs/ACA.md); the daemon derives the limit from these rows when it reads agents, and a timer at the earliest limited reset tells the streams when it ends.

### Kept for every start

An agent is created with a model and permissions (the wdx doc AGENT_CREATE), in the `requested_model` and `permissions` columns (`0023_agent_launch.sql`), and `agent.Launches`, the one place every start goes through (create, `agent start`, `agent restart`, a relaunch with its project or after the daemon restarts), passes both in `Launch.Model` and `Launch.Permissions`. Neither changes after creation for now.

- `model`: as the ACA's own model flag takes it; none is its default. For Claude Code `--model <model>`: `fable`, `opus`, `sonnet` and `haiku` are always the newest of their class.
- `permissions`: `full`, the default, asks for no approval, with network access and no sandbox; `ask` is the ACA's default, as the user's settings for it say. For Claude Code `full` is `--permission-mode bypassPermissions`, and its `--settings` file sets `skipDangerousModePermissionPrompt`, so that the warning it shows the first time does not stop the agent, and `sandbox.enabled: false`; `ask` passes nothing. For Cursor `full` is `--force --sandbox disabled --approve-mcps`, and `ask` passes nothing; every start passes `--trust` (the wdx doc ACA_CURSOR).
- Agents created before read `ask`, with no model, as that is what they ran with.
- What models there are, each ACA says through `Models()`: `GET /v1/acas/{aca}/models`, `wdx agent models [--aca <id>]`, and `--model`'s completion. Claude Code's are its aliases, fixed in wdx.

Which AI coding agent an agent runs is chosen at creation, `--aca <id>` or tide's form, among the usable ones: installed, as the login shell finds its program, and enabled by `aca.<id>.enabled`. By default the first usable, in the order `aca.All()` gives; `wdx agent acas` and `GET /v1/acas` list them. Otherwise `aca_unavailable` (the wdx doc ACA_SELECT):

| | Not installed | Disabled |
|---|---|---|
| A running agent | goes on | goes on |
| `agent start`, `agent restart`, a relaunch with its project or after the daemon restarts | refused before anything stops; a relaunch leaves it failed, saying why | runs |
| `agent transcript`, `get`, `remove` | work | work |

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
| `agent start` | from `exited`, `failed` or `lost`: starts it again, in the same session, so the conversation comes back, in the directory it last worked in if that is still there, else its own |
| `agent restart` | from any state but removed: a new session under the same id, whose first message tells it to read its history; docs/AGENT_RESUME.md |
| `agent rename` | changes the name on its terminal row; the process and session go by id and do not notice. A tombstone cannot be renamed |
| `agent remove` | stops a live process (`--stop-running`), deletes both rows, moves wdx's backup of the conversation to the trash for 7 days (docs/AGENT_RESUME.md); the ACA's own transcript stays on disk |
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
| `POST /v1/agents/{agent}/restart` with `{"force": false}` | 200 `{"result": "restarted", "agent": {...}}` | `not_found`, `project_closed`, `agent_busy`, `agent_removed`, `start_failed` |
| `DELETE /v1/agents/{agent}?stop_running=true` | 200 `{"result": "removed", "agent": {...}}` | `not_found`, `agent_running` |
| `GET /v1/agents/{agent}/history?tail=` or `?from=&count=` | 200 `{"history": {...}}`, as for terminals; what attach scrolls through | `not_found` |
| `GET /v1/agents/{agent}/transcript?tail=` or `?from=&count=` | 200 `{"transcript": {...}}`; the conversation as text, docs/AGENT_RESUME.md | `not_found` |

`{agent}` is `id:` and its id, or a name together with `?project=<ref>` or `?dir=/abs`, as for terminals.

```json
{"id": "agent-1a2b3c4d", "name": "swift-falcon", "project_id": "proj-…", "project_name": "wdx",
 "parent_agent_id": null, "children": [], "terminal_id": "term-5e6f7a8b", "aca": "claude_code", "session_id": "…", "dir": "/home/me/dev/wdx", "worktree": "main",
 "state": "running", "activity": "idle", "prompt": null, "draft": false, "queued": 0, "model": "Haiku 5.5", "context": {"tokens": 70086, "window": 200000}, "limited": null, "pid": 4242, "exit_code": null, "error": null,
 "created_at": "…", "updated_at": "…", "started_at": "…", "ended_at": null, "removed_at": null}
```

`terminal_id` is there because attaching dials that terminal's holder. Text output never shows it.

`prompt` is what the agent asks while `activity` is `waiting`, as its screen shows it and the holder reads it (docs/AGENT_STATE.md, Why it is waiting; the wdx doc CLAUDE_PROMPTS): `{"kind": "yes_no", "title": "Bash command", "question": "Do you want to proceed?", "detail": "touch wdx-a.txt", "options": ["Yes", "Yes, and always allow access to /tmp/… from this project", "Yes, and switch to auto mode · auto mode handles these prompts for you", "No"], "selected": 0, "multi": false}`, or a `decision` with `"title": "Fruits"`, `"detail": null`, `"multi": true` and `"ticked": [0]`, the indexes of the options whose box is ticked, in order, `[]` when none is and for every other prompt. `title` and `detail` are null when the prompt has none, `selected` when no option is marked; the whole is null when no prompt is read, when the agent is not waiting, and when its holder is too old to tell. The text is kept only while the prompt is open: the row is cleared when the terminal ends, and the logs hold its kind and options' count, never its words. `agent get` shows `prompt` (`yes/no · Bash command`), `question`, `detail` and `options`, `❯` before the selected one and, in a multi-select, `[✔]` before a ticked option and `[ ]` before the others; `agent list`'s `ACTIVITY` column reads `yes/no` or `decision` in place of `waiting`.

`context` is null until the agent's ACA has told it, and again once a restart begins a new session; its `window` is null when the ACA does not tell. `agent get` shows it as `context  70k of 200k (35%)`, or `70k` with no window, and `agent list` in a `CONTEXT` column, the size alone, empty when it is null; both color it green below `context.threshold.warning`, yellow from it and red from `context.threshold.critical` (`style.ContextFine`, `ContextWarning`, `ContextCritical`), as tide does. One function writes a size, `api.TokenCount`: the number under 1,000 (`850`), thousands rounded down with `k` (`150k`), millions with one decimal rounded down with `M` (`1.5M`, `2M`).

`limited` is the usage bucket its model draws on that is limited now, `{"bucket": "3p", "resets_at": "…"}`, the bucket as its ACA names it (`""` for the one every model draws on) and its reset, null when it does not tell; null when none is, while the agent is not running and when its ACA has told no usage. A model `ModelBuckets` does not know is limited only while every bucket of its ACA is. The daemon works it out from `aca_usage` each time agents are listed, got or streamed, and stores nothing (docs/AGENT_STATE.md, Limited by its account; the wdx doc AGENT_LIMITED). `agent get` shows it as `limited  Claude and GPT models, resets in 2 h`, and `agent list`'s ACTIVITY reads `limited` unless the agent is working.

Projects:

- `GET /v1/projects/{project}` and its event stream set `agents` beside `terminals`.
- `project_in_use` counts live agents too, and lists them under `details.agents`.
- Results list them under `stopped_agents` and `started_agents`, left out when empty as `stopped` and `started` are.

New codes in `internal/api/codes.go`:

| Code | Status | Exit | |
|---|---|---|---|
| `agent_running` | 409 | 1 | the agent is already starting or running |
| `agent_busy` | 409 | 1 | the agent is in the middle of something, which a restart would lose |
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
wdx agent rename (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --new-name <name>
wdx agent attach (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
wdx agent restart (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--force]: docs/AGENT_RESUME.md
wdx agent transcript (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--tail <lines> | [--from <line>] [--count <lines>] | --path | --brief]: docs/AGENT_RESUME.md
wdx agent send   (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text> [--instant]: docs/SEND.md
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
NAME          STATE       ACTIVITY  MODEL      CONTEXT  ACA          DIR                ID
calm-otter    exited (0)                       150k     claude_code  /home/me/dev/wdx   agent-5e6f7a8b
swift-falcon  running     waiting   Haiku 5.5  70k      claude_code  /home/me/dev/wdx   agent-1a2b3c4d
```

## Tests

CI has no `claude`, and tests never touch the network, so no test runs a real ACA. Everything else runs in CI as it does for terminals, with real holders and real processes:

- **Agent, daemon and cli tests** use a test ACA, `aca/acatest`, a second implementation of `AiCodingAgent` handed in through the daemon's `ACA` option. Its command is `/bin/sh`, and it records the session of each start in a file, so a test can assert which session a relaunch used. One test per outcome of each command and code; close, open, remove and restart of a project with agents; reconciliation with an agent's holder alive across a daemon restart.
- **`aca.ClaudeCode`** has unit tests of `Command` against a temporary home: with and without a transcript.
- **The real thing** is checked by hand before a change here lands: create, attach and see Claude Code, detach, a daemon restart, close and open the project, `agent start` after `/exit`, remove. It sends no prompt unless typed. Nothing automated notices if Claude Code changes its flags or where it keeps transcripts.

Limits known so far: until its hook has told a session's path, `aca.ClaudeCode` looks for transcripts under `~/.claude`, so a `CLAUDE_CONFIG_DIR` set in the user's rc file would make its first start of a session resume nothing; and resuming a session with a conversation is covered by unit tests of the command, not yet by a run against Claude Code, since that sends a prompt.

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
