# Sending to terminals and agents

Status: built. `wdx terminal send`, `wdx agent send` and `--instant` work, checked on real Claude Code 2.1.316 (see Checked on the real thing). The queue length on `agent get` waits for the state channel to the daemon (docs/AGENT_STATE.md, Sharing state).

Lets scripts and ACAs give a running terminal or agent a message without attaching.

## Commands

```
wdx terminal send (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text>
wdx agent send    (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text> [--instant]
```

- `POST /v1/terminals/{terminal}/send` with `{"prompt": "..."}`, and `POST /v1/agents/{agent}/send` with `{"prompt": "...", "instant": false}`.
- 200 `{"result": "queued", "terminal": {...}}`, or `"agent"`. The message is in the holder's inbox; it may not have been delivered yet, and the command does not wait for a reply.
- Errors: `not_found`, `send_failed` (exit 3); `terminal_not_running` on terminals; `agent_not_running` and `agent_removed` on agents.
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
- **`ScreenState`**: what the `inboxReader` gets from `watchScreen`, as `aca.ScreenParser` reads it: `Activity`, `InputBox`, `Draft`, and `InTurn`, which tells a running turn from background work. How it is read from the screen stays in the ACA's parser, so the `inboxReader` never sees the screen.

- The daemon uses its existing connection to the holder (`holder.Conn`), with one new frame carrying the message. A holder too old for it gets `send_failed` with a hint to restart, known from its `hello`.
- The holder's control loop only queues the message, and one goroutine writes to the PTY, so a program that stops reading cannot hold up `stop`.
- The queues live in memory, and `instantQueue` is always emptied first. Queued messages survive a daemon restart, and die with the holder or when the terminal is stopped, which is logged with a count. A relaunch does not bring them back.
- A message the PTY cannot take blocks only that goroutine. `make test` checks this with a program in raw mode that never reads.
- A held message shows only in the holder's log (`message held`, with its reason), until the state channel to the daemon can carry the queue's length to `agent get`.

### Delivering

The holder stays free of ACA code. How an ACA is delivered to is handed in as the activity parser is: the daemon passes `--aca`, and the hidden `wdx holder` command calls `aca.ByID` and gives the holder its `Deliver` and `Interrupt` (docs/AGENT_STATE.md). A holder that does not know the ACA, an older binary than the daemon that named it, treats it as a shell. Only `claude_code` exists so far, so this cannot happen yet; the next ACA needs to settle it.

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
| `working`, footer too cut off to say | delivers | delivers: it never interrupts on a guess (decided by the human: wdx is for laptops and larger) |
| `waiting` | holds | holds |
| `unknown` | holds | holds |
| no input box, whatever the activity | holds | holds |

- `working` delivers rather than holds because a background dev server keeps an agent `working` indefinitely. Claude Code queues input during a turn (see Checked on the real thing).
- With no input box a message has nowhere to go. The motion fallback can read such a screen `working`, as during startup, so the box is checked as well as the activity.
- `--instant` holds on `waiting` because Esc would cancel the prompt, and the parser cannot yet tell which prompt it is; on some, cancelling may exit the CLI. Once the later step that reads the prompt lands, permission prompts could be cancelled.
- `InTurn` tells a turn from background work, for the `inboxReader` only. The public `activity` stays as it is.
- The interrupt key is pressed once per message: pressed again once the turn has stopped, Esc does something else. A turn that does not stop holds the message until it ends by itself.
- A second `--instant` interrupts the turn the first started. General messages wait behind instant ones.
- **`draft` comes before the table.** While there is text in the input box, the `inboxReader` holds, whatever the activity and with `--instant` too, so a message is never submitted along with what a human is typing. `watchScreen` reads `draft` true or false along with the activity and tells the `inboxReader`; it is not public, and `activity` stays `idle` with a draft in the box (docs/AGENT_STATE.md).
- A draft left in the box holds messages for as long as it stays, and they queue up behind it, with no timeout (decided by the human). The queue length on `agent get` shows it.
- Text nobody typed is not a draft: Claude Code fills the box with dim placeholders and suggestions, which read the same in plain text. Reading this correctly is the parser's; read wrong, every message holds. A message pasted over a suggestion replaces it.
- Claude Code's bash mode counts as a draft even with only its `!` (decided by the human), since a message there would run as a shell command. It holds until the human leaves bash mode.
- The `inboxReader` checks `draft` before the paste, not after: the box holds the message until its Enter. A human who starts typing in the moment between the check and the paste still merges with it.

## Logging

Prompt text is never logged (AGENTS.md), but today the CLI logs raw args and every flag, and the daemon logs request bodies. The arguments and flags record `<n bytes>` in its place and the request body `prompt_bytes`, and tests check that the text appears in no log.

## Protocol

`frameSend`, 13, goes from the daemon to the holder, with `instant` in its payload. 12 is kept for the state channel (docs/AGENT_STATE.md), which can carry the queue's length too.

Version 3 is the inbox, for shells: an older holder would drop `frameSend` silently, so the daemon asks for the version that has it. Version 4 delivers to an ACA and knows `instant`: a version 3 holder would type the message into an ACA with no look at its screen, so `agent send` asks for 4 and gets `send_failed` from an older one. So nothing here needs another acceptance release, but the next version, 5, does.

Today's daemon accepts only versions 1 and 2, so rolling back to it would lose every newer holder. As for version 2 (docs/ATTACH.md), the daemon's acceptance ships first, in a release of its own that is installed before either feature is released.

## Plan

Each step passes `make test` on its own and works in the worktree `send`. Nothing here breaks a user, so no `!`.

1. **The daemon accepts holder versions 3 and 4.** `drives`, in its own commit. Activity needs it too.
2. **Inbox and `terminal send`.** `frameSend`, the holder's inbox, its `inboxReader` and delivering to a shell, `holder.Conn`, the API, the daemon, the CLI and the logging rule. Independent of activity, so it can land first.
3. **`agent send`.** `AiCodingAgent`'s `Deliver`, the delivery table, Claude Code's paste and Enter. Built with step 4, in one commit.
4. **`--instant`.** The instant queue, `InTurn` and the interrupt key. Built.
5. **Docs and the real thing.** Done; see below. A narrow pane was not checked, as a footer too cut off to say only costs the interrupt.
6. **The queue length on `agent get`.** With the state channel to the daemon, built separately.

## Checked on the real thing

On 2026-10-31, on real Claude Code 2.1.316 under a real `wdx holder` at 120x40, in `--safe-mode` with Haiku: `holdercheck send`, in `internal/aca/testdata/claude_code/record/`.

| Sent | What happened |
|---|---|
| During startup | held for no input box, then the trust prompt, again for no input box, delivered at the first `idle`, and Claude replied |
| Three lines at idle | one message, one turn |
| General, during a turn | Claude queued it (`Press up to edit queued messages`) and replied after the turn |
| Instant, during a turn | Esc, `Interrupted`, the turn stopped in under 200 ms, delivered, replied |
| General then instant, in one turn | Esc, Claude ran the queued one, then the instant one, in that order: **the general reply comes first** |
| With a draft in the box | held; delivered once the draft was cleared |
| Instant, at a permission prompt | held; delivered once the prompt was cancelled |

So "`instantQueue` is always emptied first" holds in the inbox only: what is already queued inside Claude Code runs before an instant message.

## Later

A delivered status for scripts, `--prompt-file` (`--prompt` shows in `ps`), reading an agent's reply, and the stored per-agent inbox of V1_FEATURES.md, which could feed this one.
