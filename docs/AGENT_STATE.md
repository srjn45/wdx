# Agent activity

Status: built. `ScreenParser` and `ClaudeCodeScreenParser` read the screen, the holder's `watchScreen` keeps the latest `ScreenState` on a terminal running an ACA, and the holder tells the daemon, which shows it in the API, `wdx agent list` and `get`, and tide (see Syncing to the daemon). `wdx agent send` reads it in the holder (docs/SEND.md).

What an agent's ACA is doing right now. It is not the terminal's `state`, which stays whether the process is alive (docs/AGENT.md). Later features build on it: auto-approve, switching ACA or resuming at a limit, autopilot. None of those is in this step.

The first step answered only "is it working, idle or waiting?", from the bottom of the screen; the prompt, why it is waiting, is read for Claude Code since (see Why it is waiting).

## The model

`activity` is a new field of an agent, beside `state`, and `draft` says whether a human left text in its input box. Both are null unless the process is running.

| `activity` | Meaning |
|---|---|
| `working` | in a turn |
| `idle` | at its input box |
| `waiting` | a prompt is open |
| `unknown` | the parser does not recognize the screen, or the holder is too old to tell; never acted on |

## How Claude Code is read

From the recordings (2.1.314), looking only at the bottom of the screen. The footer is the line just below the input box (`❯` between two rules). It is often not the last line: while a subagent runs in the background, a panel of `● main` and `◯ general-purpose ...` rows sits below it. In this order:

| Bottom of the screen | `activity` |
|---|---|
| last two lines, joined, have `Esc to cancel` | `waiting` |
| no input box and no rule in the last three lines, which have `ctrl+g to edit in` (plan approval) | `waiting` |
| footer has `esc to interrupt` | `working` |
| footer has `1 shell` (any number) or `↓ to manage`, or a `◯` row is below it | `working`: the turn ended, but a background shell or subagent still runs |
| footer has `? for shortcuts`, `← 1 agent` or `← for agents` | `idle` |
| an input box with any other footer | `unknown` with `InputBox`, decided by the motion fallback |
| anything else | `unknown` |

A box's prompt line starts with `❯` and a no-break space, or `!` in bash mode. A prompt's option, as `❯ 1. Red`, has a plain space after `❯`, in every recording, so it is not a box, while a draft reading `1. buy milk` is. If Claude changes that spacing, this check breaks.

The match is case-sensitive: `esc to interrupt` and `Esc to cancel` differ only in what follows.

Only the bottom counts: Claude's replies contain "Do you want", and `❯` is on every past message, so a match on the whole screen is wrong.

Words in the footer that do not mean background work is running: `← 1 agent` is on every screen, with nothing running, and `/tasks to see subagents` stays after the subagents have finished.

An agent that leaves something running in the background stays `working` for as long as it runs, as the human asked. So one that started a dev server and finished its turn shows `working`, not `idle`, until that server ends.

## Step 1 findings

All on 2.1.314 in tmux, with the recordings in `internal/aca/testdata/claude_code/2.1.314/`. The first table above was written before these and has since been corrected; this is what the recordings found wrong with it.

Where the table is wrong:

- **Footers are cut off with `…`, and then read `idle`.** `⏵⏵ auto mode on (shift+tab to cycle) · /tasks to see subagents · esc to int…` during a turn at 80 columns, in the human's default mode, once a subagent has run. `⏸ manual mode on · ? for shortcuts · ← 1 agent · ↓ to m…` with a subagent running at 60. `⏸ manual mode on · ? for shortcuts …` at 40.
- **Typing during a turn reads `idle`, at any width.** The footer shrinks to the bare mode, `⏵⏵ auto mode on (shift+tab to cycle)`, with `❯ abc` in the box and the spinner above it. In a replay that is a false working, idle, working.
- **Plan approval has no `Esc to cancel`, so it reads `unknown`.** Its last line is `ctrl+g to edit in VS Code · ~/.claude/plans/….md`, which wraps at 60.
- **At 40 columns a decision prompt reads `idle`:** its footer wraps to `... Esc` / `to cancel`, and `❯ 1. Red` between two rules looks like an input box.

What held: a prompt replaces both the box and the subagent panel, so `Esc to cancel` stays on the last line with a shell and a subagent running. `2 shells` is the plural. The false-match cases hold (`← 1 agent`, `/tasks to see subagents` afterwards, `❯` on past messages, "Do you want" in a reply).

The table now under How Claude Code is read comes from these. Tried on all the recordings, it gave no wrong activity, only `unknown` where the footer says too little:

| Bottom of the screen | `activity` |
|---|---|
| last two lines, joined, have `Esc to cancel`; or last three have `ctrl+g to edit in` | `waiting` |
| footer has `esc to interrupt` | `working` |
| footer has `1 shell` (any number) or `↓ to manage`; or a `◯` row below the footer | `working` |
| footer has `? for shortcuts` or `← 1 agent` / `← for agents` | `idle` |
| anything else: a cut-off footer with none of these, or the bare mode while typing | `unknown` |

The human approved this table, and it is in the parser. One tightening beyond it: the prompt check ignores anything above the last rule, or a draft containing `ctrl+g to edit in` would read `waiting`.

### The motion fallback

Chosen by the human on 2026-10-31 for unreadable footers only, and tested on timed recordings. With the table alone, a running turn reads `unknown` in any pane narrow enough to cut the footer. What the table decides is final, so the Bash prompt's blinking `●` does not matter. Otherwise:

| The screen is... | `activity` |
|---|---|
| moving | `working` |
| still, with an input box | `idle` |
| still, with no input box | `unknown` |

- Moving means the rendered text differs, not that bytes arrived.
- A change within an echo of 150 ms after input is the user's own, not motion. Half of inputs change the screen within 57 ms; 60 ms gives false `working`, 300 ms delays turn starts.
- Input holds the last answer for the still time, since keystrokes hide the screen's own motion.
- `watchScreen` owns this, generic and with no Claude knowledge. `Parse` stays pure: `Activity: unknown` with `InputBox: true` says "an input box whose footer does not say", and it never leaves the holder. The fallback is `screen.Motion`, which has no clock of its own, so the tests run it on the recordings' times and the holder on real ones.
- The still time is 3 s, which is what the tests use.

The results, against true turn times taken from each session's own transcript (9 sessions at normal settings, one machine, Haiku, 2.1.314, under tmux, not a real holder):

| Still time | Mid-turn false `idle`, normal | Same, reduced motion | False `working` flashes, normal |
|---|---|---|---|
| 0.5 s | 1 / 0.1 s | 31 / 17.7 s | 16 / 7.6 s |
| 1 s | 0 | 10 / 7.0 s | 15 / 12.1 s |
| 2 s | 0 | 2 / 1.6 s | 16 / 20.5 s |
| 3 s | 0 | 1 / 0.4 s | 15 / 18.6 s |
| 4 s | 0 | 0 | 14 / 19.2 s |

The longest still gap in a turn is 1.12 s at normal settings, and 3.36 s with `prefersReducedMotion`, where the spinner stops animating. A turn start shows within about 0.5 s. A turn's end on an unreadable footer shows about the still time later.

Known wrong answers, all on unreadable footers:

- Anything that moves on an idle screen shows `working` for up to the still time: a hint clearing, as `Ctrl+Y`, the effort hint or `/tasks` under a draft. 15 episodes in about 22 minutes of recording.
- At 40 columns, a prompt suggestion filling the box after a turn kept `working` until 44.2 s in one recording, and 7.3 s past the turn's end in another.
- A stall past the still time during a turn reads `idle`.
- Typing within the still time of any screen change reads `working` until it is up, as right after startup. A change more than the echo after input counts as motion, as `Press Ctrl-C again to exit` going away. Neither is a false `idle`.

Not shown by the recordings: a resize or attach redraw moving the screen, a stall longer than recorded, a forced slow API wait, a loaded machine. A real holder: see Step 2 findings. The recordings do not prove two rules, which only unit tests fail on: bytes are not motion, and input holds the answer. In fullscreen mode Claude turns on any-motion mouse tracking; a viewer's mouse movement does not count as input (see Step 2 decisions).

The true turn times are frozen as data in the test; transcripts are not kept.

### What `wdx agent send` needs

`wdx agent send` delivers on `idle` (docs/SEND.md), so the human asked that no open prompt ever reads `idle`. A throwaway check on all 24 recordings, at still times of 3 s and 4 s: at each of the 127 moments whose last three lines show a prompt's footer, the parser read `waiting`, and none read `idle`. That check uses the same footer words as the parser, so it cannot see a prompt whose footer is not recognized. Such a prompt without an input box reads `unknown`, which holds. One with something like a box would read `idle`: at 40 columns a decision's `❯ 1. Red` between two rules already looks like one, and only its footer saves it. At narrower widths, or for plan approval at 40 and prompts not recorded, that is untested. A box whose line is a numbered option does not count as a box, since step 1.

**Conflict with docs/SEND.md, to settle when `wdx agent send` is discussed.** It says an ACA's text in its box reads `unknown`, so a message is not submitted along with what a human is typing. With the fallback, a still draft reads `idle`, so `agent send` would deliver into the draft. Agreed with the human: the public `activity` stays `idle`, and the parser also reports text in the box, whatever the activity, since a human can type during a turn too. `watchScreen` reads it along with the activity, and the sender holds while it is there. docs/SEND.md already plans for the parser to tell the sender more than the public `activity`.

It is called `draft`, since the text stays after the typing stops. A draft left in the box holds messages for as long as it stays, and they queue up behind it, with no timeout (decided by the human). Reading it needs care, as Claude fills the box with text nobody typed, a placeholder (`❯ Try "create a util..."`) or a suggestion (`❯ show the ping output`). In plain text they look like a draft. In the raw output they are dim (`\x1b[2m`) and typed text is not, and with typed text the footer shrinks (`⏵⏵ auto mode on · 1 shell`, against `... · ← 1 agent · ↓ to manage` with a suggestion). 

Tested on 8 scripted sessions at 120, 60 and 40 columns in both renderers, in `--safe-mode`, under tmux. `ScreenParser.Parse(lines, undimmed []string)` returns `ScreenState{Activity, InputBox, Draft}`, where `undimmed` comes from `Screen.Undimmed()`: the same rows as `Screen.Text()` with dim cells blank. The answer was right for an empty box, a placeholder, a suggestion, text typed at idle, during a turn and with a shell or subagent running, text typed then cleared, typing over a suggestion, accepting one with Tab or Right, a multi-line draft, a paste, a slash command with its menu open, bash mode, a prompt opening over a draft and closing again, a wrapped draft, a past `❯` above, and a draft with dim-looking words. A draft only ever appeared within 800 ms of input.

Known failures, kept as such in the tests:

- **No color, with the footer cut off.** With `NO_COLOR=1` or `FORCE_COLOR=0` Claude sends no dim, so only the footer veto below can tell its own text from a draft. Where the footer is cut before the veto's words, its placeholder or suggestion reads as a draft: at 40 columns in auto mode (`⏵⏵ auto mode on (shift+tab to cycle)`), and at 30 columns in manual mode (`⏸ manual mode on · ? for …`). With `Activity: unknown` and `InputBox: true`, so the fallback can call the agent `idle` while a message is held. At 120 columns, and 40 in manual mode, it reads right. `NO_COLOR` is not in the holder's inherited variables, but a user's rc file could set it, as Claude starts through their login shell. Untested under a holder.
- **A draft taller than the screen, classic renderer, 40x24.** The box's `❯` and top rule scroll away, so it reads `unknown` with no box and no draft. `unknown` also holds a message, so this fails safe.

**The footer veto, decided by the human.** When the box's footer matches `? for shortcuts`, `← <n> ag` or `← for agents`, `Draft` is false whatever the styling says, as Claude drops those words while the box holds text. No recording has text that is not dim above one of them, footer and text change in the same chunk, and a test keeps checking it on the color recordings. A bare footer says nothing the other way: many moments have one with no draft.

In bash mode the box has a draft even with only `!`, decided by the human for `wdx agent send`: a message delivered there would run as a shell command.

**The draft's text** (the wdx doc SEND_CONFIRMED). `ScreenState.DraftText` is the box's text as shown, `""` when `Draft` is false: each line trimmed and the lines joined with `\n`, without the box's prompt mark (`❯`, `›`, `>`, `→`) and without what the ACA dims there itself. Claude Code, Codex and Antigravity read it from the box's undimmed lines, Cursor from the undimmed lines of its box block, as each reads `Draft`, through one helper, `draftText` (`internal/aca/drafttext.go`). A mode's mark stays, as in `! ls`, since it is part of what the box would run; blank lines at either end are left out, one inside stays. A long paste reads as the ACA renders it, as Claude Code's `[Pasted text #1 +58 lines]`. The holder only compares it, what the screen showed just after its paste with what it shows now, to know whether to press the submit key again (docs/SEND.md, Typing); nothing parses it. `Draft` itself is unchanged. As `ScreenState.Equal` sees the text, the holder logs `screen state changed`, never with the text, each time it changes, as a person types.

Not recorded: vim mode, `#` and other input modes, image pastes, a running turn in bash mode, a real holder.

A read and parse costs about 340 µs and 25 KB at 120x40 on a machine kept busy, a third of the 1 ms it cost under the same load before `Screen.lines` stopped copying each cell, and most of it under the holder's screen lock.

Other facts:

- **Two renderers.** The human's `"tui": "fullscreen"` setting puts Claude on the alternate screen from startup to exit, so `Screen.Lines()` would return nil for the whole session. `"tui": "default"` never enters it, its footer shares a row with `◐ medium · /effort`, and notices can sit below it. Parsers receive `Screen.Text()`, plain text of whichever screen shows, and `Screen.Undimmed()`, the same rows with dim cells blank.
- **The moment is 50 ms.** Below it a blank screen at startup, 18-30 ms long, shows as flicker. From 100 ms real prompts can go missing, though the driver answered those unnaturally fast. Half-drawn frames appear only when a chunk is cut mid-frame, never across a recorded chunk boundary. Not measured on a loaded machine.
- **Parsing costs nothing that matters,** about 0.1% of a core at the 1.4-7.3 reads a second seen while working.
- **Recording without private content.** `--safe-mode` keeps the login and drops CLAUDE.md, settings and plugins. Fixtures have paths and session ids masked at the same length. The banner's plan name, `Haiku 5.5 · Claude Max`, stays.
- **Not recorded:** a foreground subagent (Haiku backgrounded it twice), MCP input, login, a network retry, a limit, plan approval and a shell at 40 columns. Under a real holder: see Step 2 findings.

## Where it lives

The holder owns it, as it owns the truth about the process (docs/TERMINALS.md):

```
PTY ─► spool ─► vt screen ─► channel ─► watchScreen ─► log and the daemon
                                            │ calls
                  ScreenParser.Parse(lines, undimmed)
```

- The holder stays free of ACA code. Everything in it is generic, `watchScreen` included: the channel, the moment's wait, the recover, pushing a change. Only the parser is ACA-specific.
- `aca.ScreenParser` is the interface, with `Parse(lines, undimmed)` giving a `ScreenState`; `aca.ClaudeCodeScreenParser` is Claude Code's, and `CodexScreenParser` and others come with those ACAs. Parsers are pure functions of the screen's lines. `AiCodingAgent` gets a `ScreenParser()` method, so an ACA cannot compile without one, and `aca.ByID(id)` is the factory.
- The holder is its own process, so the daemon cannot hand it a parser: the daemon passes `--aca <id>`, and the hidden `wdx holder` command in `internal/cli/holder.go` calls `aca.ByID` and gives the holder just a function. So `cli -> aca` joins the dependency directions, not `holder -> aca`. A shell terminal has no parser and no goroutine.
- `agent.Launches` keeps its list of ACAs, through which tests hand in their own; it is not moved onto `aca.ByID`.
- `watchScreen` runs apart from the vt, told over a channel of capacity 1 each time the screen changes. The vt's send never blocks: when a signal is already pending it is enough, since the goroutine reads the latest lines when it wakes. It never holds up the PTY or the vt.
- After a signal it waits a moment before reading, so that it reads a finished redraw: Claude does not use synchronized output, so half-drawn screens do reach the vt. This limits the rate rather than waiting for quiet, so a screen that never stops is still read. The moment is a field tests can shorten; its value comes from replaying the recordings.
- Nothing polls: one timer, armed only for a read that is due or while the answer depends on the still time.
- A panic in a parser is recovered and gives `unknown`, logged once and without its value, which could hold the screen's text: the holder owns the agent's PTY, and a parser must never take it down. A moving screen stays `unknown` then, not `working`, as nothing can say what it shows. A hung parser only delays the state, and the holder quits without it.
- Changes are logged at info as their values (`activity`, `input_box`, `draft`), at debug what the parser itself read, and a counted input with no content. The screen's text never leaves the holder.

A parser fixed in a wdx release reaches a running agent only when it is relaunched, since a holder keeps its binary. That matches the ACA: a running ACA keeps its wording until relaunched too, and the relaunch starts a holder of the current wdx.

### Motion, for other ACAs

Another way to read an ACA: ignore the screen while output keeps coming, and identify the activity only once it stops. It is the simple choice for an ACA whose last line does not say enough, so it stays an option when adding Codex, Cursor or Antigravity, chosen per ACA. Not built in this step.

An ACA with no parser of its own yet returns `aca.NoScreenParser`, which reads only an input box: `unknown` with `InputBox` on every screen, so a moving screen reads `working`, a still one `idle`, and the inbox delivers on `idle`. Draft, turn and model are never seen. It delivers to any still screen, a login prompt among them, so each ACA checks its startup on recordings before it drops the stub (the wdx doc ACA_SELECT).

Codex has `aca.CodexScreenParser` (the wdx docs ACA_READY and CODEX_ACTIVITY). Codex draws its box while it still loads, and a message submitted then was lost (its Tab dropped, when Deliver pressed Tab), so the box reads as an input box only once the status line (`GPT-6-Sol medium · <dir>`) shows under it. It reads a draft from the box's text that is not dim, and the model from the status line. The activity comes from the block of lines just above the box, past the blocks of messages waiting (`• Queued follow-up inputs`, and `• Messages to be submitted after next tool call` for one steered into the turn and not yet taken in): `• Working (5s • esc to interrupt)` in a turn, `1 background terminal running` while a command outlives the turn (working, not in a turn, as Claude Code's background shells), `Worked for 3s` or `■ Conversation interrupted` once it ended, and an approval's footer `Press enter to confirm or esc to cancel` for waiting. While Codex streams an answer nothing says it works, so the activity is `unknown` and the motion fallback decides: a streaming screen moves. Its recordings are in `internal/aca/testdata/codex/0.163.0/`, made by `testdata/codex/record/ready.py` and `turns.py`.

Antigravity has `aca.AntigravityScreenParser`, checked on 1.5.2 (the wdx doc ANTIGRAVITY_PARITY). From the bottom up its screen is the user's own status line, if they have one (the wdx doc ANTIGRAVITY_CONTEXT: wdx runs theirs with its own, and Antigravity draws what it prints under its footer), the footer, a full-width `─` rule, a panel only while background work runs, the box (`>`, a draft's further lines indented under it), another rule, and above it the spinner and messages waiting. The box is the last `>` line whose lines end at a full rule, which leaves out earlier messages (under short rules), approvals and menu entries. The footer is the first line under that rule, past the panel's rows and its rule, and past the `/` menu's hint; what is under the footer is not read. It reads as an input box only once the footer's dim right part names a model, about 1.5 s after start: a message delivered before the box is drawn is lost. The model is that part up to its first ` · ` (`Gemini 3.8 Flash · low` gives `Gemini 3.8 Flash`). A draft is any text in the box, the `/` of the slash menu included, so the inbox holds while a menu is open. The activity:

- **working, in a turn:** the footer's left part is `esc to cancel` or `Press up to edit queued messages`, or a braille spinner (`⣷  Generating...`) starts a line in the block just above the box, so a draft typed in a turn, which empties the footer, still reads in a turn;
- **working, not in a turn:** a `● ` row in the panel below the box (a background task or a subagent), or `· N task(s)` or `· N subagent(s)` on the footer's right; Antigravity starts a turn by itself when one ends;
- **idle:** `? for shortcuts` with neither;
- **waiting:** no box, and `↑/↓ Navigate` among the last three lines, so a user's status line under the footer does not hide it: an approval, the trust prompt, `/model`;
- **unknown**, for the motion fallback: the `/` menu, a draft at rest, any other footer.

The activity is read even before the model shows. At 40 columns the footer's right part is cut, and while a message is queued or pending it loses the model, so the box is not ready and the inbox holds; the panel still shows background work. Its recordings are in `internal/aca/testdata/antigravity/1.5.2/`, made by the scripts in `testdata/antigravity/record/`; `status-120x40` and `status-user-120x40` were made with wdx's status line on, the second with a user's line under the footer.

Cursor has `aca.CursorScreenParser` (the wdx doc CURSOR_PARITY), checked on cursor-agent 2026.11.05. The screen ends with the box (`→`, or `!` in shell mode, `&` in cloud mode) and, past two blank lines, the footer: an optional `N task(s)` line or cloud hint, the status line (`Gemini 3.8 Flash Low · 0.8%   Run Everything`) and the directory. The box is an input box only with that footer under it, so the blank screen while Cursor loads, the `/` menu and `Press Ctrl+C again to exit`, which take the footer's place, read none; an approval's options start with `→` too, which is why the box is not just the last `→` line. A draft is the box's mark not dim, the placeholder being dim but for the cursor's cell; shell and cloud mode are drafts. The model is the status line before ` · `, as `Models` names it; before the first turn the line has no ` · `, and at 40 columns it wraps, so the last name read stays. The activity: `ctrl+c to stop` at the end of the box's line, or a spinner line (two braille cells, then `Running`, `Working`, `Exploring`…) just above the box, past a tip line, is `working` in a turn, a streamed answer included, unlike Codex; a draft in a turn takes the hint off the box's line, but the spinner stays. `N task(s)` under the box is `working`, not in a turn: a command the turn left running. An approval (`Run this command?`, `Skip & tell the agent what to do instead`) or the box Esc on it gives is `waiting`. Anything else is `idle`, read from what is missing, which Codex's parser never does: a Cursor that changes how a turn shows would read `idle` in one, so the recordings check that no read inside a turn says `idle`. It also reads `SteerPending`, a field of `ScreenState` every parser may set and the holder alone reads (it is not in `Status`): the panel of follow-ups (`┌─ follow-ups`), the block just above the box or above the spinner's block, marks one `· steering`, wrapped onto a line of its own on a narrow screen. A follow-up left `· steering` after a human's Ctrl+C stays marked until a turn takes it. The holder's state-change log lines carry `steer_pending`. A directory too long for the screen wraps onto lines of its own, which do not start with a path (seen with a long scratch path): the directory is the first line after the status line that starts with `/` or `~`, so the status line is one line or, wrapped, two, and the box is read however many lines the directory takes. Before 2026-11-06 a wrapped directory left the model unread, and one wrapped twice read no box, so a message was held for good. Cursor draws the box a little before it reads keys, which the screen does not show, so the holder also asks the PTY (docs/SEND.md, not reading keys). Its recordings are in `internal/aca/testdata/cursor/2026.11.05/`, made by `testdata/cursor/record/ready.py` and `turns.py`.

Claude Code uses it only as the fallback for unreadable footers (see The motion fallback): as the main rule it would misread the Bash prompt, whose `●` blinks every 0.6 s for as long as it is open. An ACA that uses it needs the same check first: a recording of whether anything keeps moving at its prompts and at idle.

### Syncing to the daemon

Decided by the human on 2026-10-31 and 2026-11-01.

```
watchScreen ─► holder ─► frameStatus ─► Conn ─► Supervisor ─► agents row ─► notify ─► project stream ─► tide
```

More attributes will come from watching a terminal, so instead of an event type for each, the holder sends one complete object over its existing connection on every change, as the project stream does for tide: `frameStatus`, frame 12, carrying a `Status`. The name avoids `state`, which means whether the process is alive, and `ScreenState`, as later fields will not come from the screen.

```json
{"activity":"waiting","draft":false,"queued":0,"model":"Haiku 5.5"}
```

`model` is left out until the program has shown one (see Which AI model it runs).

`Hello` carries the same object, so a daemon that restarts learns it on connecting. `input_box` and `in_turn` stay in the holder, for the fallback and the `inboxReader`, so a change only to them sends nothing. Only the daemon's connection gets the frame, not attached clients.

The daemon keeps it in the database, not memory, so `agent.Store` and the project stream work as they do today:

```sql
ALTER TABLE agents ADD COLUMN activity TEXT;
ALTER TABLE agents ADD COLUMN draft INTEGER;
```

- **Columns in `agents`,** decided by the human on 2026-11-01, over one JSON column of `Status` and over a table of their own. These are facts wdx's own logic will build on, so they keep a typed place, and adding one is a line that only adds. A table of their own gains nothing from how often they change: SQLite has one writer for the whole database, none of them is indexed, and the row is small. What a change costs is every open stream reading its project again, which neither would change. Revisit when a value is large or nested, as a prompt's text and options, or when history is wanted: those go in a table of their own, leaving these here.
- **Additive,** so `wdx update --rollback` keeps the data. An older daemon skips the frame, as its reader takes only `exited`.
- **No `CHECK` on `activity`.** Adding `limited` would rebuild the table, and a running holder can be newer than its daemon. The daemon turns a value it does not know into `unknown`.
- **Never stale.** The agents query returns both only when the terminal's state is `running`, so exit, stop and lost need no code of their own. On a launch, and on adopting a terminal still `starting`, what `Hello` says is written before the terminal is marked `running`. Each dial writes it: the launch, `Reconcile` and the redial in `follow`.
- **`updated_at` is untouched.** It says when `state` last changed.
- **`draft` is public:** in the API and `wdx agent get --json`, true or false, never the text. The holder's sender goes on reading it locally.
- **No time of the last change for now.** "Waiting for 12 minutes" or finding a stuck agent would need one, added then, set by the holder so that a daemon restart does not reset it.
- **A holder too old to send status** shows `unknown` until relaunched. A shell terminal has no status.
- **Nothing blocks the terminal.** The holder writes to the daemon under `h.mu`, which a stalled daemon could hold for the whole write timeout, so a goroutine of its own sends the latest status, not `watchScreen`. It has its own wake, as the `inboxReader` takes `published`. `Conn` keeps only the latest, so its reader never waits on the database.
- **No change is missed.** The holder updates the latest status before taking `h.mu` to write, so a change during a connect goes out in `Hello`, the next frame or both. A repeat is harmless.
- **`terminal` still knows nothing of agents.** The Supervisor gets a function in its options, as with `AgentLaunch`, which the daemon connects to `agent.Store` and then to the streams' notice.
- **No new protocol version,** decided by the human. The version stays 4, as the daemon asks nothing new of the holder: a holder with no status in its `Hello` is too old to send one, and the released daemon skips the frame and the field. Version 5 would need a release of its own first, since `drives` stops at 4 and a rollback would lose every newer holder.
- **The queue's length** is `queued`, a field of `Status` and a column, told when a message comes and when it goes (docs/SEND.md, step 6). `wdx agent get` shows it in text only when it is not 0.

Checked on real Claude Code 2.1.316 on 2026-11-01, through a real daemon in a throwaway home, against `wdx agent list` and `get`:

- At a trust prompt it read `waiting`, and still did after the daemon was stopped and started again. Removing the agent then showed no status.
- One turn of 7 s, a `ping` and a one-word reply, went `unknown`, `idle`, `working` with `draft` true for 105 ms while `agent send` pasted the message, `working`, `idle`: 5 writes, so a few a turn, and each makes every open stream read its whole project again. The stream's cost itself is not measured.
- Not driven through the daemon: a permission, a decision, Esc, a narrow tide pane. The parser's answers for them under a real holder are in Step 2 findings, and the row in a narrow pane is in `TestAgentRowActivity`.

## What the user sees

```
$ wdx agent list
NAME          STATE    ACTIVITY  MODEL      ACA          DIR               ID
calm-otter    running  waiting   Haiku 5.5  claude_code  /home/me/dev/wdx  agent-5e6f7a8b
swift-falcon  running  working   Opus 5.5   claude_code  /home/me/dev/wdx  agent-1a2b3c4d

$ wdx agent get --name calm-otter --json
{"agent": {"name": "calm-otter", "state": "running", "activity": "waiting", "draft": false, ...}}
```

`draft` shows in `wdx agent get`'s text only when true, and not in the list.

The project stream carries both, so tide's Agents list shows activity live, decided by the human:

| `activity` | The row |
|---|---|
| `waiting` | a state mark of its own, which a narrow pane keeps, and the word |
| `working` | the word, muted |
| `idle`, `unknown` | the running mark alone |

A folded parent says `1 waiting` in place of `2 below` while an agent it hides is waiting, so that one cannot go unnoticed.

Known limits at the end of this step:

- An agent at a token limit probably shows `idle`, since its turn ended and its input box is back. Not recorded yet, and `limited` is the next step's.
- Claude rewording its footer gives `unknown`, not a wrong activity, until a wdx release fixes the parser.

## Step 2 decisions

Decided by the human on 2026-10-31. The plan's step 2 below is narrowed to this.

- **The holder reads the screen and keeps the latest `ScreenState`. It tells nobody yet.** Each change is logged, values only, never the screen's text. No frame and no protocol change, so it cannot clash with docs/SEND.md's frames.
- **Syncing state to the daemon came after this step;** see Syncing to the daemon.
- **Mouse movement is not input.** Claude Code's fullscreen mode gets every mouse move, so counting them would freeze the answer whenever a mouse crosses a pane.
- **What counts as input** to the fallback, decided in the holder from each raw input frame (`isInput`, `internal/holder/watch.go`): anything but a frame made only of mouse movement, in the SGR or X10 encoding. Bit 32 of a report's button code is motion, with or without a button held, so a drag is movement too, and bit 64 a wheel. A frame with a key among the moves is input. So is a resize. The vt's answers to queries (`h.answer`) go to the PTY without passing it. An attached client's terminal answers instead, in input frames, and those do count, holding the answer for the still time; under a real holder Claude queried only at startup, so that costs nothing. A report cut at a frame's end counts as input too, the safe side.
- **Scrolling.** Where the program did not ask for the mouse, the wheel scrolls the viewer's own history (`internal/termview`), which never reaches the holder. Where it did, as fullscreen Claude Code does, the wheel is forwarded, and Claude Code redraws. A wheel or click there counts as input, so it holds the answer instead of reading as motion. A readable footer is unaffected either way.

## Step 2 findings

On a real `wdx holder`, with no daemon, in a throwaway home, on 2026-10-31: 8 sessions at 120x40, 60x24 and 40x24, each 80x24 until its first attach as any terminal is, in `--safe-mode` with Haiku, some at the same time; `record/holdercheck` drove them, and the states are the holder's own log. Nothing was attached at startup, so the holder's vt answered what Claude asked.

- **Synchronized output: none,** on 2.1.314 or 2.1.316. No `\x1b[?2026h` or `$p` in any session's whole output. Claude asked for device attributes and the version (`\x1b[c`, `\x1b[>0q`), which the vt answered, and the kitty keyboard flags (`\x1b[?u`), which it did not until docs/KITTY_KEYS.md, and only at startup. So half-drawn screens can still reach the vt and the moment is still needed. **The moment of 50 ms holds:** every logged read made sense, with no false reading between two real ones. That is the only evidence: the output under a holder has no chunk times, so it was not swept again as it was under tmux, and the load was at most three sessions at once.
- **The parser read every state right,** on both versions: trust, idle, working, a Bash permission (`Esc to cancel · Tab to amend`), a file permission, a radio decision, Esc at each back to idle, Esc in a turn, a draft at idle and in a turn, and `draft` going again on Ctrl-U. Not reached: Esc at trust, which quits Claude; plan approval, a checkbox and background work, which were not driven this time. The narrow sessions (`esc to interrupt…` at 40 columns) still read `working`.
- **2.1.316 against 2.1.314: the same footers,** all six of those seen: `⏸ manual mode on`, `... · esc to interrupt · ← 1 agent`, `... · ? for shortcuts · ← 1 agent`, `Enter to confirm · Esc to cancel`, `Enter to select · ↑/↓ to navigate · Esc to cancel`, `Esc to cancel · Tab to amend`. The rule table needs no change.
- **Scrolling fullscreen Claude holds up** (2.1.316, `--settings '{"tui":"fullscreen"}'`, which turns on 1003 and 1006 mouse modes, on the alternate screen). The footer and the box stayed on screen through 16 single wheels and 5 in a frame, with `Jump to bottom (ctrl+End) ↓` added above them, and the parser kept reading `idle`. Each redraw was 1 or 2 chunks of about 370 bytes, and ended 2.8 to 34.2 ms after the wheel, with three sessions running: well within the echo of 150 ms, read 50 ms later. A wheel at the bottom sends nothing. A mouse move sent nothing but the first, 44 bytes that left the text as it was.
- **A wheel holds a still draft's answer, as designed.** With a draft at idle, whose footer does not say, 2 s of mouse moves and 20 wheels 100 ms apart logged no change: it stayed `idle`.
- **Known wrong answers seen again,** as listed under The motion fallback: a key typed within the still time of any change reads `working` until it is up, and one hint clearing above a draft (`Ctrl+Y`, `/effort`) reads `working` for the still time. Both only on the unreadable footers, as a draft's is.

Found on the way:

- **A client that types and leaves at once can lose what it typed.** Input frames are read until the connection closes, but the writer fails on the same closed connection while sending the snapshot, and its `disconnect` closes the holder's side before the frame is read. In 4 of 147 attach, write, close cycles with no pause, the holder logged no input, and where it showed the keys never reached the program: an Enter, and two Escs that left a prompt open. No client does that today, as a viewer stays attached. It matters to anything that sends and leaves. Older than this step, and not changed by it.
- **`DISABLE_AUTOUPDATER=1` does not reach a terminal,** as only a fixed list of variables does (docs/TERMINALS.md). The first run of 2.1.314 updated itself, wrote `✔ Update installed · /restart to apply` beside the footer, and rewrote `~/.local/bin/claude` to the 2.1.316 it already pointed to. `record/holdercheck` now puts it in the command through `env`. A real agent runs Claude's updater like any other Claude.

## Plan

Each step builds and passes `make test` on its own, and is a `feat:` or `test:` commit. Adding a field and a column breaks nothing, so no `!`.

1. **Prove the rule, before touching the holder. Done.** The parser, tested on recordings: every recorded screen gives its activity, and replaying the raw output chunk by chunk through `internal/screen` gives the right sequence.
2. **The holder. Done:** first without what tells anyone (Step 2 decisions): `--aca`, `AiCodingAgent.ScreenParser()`, `aca.ByID`, `watchScreen`, the fallback moved out of the tests into `screen.Motion`. Tests run the real parser against a test command that prints real footers, so no real ACA is needed. The `cli -> aca` direction in AGENTS.md. Claude Code 2.1.316 re-checked. Then `frameStatus`, `Status` in `Hello` and `Conn`'s latest status, with their tests: a change is pushed, a repeat is not, a reconnect is told the current one.
3. **The daemon and API. Done.** The migration, `activity` and `draft` in `api/openapi.yaml`, `make generate`. The supervisor writes each status to the agent's row and notifies the streams. Tests: an agent's activity and draft through `GET /v1/agents`, the project stream, a daemon restart with the holder alive, a holder with no status showing `unknown`, a relaunch showing nothing of the last run, null once the process ends.
4. **The CLI. Done.** The `ACTIVITY` column and `get`'s fields, with their tests and help.
5. **Tide. Done.** The activity in the Agents list, the `waiting` mark and what a folded parent says, with their tests.
6. **Docs and the real thing. Done, apart from what Syncing to the daemon lists as not driven.** docs/AGENT.md, docs/TERMINALS.md, README, V1_FEATURES.md. Then by hand with the real Claude Code: a prompt, a permission, a decision, Esc, a trust prompt, a daemon restart while waiting, and a narrow tide pane. `make check`, `make test`, and `make test-stress PKG=` on `holder`, `terminal` and `agent`.

## Recordings

Real Claude Code 2.1.314 sessions, at widths from 30 to 120 and in both renderers, are in `internal/aca/testdata/claude_code/2.1.314/`: 29 recordings, 2.3 MB. Each holds `raw.out` and `raw.timing`, with input times where a test needs them, and some a `marks.log` of what was done when. The tests replay them through the screen in CI, which needs no real ACA.

They ran in tmux with Haiku and tiny prompts, in `--safe-mode`, which keeps the login and drops CLAUDE.md, settings and plugins, apart from `prompts`, which quotes nothing private. Paths and session ids are masked at the same length, so the layout holds; a mask can leave a character changed, as `re-exable`. Claude's `sleep` was blocked by the human's settings in an early attempt, so they use `ping`. The scripts are in `internal/aca/testdata/claude_code/record/`. The tests replay each recording at most once per test binary and share what it read, so a `-count` costs little.

| Screen | Last line | Moves? |
|---|---|---|
| working | `⏸ manual mode on · esc to interrupt · ← 1 agent` | a spinner, gaps up to 0.32 s |
| idle | `⏸ manual mode on · ? for shortcuts · ← 1 agent`; `⏸ manual mode on` while typing; also after a denial or Esc | no, apart from a rare notice going away |
| trust | `Enter to confirm · Esc to cancel` | no |
| permission | `Esc to cancel · Tab to amend`, or `Esc to cancel` | Bash: the `●` blinks every 0.6 s. File: no |
| decision | `Enter to select · ↑/↓ to navigate · Esc to cancel` | no |
| background shell, turn ended | footer `⏵⏵ auto mode on · 1 shell · ← 1 agent · ↓ to manage` | no |
| background subagent, turn ended | footer `⏵⏵ auto mode on (shift+tab to cycle) · ← 1 agent · ↓ to manage`, then the panel below it; last line `◯ general-purpose  Run ...  8s · ↓ 31.6k tokens` | yes, its timer ticks every second |

Haiku backgrounded the subagent it was told to run in the foreground, so a foreground subagent is not recorded.

With the user's default of auto mode no permission prompt appears for these commands, so which prompts come depends on the mode. Not recorded: MCP input, login, a network retry, a limit.

## Which AI model it runs

Status: built. Decided by the human on 2026-11-01: for display only for now, and a new agent shows its model right away.

- **Read from the screen.** `ScreenState` gets `Model`, the name as Claude shows it (`Haiku 5.5`), empty when the screen does not say. `Parse` stays pure. Claude's session transcript gives the exact id on each reply (`claude-haiku-5-5`), and records what really answered, but only after the next reply. Anything that comes to act on the model, as switching or cost, reads the transcript instead.
- **Remembered by the holder.** The banner scrolls away, and so does a confirmation. So `watchScreen` keeps the last name read. It reads from the process's start, and a relaunch starts a new holder, so a name never outlives its run.
- **Two places count, the lowest on the screen wins, and only while the input box shows, empty, at the bottom of the conversation:**

| Where | Looks like |
|---|---|
| a confirmation, on the line just below `❯ /model...` | `⎿  Set model to Haiku 5.5 and saved as your default for new sessions`, `... for this session only`, `Set model to Sonnet 5.5 (default) and ...`, `Kept model as Haiku 5.5` |
| the banner, on the line below the top of its logo (`▐▛███▛█`) | `▝▜██████▀  Haiku 5.5 · Claude Max`, or only `Haiku 5.5` at 30 columns |

A name anywhere else means nothing: told to end its turn with one word, Claude replied `● Haiku` (`background-auto-80x24`). `Model 'nonsense' not found` changes nothing.

The conditions are there because old names do show:

| Screen | What it shows | So |
|---|---|---|
| fullscreen, scrolled back | older confirmations lowest, with `Jump to bottom` over the conversation | that line stops the search |
| the transcript view (`ctrl+o`) | every confirmation | no input box |
| a command being typed, as `/model` | its suggestions cover the newest confirmation at a narrow width, leaving an older one lowest | a draft: not read |
| the `/model` menu | every model's name | a prompt, with no input box |

- **Its shape is checked.** The screen's text never leaves the holder, and this is the one exception, so a value must be a word and a version, as `Opus 5.5` or `Fable 5`. Anything else is not read, the last name staying. That also stops a name cut by a wrap being read as a shorter one.
- **Synced as `activity` is.** A `model` field of `Status`, so no new protocol version: a holder too old to send it shows null. Then `ALTER TABLE agents ADD COLUMN model TEXT;`, null unless the process is running, and `model` in the API and the project stream.
- **Where it shows:** `wdx agent get`, a `MODEL` column in `wdx agent list`, and tide's agent info tab (docs/TIDE_INFO.md), next to `aca` in all three, not the Agents row, which is narrow.

### What the recordings found

18 sessions on 2.1.316 under tmux, in `--safe-mode` and a `CLAUDE_CONFIG_DIR` of their own, in both renderers at 120x40 and 40x24, and the default renderer at 30x24: `record/model.py` to `model5.py`. Eight are the fixtures in `internal/aca/testdata/claude_code/2.1.316/`.

Under a real `wdx holder`, with no daemon, on 2026-11-01: `holdercheck`'s `model` scenario, in both renderers at 80x24 and 40x24. On the holder's own connection, what a daemon is told, it read the banner, `/model sonnet`, `opus[1m]`, the menu opened and cancelled, and a turn, and told no other name. An unknown name changed nothing. That check is what found the row of the table about a command being typed. Through a real daemon only with a test command printing a real banner (`TestAgentStatus`), as a real `claude` started by a daemon would have used the user's own settings.

- **The banner redraws in place** on a `/model` (the menu, an argument, session only), in both renderers, and is first drawn only once the trust prompt is answered. So a new agent shows its model after that, and null at the prompt.
- **The banner can be wrong.** After `/model opus[1m]` it stayed `Sonnet 5.5` under `Set model to Opus 5.5`, which is why the lowest wins and a confirmation is below it. That name has no `[1m]` in either place.
- **`--resume` keeps the session's model,** whatever the settings say, and shows it in the banner. Replies or none after the last `/model`, the banner, the old confirmation and `Kept model as` all agreed.
- **On a small screen a resume's history scrolls the banner off** about 0.25 s after it is drawn, in 3 to 5 chunks. The moment of 50 ms reads it first in the replay (`model-resume-40x24`), not yet under a holder.
- **Unchanged by** `/clear`, `/compact`, a turn, and the menu cancelled with Esc, apart from the `Kept model as` line.

Known wrong answers, none of which matters for display:

- One unexplained: a session that went through session-only, `default` and `opus[1m]`, ending on `/model sonnet`, resumed with the banner saying `Opus 5.5` and the old confirmation below it saying `Sonnet 5.5`. It reads `Sonnet 5.5`, and which was true was not checked. A plain `/model sonnet` did not do it.
- A model that changes without printing these lines, as an automatic fallback, a plan-mode model or a limit, leaves the old name showing. None was recorded.
- An agent can print the same two lines and show a false name.
- It is the main session's model; a subagent can run another.
- A name that is not a word and a version, as a custom model on another provider, shows the last name, or null. Not recorded.
- Without color, where a cut-off footer makes a placeholder read as a draft (see the known failures above), the banner is not read until the box empties or the footer says more. All four color-free recordings the test replays still read it, at 120, 40 and 30 columns.

### Found on the way

- **`/model` changes the default of every later agent.** Enter in the menu and `/model <name>` both write `"model"` to Claude's settings (`saved as your default for new sessions`), only `s` in the menu does not. So an agent's `/model` reaches the next agent started.
- **`/clear` starts a new session,** a transcript of its own, and `--resume` of the old id goes back to the conversation before it. An agent's `session_id` now follows it through a hook, so a relaunch goes on from the latest conversation (docs/AGENT_RESUME.md).

## Why it is waiting

Built for Claude Code (the wdx doc CLAUDE_PROMPTS, step 1); the other three parsers leave it nil. `ScreenState.Prompt` is what the ACA asks, read only with `waiting` and nil when the parser sees a prompt it cannot read, so `waiting` alone is what it was. Decided: what is being asked is shown by `wdx agent get` and tide (step 2), kept out of the logs; `limited` is for a later step of its own.

| `Prompt` | What it holds |
|---|---|
| `Kind` | `yes_no`, answered yes or no in one of Claude's variants (a permission, a plan approval, the trust prompt), or `decision`, a question Claude's model asks with options of its own. Which yes/no prompt it is is not a field: the step that answers them adds it |
| `Title` | `Bash command`, `Create file`, `Bash command · from the general-purpose agent`, `Accessing workspace`, a decision's tab `Color`; `""` for the plan approval |
| `Question` | `Do you want to proceed?`, `Do you want to create wdx-b.txt?`, `Claude has written up a plan and is ready to execute. Would you like to proceed?`, `Accessing workspace:`, a decision's question |
| `Detail` | the command, the file's name, the workspace path; `""` for a decision and the plan |
| `Options` | every label as shown, without its number, box or tick; a decision's hold Claude's own `Type something` and `Chat about this` |
| `Selected` | the option with `❯`; -1 when none shows |
| `Multi` | a decision whose options are checkboxes |
| `Ticked` | the index of each option whose box is `[✔]`, in order; empty for a multi-select with none and for every other prompt |

How Claude Code's parser reads it, in the two branches that read `waiting`: the kind from the footer and the line above the options, never from the options' words, which a reply above can hold too; the options upward from the footer; the question right above them; the title and detail from the block under the last full rule, never above it. The rules are in docs/ACA.md's row, the widths' findings in the wdx doc CLAUDE_PROMPTS. The holder compares states with `ScreenState.Equal`, so a selection moving or a box ticked is a change it publishes.

From the holder to the person (step 2): `holder.Status` carries it as `prompt`, left out when none is read, so an older holder reads as none; a status goes out when the prompt alone changes. The daemon keeps it in `agents.prompt` as JSON with the activity, only with `waiting`, cleared when the terminal ends and, for terminals that ended with no daemon running, at the daemon's start, and serves it as `Agent.prompt` (docs/AGENT.md), which the project stream sends as any other change. The logs say its `prompt_kind` and `prompt_options`, the count, never the text: the holder's `screen state changed`, the daemon's `agent status changed`, and the request and command logs, whose responses go through `api.AgentResponseLogged`. tide's row and `agent list` read `yes/no` or `decision` in place of `waiting`; the info tab and `agent get` show `prompt`, `question`, `detail` and `options`, `❯` before the selected one and, in a multi-select, `[✔]` before a ticked option and `[ ]` before the others.

Options differ per prompt: `No` is 4 for Bash and 3 for a file, and offers depend on the mode, so whatever answers later goes by label, not number.

### Hooks

Hook support differs too much to build on across ACAs (checked 2026-10-31):

| | Claude 2.1.314 | Codex 0.163.0 | Cursor 2026.10.19 | Antigravity 1.4.7 |
|---|---|---|---|---|
| Set per launch | `--settings`, untested | not documented | none documented | sources conflict |
| Permission prompt event | yes, allow or deny | yes, allow or deny | no | `allow` did not suppress the prompt on 1.1.2 |
| Interrupt seen | no | `Interrupt` | `stop` status `aborted` | unknown |
| Limit event | `StopFailure`, no reset time | not documented | not documented | unknown |

They come with auto-approve, ACA by ACA. They give the exact command where the screen cuts it off, and they matter for safety: an agent can print text that looks like a prompt, so what approves must not trust the screen alone. Auto-approve on an ACA without a permission hook needs its own review.

From Claude Code's hook docs, not yet tried:

- `PermissionRequest` can answer `allow` or `deny` without typing keys, and carries `tool_name` and `tool_input`.
- `PreToolUse` on `AskUserQuestion` can answer a decision: `allow`, with `updatedInput` holding the `questions` and an `answers` map from question text to the chosen label.
- `StopFailure` gives `error` (`rate_limit`, `billing_error`, `authentication_failed` and others), `error_details` and `last_assistant_message`.
- `Stop` does not fire on a user interrupt, and cancelling a tool fires no failure hook.
- The docs do not list `--settings` as a hook source, but wdx's `SessionStart` hook given there runs; `disableAllHooks` can turn hooks off, and `--safe-mode` silences every hook, those from `--settings` too (2.1.319 prints `Safe mode: all customizations are disabled (CLAUDE.md, skills, plugins, hooks, MCP, agents, and more)`), so a hook-based feature must survive both. wdx does not use `--safe-mode`; the record scripts do, which is why step 1 of the wdx doc CLAUDE_PROMPTS could not compare the hooks' JSON with the screen.
