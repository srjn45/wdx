# Sending to terminals and agents

Status: design agreed 2026-10-31. Steps 1 and 2 are built; `wdx terminal send` works. `agent send` and `--instant` wait for agent activity (docs/AGENT_STATE.md), its step 2.

Lets scripts and ACAs give a running terminal or agent a message without attaching.

## Commands

```
wdx terminal send (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text>
wdx agent send    (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text> [--instant]
```

- `POST /v1/terminals/{terminal}/send` with `{"prompt": "..."}`, and `POST /v1/agents/{agent}/send` with `{"prompt": "...", "instant": false}`.
- 200 `{"result": "queued", "terminal": {...}}`, or `"agent"`. The message is in the holder's inbox; it may not have been delivered yet, and the command does not wait for a reply.
- Errors: `not_found`, `send_failed` (new, exit 3); `terminal_not_running` on terminals; `agent_not_running` and `agent_removed` on agents. These two codes are the CLI's only today, and now the API returns them too.
- An empty `--prompt` is a usage error. `--instant` exists only on agents: on a shell the interrupt would be Ctrl-C, which kills whatever is running.
- Enter is always pressed.
- There is no size limit of our own. The one ceiling is the protocol's 1 MiB frame, which the CLI cannot reach (Linux caps one argument at about 128 KiB); a larger request through the API gets `send_failed`.

## Delivery

```
sender ─► wdx ─► daemon ─► control channel ─► inbox ─► inboxReader ─► deliver ─► PTY
```

The words, decided by the human on 2026-10-31:

- **sender**: whoever sends a message: a human, a script or an ACA.
- **inbox**: where a message lands in the holder, with two queues, `instantQueue` and `generalQueue`. They are slices behind a mutex, not channels: a channel's fixed capacity would make the control loop block or refuse a message, and a refusal cannot reach the sender, as `Conn.Send` has no reply. The cost is that nothing bounds them.
- **inboxReader**: the one goroutine that reads the inbox and decides when a message may go in.
- **deliver**: putting one message into the shell or the ACA.
- **`ScreenState`**: what the `inboxReader` gets from `watchScreen`, as `aca.ScreenParser` reads it: `Activity`, `InputBox` and `Draft`, and for `--instant` something still to add that tells a running turn from background work. How it is read from the screen stays in the ACA's parser, so the `inboxReader` never sees the screen.

- The daemon uses its existing connection to the holder (`holder.Conn`), with one new frame carrying the message. A holder too old for it gets `send_failed` with a hint to restart, known from its `hello`.
- The holder's control loop only queues the message, and one goroutine writes to the PTY, so a program that stops reading cannot hold up `stop`.
- The queues live in memory, and `instantQueue` is always emptied first. Queued messages survive a daemon restart, and die with the holder or when the terminal is stopped, which is logged with a count. A relaunch does not bring them back.
- A message the PTY cannot take blocks only that goroutine. `make test` checks this with a program in raw mode that never reads.
- `agent get` shows how many messages are queued, because a held message is otherwise invisible.

### Delivering

The holder stays free of ACA code. How an ACA is delivered to is handed in as the activity parser is: the daemon passes `--aca`, and the hidden `wdx holder` command calls `aca.ByID` (docs/AGENT_STATE.md).

| | Message | Interrupt |
|---|---|---|
| Shell | the text, then Enter | none |
| Claude Code | bracketed paste, about 150 ms, then Enter as a separate key | Esc |

Claude Code needs the separate Enter because one sent while a paste settles can be dropped, and needs the paste so that newlines stay content (both learned in the old warden). A paste-end marker in a prompt is stripped, so a prompt cannot break out of the paste.

### When an agent's message goes in

| `activity` | General | `--instant` |
|---|---|---|
| `idle` | delivers | delivers |
| `working`, in a turn | delivers | presses the interrupt key, waits for the turn to stop, delivers |
| `working`, only background work | delivers | delivers; there is no turn to interrupt |
| `waiting` | holds | holds |
| `unknown` | holds | holds |

- `working` delivers rather than holds because a background dev server keeps an agent `working` indefinitely. This relies on Claude Code queueing input during a turn, still to be checked by hand.
- `--instant` holds on `waiting` because Esc would cancel the prompt, and the parser cannot yet tell which prompt it is; on some, cancelling may exit the CLI. Once the later step that reads the prompt lands, permission prompts could be cancelled.
- Telling a turn from background work needs the parser to expose it to the `inboxReader`. The public `activity` stays as it is.
- A second `--instant` interrupts the turn the first started. General messages wait behind instant ones.
- **`draft` comes before the table.** While there is text in the input box, the `inboxReader` holds, whatever the activity and with `--instant` too, so a message is never submitted along with what a human is typing. `watchScreen` reads `draft` true or false along with the activity and tells the `inboxReader`; it is not public, and `activity` stays `idle` with a draft in the box (docs/AGENT_STATE.md).
- A draft left in the box holds messages for as long as it stays, and they queue up behind it, with no timeout (decided by the human). The queue length on `agent get` shows it.
- Text nobody typed is not a draft: Claude Code fills the box with dim placeholders and suggestions, which read the same in plain text. Reading this correctly is the parser's, and still to be tested on the recordings; read wrong, every message holds.
- The `inboxReader` checks `draft` before the paste, not after: the box holds the message until its Enter. A human who starts typing in the moment between the check and the paste still merges with it.

## Logging

Prompt text is never logged (AGENTS.md), but today the CLI logs raw args and every flag, and the daemon logs request bodies. The arguments and flags record `<n bytes>` in its place and the request body `prompt_bytes`, and tests check that the text appears in no log.

## Protocol

Frame numbers are shared with activity, to avoid a clash: 12 `frameActivity`, 13 `frameSend` (daemon to holder), 14 `frameInbox` (holder to daemon: the queue's length, on each change).

Whichever feature lands second takes the next protocol version: 3 and 4. A released version 3 holder without the inbox would drop `frameSend` silently, so the daemon asks for the version that has it.

Today's daemon accepts only versions 1 and 2, so rolling back to it would lose every newer holder. As for version 2 (docs/ATTACH.md), the daemon's acceptance ships first, in a release of its own that is installed before either feature is released.

## Plan

Each step passes `make test` on its own and works in the worktree `send`. Nothing here breaks a user, so no `!`.

1. **The daemon accepts holder versions 3 and 4.** `drives`, in its own commit. Activity needs it too.
2. **Inbox and `terminal send`.** `frameSend`, the holder's inbox, its `inboxReader` and delivering to a shell, `holder.Conn`, the API, the daemon, the CLI and the logging rule. Independent of activity, so it can land first.
3. **`agent send`.** `AiCodingAgent`'s `Deliver`, the delivery table, Claude Code's paste and Enter. Lands after activity's step 2, so no agent ever gets a message blind. Takes `--aca` and `aca.ByID` from there.
4. **`--instant` and the queue length.** The instant inbox, the interrupt key, `frameInbox`, the field on `agent get`.
5. **Docs and the real thing.** docs/ACA.md, docs/AGENT.md, docs/TERMINALS.md, docs/ATTACH.md's "not in this step" line, V1_FEATURES.md, AGENTS.md. By hand with real `claude`: a prompt during a turn, during startup and at a permission prompt; multi-line; Esc; a narrow tide pane.

## Later

A delivered status for scripts, `--prompt-file` (`--prompt` shows in `ps`), reading an agent's reply, and the stored per-agent inbox of V1_FEATURES.md, which could feed this one.
