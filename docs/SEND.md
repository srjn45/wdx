# Sending to terminals and agents

Status: built. `wdx terminal send`, `wdx agent send` and `--instant` work, checked on real Claude Code 2.1.316 (see Checked on the real thing); an instant message to Claude Code is now steered, checked by hand on 2.1.319 (the wdx doc CLAUDE_STEER), and so is one to Codex and to Cursor, which steers every message, checked by hand on 2026.11.05 (the wdx doc CURSOR_PARITY). The queue length is on `agent get`, through the state channel to the daemon (docs/AGENT_STATE.md, Syncing to the daemon). Since the wdx doc SEND_CONFIRMED, a typed message is submitted once the screen shows its paste, and the holder confirms the draft went, checked by hand on all four ACAs under load.

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

`wdx agent create --prompt` gives a new agent its task the same way: the first message in its inbox, kept and held back like any other until the agent is ready for it. So no ACA needs a way to take a task when it starts. A prompt that cannot be sent leaves the agent running, and `prompt_error` says why.

## Keeping a message until it arrives

Decided with the human on 2026-11-01. Every message is first stored in the table `messages` in the daemon's database, then forwarded to the holder as it always was. A holder's inbox still decides when to type, with the rules below, so it stays what it was, and works with no daemon there; the table is the record, and the only thing recovered from.

```
send -> daemon stores it -> forwards it to the holder's inbox -> typed -> holder reports its id -> daemon marks it delivered
```

| State | Means |
|---|---|
| `pending` | stored, and on its way |
| `delivered` | the holder typed it in. Its text is cleared, as it can hold secrets |
| `held` | its holder died, and what it was meant for is gone: it waits for a human |
| `unconfirmed` | handed to a holder too old to say what became of it. It is not sent again |

- **The holder confirms.** A hello says `confirms`, and a message has an id. `frameDelivered` reports what it typed, and a hello lists the last ones typed, including with no daemon there. Neither takes a protocol version: an older holder says nothing, an older daemon skips the frame, and an older holder ignores the id.
- **Sending again is harmless.** A holder ignores an id it has seen. So on connecting to any holder, as at every daemon start, the daemon just sends all that is `pending`.
- **A holder that died.** What was forwarded to another holder goes on when the agent resumes the same conversation, by `session_id`. Otherwise, or in a shell started anew, it is `held`: an old "commit" must not arrive out of nowhere. `wdx message list`, `resend` and `discard` deal with it, and an agent's row says `1 held`.
- **A restart is the exception.** `wdx agent restart` moves what is pending to the new session, as a human asked for it and it is the same agent going on, behind a first message that tells the agent to read its history. They become general, or they would interrupt that reading. docs/AGENT_RESUME.md.
- **A review** follows its message, by `review_comments.message_id`. Its comments are `delivered` when it is, and held it goes, giving them back as drafts to send again. So does its agent being removed, through a trigger.
- **With no daemon.** Holders go on typing what they have, and report it when one comes. A new message cannot be sent. The holders of agents go on either way.
- **It may come twice.** A holder that typed a message and died before it could say has it sent again. `attempts` above 1 says so. Losing one is worse.
- `queued` on an agent still comes from its holder: how many wait in its inbox.

## Delivery

```
sender ─► wdx ─► daemon ─► control channel ─► inbox ─► inboxReader ─► deliver ─► PTY
```

The words, decided by the human on 2026-10-31:

- **sender**: whoever sends a message: a human, a script or an ACA.
- **inbox**: where a message lands in the holder, with two queues, `instantQueue` and `generalQueue`. They are slices behind a mutex, not channels: a channel's fixed capacity would make the control loop block or refuse a message, and a refusal cannot reach the sender, as `Conn.Send` has no reply. The cost is that nothing bounds them.
- **inboxReader**: the one goroutine that reads the inbox and decides when a message may go in.
- **deliver**: putting one message into the shell or the ACA.
- **`ScreenState`**: what the `inboxReader` gets from `watchScreen`, as `aca.ScreenParser` reads it: `Activity`, `InputBox`, `Draft` and its text `DraftText`, `InTurn`, which tells a running turn from background work, and `SteerPending`, a steer the ACA has not taken yet. How it is read from the screen stays in the ACA's parser, so the `inboxReader` never sees the screen.

- The daemon uses its existing connection to the holder (`holder.Conn`), with one new frame carrying the message. A holder too old for it gets `send_failed` with a hint to restart, known from its `hello`.
- The holder's control loop only queues the message, and one goroutine writes to the PTY, so a program that stops reading cannot hold up `stop`.
- The queues live in memory, and `instantQueue` is always emptied first. Queued messages survive a daemon restart and die with the holder, which is logged with a count. What brings them back after that is the table (see Keeping a message until it arrives).
- A message the PTY cannot take blocks only that goroutine. `make test` checks this with a program in raw mode that never reads.
- A held message shows only in the holder's log (`message held`, with its reason), until the state channel to the daemon can carry the queue's length to `agent get`.

### Delivering

The holder stays free of ACA code. How an ACA is delivered to is handed in as the activity parser is: the daemon passes `--aca`, and the hidden `wdx holder` command calls `aca.ByID` and gives the holder its `Paste`, `Submit`, `SendNow` and `Interrupt` (docs/AGENT_STATE.md). Each step writes its keys and nothing else, with no wait in it: when each runs is the holder's, from the screen (Typing, below). A holder that does not know the ACA treats it as a shell, which cannot happen: the daemon starts a holder from its own binary, naming its agent's ACA then, so a holder is never older than its ACA. One that outlives an update was started with an ACA its binary knew.

| | `Paste` | `Submit` | `SendNow`, for an instant message | Interrupt |
|---|---|---|---|---|
| Shell | the text | Enter | none | none |
| Claude Code | bracketed paste | Enter, a separate write | Ctrl+X Ctrl+S, a separate write | Esc, now never pressed by the inbox |
| Antigravity | bracketed paste | alt+m (`\x1bm`) in one write, which reads it at the turn's next step and submits it at rest | nothing: `Submit` already steers | Esc, never pressed by the inbox |
| Codex | a space before a message starting with `/`, then bracketed paste | Enter, a separate write, which Codex reads at the running turn's next step and submits at rest | nothing: `Submit` already steers | Esc, never pressed by the inbox, as Codex steers |
| Cursor | bracketed paste, a space after a final `\` | Enter, `pasteFloor` (150 ms), Enter, general and instant alike, so that a message sent during a turn is read at its next step | nothing: `Submit` already steers | Ctrl+C, never pressed: Cursor steers (the wdx doc CURSOR_PARITY) |

Claude Code needs the separate Enter because one sent while a paste settles can be dropped, and needs the paste so that newlines stay content (both learned in the old warden). A paste-end marker in a prompt is stripped, so a prompt cannot break out of the paste.

### Typing

How `deliver` types a message into an ACA, `internal/holder/deliver.go` (the wdx doc SEND_CONFIRMED, after two Enters lost on a loaded machine, taken by a starved Claude Code as part of a paste it had not finished reading):

1. **Paste**, once the table and the `not reading keys` check let the message go.
2. **Wait for the draft.** Until the screen reads a draft, at least `pasteFloor` (150 ms) and at most `pasteCeiling` (2 s) after the paste. Past the ceiling the holder logs `paste not shown` and goes on as before the screen was read: it submits, and confirms nothing, as nothing tells what the box would hold.
3. **Remember the draft's text**, `ScreenState.DraftText` as the parser reads it then, what the holder's own paste produced: for a long message Claude Code's `[Pasted text #1 +58 lines]`, for a short one the text. Kept in memory for this delivery only.
4. **Submit.** The message counts as typed now: `message delivered` or `message steered` is logged and `frameDelivered` reported, so the daemon's `delivered` keeps meaning the holder typed it.
5. **Confirm.** Wait until the screen reads no draft, up to `submitSettle` (1 s) after the draft last changed: only a change of `Draft` or `DraftText` from the state last looked at restarts the wait, never a change of any other part of the state, as a Codex turn flipping `in_turn` several times a second or a Claude Code spinner (the wdx doc STATE_LEFTOVERS); so a submit lost in a turn is retried after one settle, not at the turn's end. The draft gone is the submit confirmed. Still there:
   - with the text remembered: the submit was lost, so `Submit` again, log `message submitted again`, and confirm again; at most `submitRetries` (2) times, then `message not submitted`;
   - with any other text: a person typed, or the ACA changed the box. `message left in the box`, and nothing is pressed.
6. **Send now.** For an instant message on an ACA that steers, once confirmed (or when the paste never showed), `pasteFloor` after, `SendNow`; never when the message was not submitted or was left in the box.

So **a person's text is never submitted**: the submit key is pressed again only on a box that holds exactly what the paste produced. The confirm looks at the draft only, never at the activity, which Claude Code reads `working` at startup with nothing running. The holder never looks for `[Pasted text` or any rendering, so a new one in an ACA's next version changes nothing. A shell, whose screen nothing reads, gets its text and Enter at once.

The four waits are fields of the holder, `Options.SendTimes`, set from those constants where zero, so a test sets them short. The log lines carry sizes, attempts and milliseconds, never a message's or a draft's text: `message delivered` / `message steered` with `paste_shown_ms` (absent when the paste never showed) and `submitted_ms`, `paste not shown` with `waited_ms`, `message submitted again` with its `attempt`, `message not submitted` and `message left in the box` with the `attempts`.

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

- `working` delivers rather than holds because a background dev server keeps an agent `working` indefinitely.
- **A message delivered during a turn is read at the agent's next step, not after the turn** (decided by the human on 2026-11-06), on every ACA that can. Claude Code queues it with Enter: 2.1.316 kept it until after the turn (see Checked on the real thing), and 2.1.319 takes it in at the next tool result in the same turn, or at the turn's end if no tool call is left (the wdx doc CLAUDE_STEER). Codex 0.163.0 takes a message submitted with Enter, which its `Submit` presses, into the running turn while it thinks, runs a tool or streams, and submits it at rest (the wdx doc CODEX_STEER; checked again by hand on 2026-11-06: sent while `sleep 25` ran, it was in the turn's reply); Tab, which it pressed before, queues it for after the turn. Antigravity 1.5.2 reads it at the turn's next step too, with alt+m, which with `queuedMessages: queue` pinned sends it to the running turn (the wdx doc ANTIGRAVITY_PARITY). Cursor 2026.11.05 reads it at the next step too: its `Submit` presses Enter twice for every message, with `steering` pinned in the agent's own config folder (the wdx doc CURSOR_PARITY).
- So `--instant` differs from a general message in going first in wdx's inbox, and for Claude Code in being sent at once rather than at the next step. Once two messages are in the ACA they are taken in the order they were typed: a general one Codex has not taken in yet is taken before an instant one typed after it.
- With no input box a message has nowhere to go. The motion fallback can read such a screen `working`, as during startup, so the box is checked as well as the activity.
- **Not reading keys comes after the table.** When the screen would let a message go (deliver, steer or interrupt), the `inboxReader` first asks the PTY whether it still reads input a line at a time (`ICANON`, read with `tcgetattr` on the master, `pty.Canonical`), and holds the message while it does, with the reason `not reading keys`; nothing is typed, the interrupt key included. Every agent's holder does so, whatever its ACA; a shell's does not, as a shell running `cat` reads lines. Found on Cursor 2026.11.05 (the wdx doc CURSOR_PARITY, decision 14): it draws its box and footer a few tens of ms before it switches its terminal to raw mode, and a message pasted in that gap stays in the box with both its Enters lost, even when they come once it reads keys (3 of 3 by hand). A change of mode wakes nothing, so only while it holds for this reason does the `inboxReader` look again every 50 ms (`keysCheckTime`); it logs `message held` once as the hold starts and `message no longer held` once as it ends. Checked on Linux only: macOS, where the ioctl is `TIOCGETA`, is untested, as no CI runs there.
- **A steer pending comes before the table, after `draft`.** While `ScreenState.SteerPending` is true the `inboxReader` holds every message, general and `--instant`, with the reason `steer pending`. Only Cursor's parser sets it, from a follow-up its panel marks `· steering`: while Cursor streams an answer, a steered follow-up waits for the next tool call or the stream's end, and Enter on it meanwhile stops the stream and sends it, so the second Enter of the next message would cut the answer (checked by hand on 2026.11.05: with the hold the stream ran to its end, the steer was taken, then the next message went in). The holder publishes every change of `ScreenState`, so the steer being taken wakes the held message. After a human's Ctrl+C, a follow-up left `· steering` stays marked, idle and through the next turn, which takes it at its end, so messages hold until then.
- `--instant` holds on `waiting` because Esc would cancel the prompt, and the parser cannot yet tell which prompt it is; on some, cancelling may exit the CLI. Once the later step that reads the prompt lands, permission prompts could be cancelled.
- `InTurn` tells a turn from background work, for the `inboxReader` only. The public `activity` stays as it is.
- The interrupt key is pressed once per message: pressed again once the turn has stopped, Esc does something else. A turn that does not stop holds the message until it ends by itself.
- A second `--instant` interrupts the turn the first started. General messages wait behind instant ones.
- **Steering** (the wdx docs CODEX_STEER, CLAUDE_STEER, ANTIGRAVITY_PARITY and CURSOR_PARITY): an ACA that can take a message into its running turn, `aca.CanSteer`, has the holder press its `SendNow` after an instant message, and an instant message goes in so, never with the interrupt: on `working` or `idle` it steers, and it holds where any message holds (a draft, `waiting`, no input box, `unknown`). So the table's "presses the interrupt key" is only for the ACAs that cannot. The holder logs `message steered`; the daemon hears of it as of any message delivered.
  - **Codex:** its `Submit` is Enter, with no `SendNow`: the paste then Enter, which Codex takes into the turn while it thinks, runs a tool or streams, ahead of messages queued with Tab, and submits when idle (checked on 0.163.0). For Codex an instant message differs only in going first in wdx's inbox.
  - **Antigravity:** an instant message is typed as any other: the paste, then, once it shows, alt+m as one write, as an Esc read alone would interrupt the turn. With `queuedMessages: queue` pinned, alt+m sends the message to the running turn, shown as `▸ <text>` and `⎿ Sending to agent...`, and Antigravity takes it at the next step: after the running tool returns, which it does not stop, or after the model's answer when no tool runs; at rest alt+m submits as Enter. So a long tool delays an instant message, and a command running long enough may be moved to a background task and the message taken at once (checked on 1.5.2 with `sleep 8`, `sleep 16` and `sleep 30`). A plain message differs only in waiting behind instant ones in the inbox; the inbox never presses Esc.
  - **Cursor:** an instant message is typed as any other: the paste, then, once it shows, Enter, which queues the message as the newest follow-up during a turn, then 150 ms and Enter again, both in its `Submit`, which steers the **oldest** follow-up, with the `steering: true` the agent's own config folder pins (the wdx doc CURSOR_PARITY, decisions 3 and 5). During a tool the model gets it at once and the running command goes on as a background task; while Cursor streams it waits, marked `· steering`, until the next tool call or the stream's end; at rest the first Enter submits and the second does nothing (checked on 2026.11.05). So with general messages already queued in Cursor, they are steered first and the instant one follows, still before the turn ends: accepted by the human. A message that is exactly a command, as `/clear`, runs as the command (decision 7).
  - **Claude Code:** an instant message is typed as any other (the paste, then, once it shows, Enter, which queues the message during a turn and submits it at rest), then, once the box is confirmed empty, 150 ms more and its `SendNow`, Ctrl+X Ctrl+S, Claude's "send now" for what is queued (checked on 2.1.319). Sent in the same write as Enter, Ctrl+X Ctrl+S is lost. While Claude runs a foreground command, the command is moved to the background, not stopped, and the message reaches the model at once; the turn may then end with `1 shell still running` and go on in a new one when the command finishes. While it streams an answer, the answer is cut, its text kept. While it thinks, the thinking is dropped and the request sent again with the message. One press sends every queued message, in order, so an instant message moves forward the general ones queued earlier in the turn, ahead of it. With nothing queued (at rest, or in a turn after Claude has taken the message itself) the press does nothing. General messages are typed as before.
- **`draft` comes before the table.** While there is text in the input box, the `inboxReader` holds, whatever the activity and with `--instant` too, so a message is never submitted along with what a human is typing. `watchScreen` reads `draft` true or false along with the activity and tells the `inboxReader`, and `activity` stays `idle` with a draft in the box (docs/AGENT_STATE.md).
- A draft left in the box holds messages for as long as it stays, and they queue up behind it, with no timeout (decided by the human). The queue length on `agent get` shows it.
- Text nobody typed is not a draft: Claude Code fills the box with dim placeholders and suggestions, which read the same in plain text. Reading this correctly is the parser's; read wrong, every message holds. A message pasted over a suggestion replaces it.
- Claude Code's bash mode counts as a draft even with only its `!` (decided by the human), since a message there would run as a shell command. It holds until the human leaves bash mode.
- **A message starting with `!` is given to the model as `\!`**, on every ACA (decided by the human on 2026-11-07; the wdx doc LEADING_BANG). Claude Code, Codex and Antigravity run a box whose text starts with `!` as a shell command themselves, without the agent's approval prompt, so the model never read such a message and whoever sent it ran a command. When only whitespace comes before the first `!`, `escapeBang` (`internal/aca/bang.go`) puts a `\` just before it as the message is typed, after the paste-end marker is taken out, in `Paste`; a sender's own `\!` gets a second `\`, so the model reads theirs. The inbox and `message list` show the message as sent, and a message to a plain terminal (`terminal send`) is typed into a shell as it is.
- The `inboxReader` checks `draft` before the paste, not after: the box holds the message until its Enter. A human who starts typing in the moment between the check and the paste still merges with it.

## Logging

Prompt text is never logged (AGENTS.md), but today the CLI logs raw args and every flag, and the daemon logs request bodies. The arguments and flags record `<n bytes>` in its place and the request body `prompt_bytes`, and tests check that the text appears in no log.

## Protocol

`frameSend`, 13, goes from the daemon to the holder, with `instant` in its payload. 12 is `frameStatus`, the state channel from the holder to the daemon (docs/AGENT_STATE.md), which will carry the queue's length too.

`frameAnswer`, 15, and `frameAnswered`, 16, answer an agent's prompt by keys, and take no version either: a holder says `answers` in its hello (docs/PROMPT_ANSWER.md). The inbox holds its messages while an answer is under way, and is woken when it ends.

Version 3 is the inbox, for shells: an older holder would drop `frameSend` silently, so the daemon asks for the version that has it. Version 4 delivers to an ACA and knows `instant`: a version 3 holder would type the message into an ACA with no look at its screen, so `agent send` asks for 4 and gets `send_failed` from an older one. So nothing here needs another acceptance release, but the next version, 5, does; the state channel needs no new version.

Today's daemon accepts only versions 1 and 2, so rolling back to it would lose every newer holder. As for version 2 (docs/ATTACH.md), the daemon's acceptance ships first, in a release of its own that is installed before either feature is released.

## Plan

Each step passes `make test` on its own and works in the worktree `send`. Nothing here breaks a user, so no `!`.

1. **The daemon accepts holder versions 3 and 4.** `drives`, in its own commit. Activity needs it too.
2. **Inbox and `terminal send`.** `frameSend`, the holder's inbox, its `inboxReader` and delivering to a shell, `holder.Conn`, the API, the daemon, the CLI and the logging rule. Independent of activity, so it can land first.
3. **`agent send`.** `AiCodingAgent`'s `Deliver`, the delivery table, Claude Code's paste and Enter. Built with step 4, in one commit.
4. **`--instant`.** The instant queue, `InTurn` and the interrupt key. Built.
5. **Docs and the real thing.** Done; see below. A narrow pane was not checked, as a footer too cut off to say only costs the interrupt.
6. **The queue length on `agent get`.** Built, in `queued`, on the state channel to the daemon. Only the agent has one: a shell's holder reads no screen and sends no status.

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

So "`instantQueue` is always emptied first" holds in the inbox only: what is already queued inside Claude Code runs before an instant message. That is still so now that an instant message steers Claude, as Ctrl+X Ctrl+S sends the queue in order; but it no longer waits for the turn to end.

The instant rows are out of date since Claude Code is steered: they were checked before Steer existed. What steering does on 2.1.319 was checked by hand in tmux, in the wdx doc CLAUDE_STEER.

## Later

A delivered status for scripts, `--prompt-file` (`--prompt` shows in `ps`), reading an agent's reply, and the stored per-agent inbox of V1_FEATURES.md, which could feed this one.
