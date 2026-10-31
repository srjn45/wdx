# Agent activity

Status: step 1 is built: `ScreenParser` and `ClaudeCodeScreenParser`, tested on recordings, which only parse the screen and produce the latest `ScreenState`. Nothing calls them yet, so nothing shows to a user. Step 2, sharing state with the daemon, and how `wdx agent send` uses it are deferred, to discuss later.

What an agent's ACA is doing right now. It is not the terminal's `state`, which stays whether the process is alive (docs/AGENT.md). Later features build on it: auto-approve, switching ACA or resuming at a limit, autopilot. None of those is in this step.

This step answers only "is it working, idle or waiting?", from the bottom of the screen. Why it is waiting, the prompt, is a separate problem for the next step (see Later).

## The model

`activity` is a new field of an agent, beside `state`. It is null unless the process is running.

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
- `watchScreen` owns this, generic and with no Claude knowledge. `Parse` stays pure: `Activity: unknown` with `InputBox: true` says "an input box whose footer does not say", and it never leaves the holder. Until step 2 the fallback exists only as `followMotion` in the tests.
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

Not shown by the recordings: a resize or attach redraw moving the screen, a stall longer than recorded, a forced slow API wait, a loaded machine, a real holder. The recordings do not prove two rules, which only unit tests fail on: bytes are not motion, and input holds the answer. In fullscreen mode Claude turns on any-motion mouse tracking, so step 2 must decide whether a viewer's mouse movement counts as input.

The true turn times are frozen as data in the test; transcripts are not kept.

### What `wdx agent send` needs

`wdx agent send` delivers on `idle` (docs/SEND.md), so the human asked that no open prompt ever reads `idle`. A throwaway check on all 24 recordings, at still times of 3 s and 4 s: at each of the 127 moments whose last three lines show a prompt's footer, the parser read `waiting`, and none read `idle`. That check uses the same footer words as the parser, so it cannot see a prompt whose footer is not recognized. Such a prompt without an input box reads `unknown`, which holds. One with something like a box would read `idle`: at 40 columns a decision's `❯ 1. Red` between two rules already looks like one, and only its footer saves it. At narrower widths, or for plan approval at 40 and prompts not recorded, that is untested. Step 2 should make a box whose line is a numbered option not count as a box.

**Conflict with docs/SEND.md, to settle when `wdx agent send` is discussed.** It says an ACA's text in its box reads `unknown`, so a message is not submitted along with what a human is typing. With the fallback, a still draft reads `idle`, so `agent send` would deliver into the draft. Agreed with the human: the public `activity` stays `idle`, and the parser also reports text in the box, whatever the activity, since a human can type during a turn too. `watchScreen` reads it along with the activity, and the sender holds while it is there. docs/SEND.md already plans for the parser to tell the sender more than the public `activity`.

It is called `draft`, since the text stays after the typing stops. A draft left in the box holds messages for as long as it stays, and they queue up behind it, with no timeout (decided by the human). Reading it needs care, as Claude fills the box with text nobody typed, a placeholder (`❯ Try "create a util..."`) or a suggestion (`❯ show the ping output`). In plain text they look like a draft. In the raw output they are dim (`\x1b[2m`) and typed text is not, and with typed text the footer shrinks (`⏵⏵ auto mode on · 1 shell`, against `... · ← 1 agent · ↓ to manage` with a suggestion). 

Tested on 8 scripted sessions at 120, 60 and 40 columns in both renderers, in `--safe-mode`, under tmux. `ScreenParser.Parse(lines, undimmed []string)` returns `ScreenState{Activity, InputBox, Draft}`, where `undimmed` comes from `Screen.Undimmed()`: the same rows as `Screen.Text()` with dim cells blank. The answer was right for an empty box, a placeholder, a suggestion, text typed at idle, during a turn and with a shell or subagent running, text typed then cleared, typing over a suggestion, accepting one with Tab or Right, a multi-line draft, a paste, a slash command with its menu open, bash mode, a prompt opening over a draft and closing again, a wrapped draft, a past `❯` above, and a draft with dim-looking words. A draft only ever appeared within 800 ms of input.

Known failures, kept as such in the tests:

- **No color, with the footer cut off.** With `NO_COLOR=1` or `FORCE_COLOR=0` Claude sends no dim, so only the footer veto below can tell its own text from a draft. Where the footer is cut before the veto's words, its placeholder or suggestion reads as a draft: at 40 columns in auto mode (`⏵⏵ auto mode on (shift+tab to cycle)`), and at 30 columns in manual mode (`⏸ manual mode on · ? for …`). With `Activity: unknown` and `InputBox: true`, so the fallback can call the agent `idle` while a message is held. At 120 columns, and 40 in manual mode, it reads right. `NO_COLOR` is not in the holder's inherited variables, but a user's rc file could set it, as Claude starts through their login shell. Untested under a holder.
- **A draft taller than the screen, classic renderer, 40x24.** The box's `❯` and top rule scroll away, so it reads `unknown` with no box and no draft. `unknown` also holds a message, so this fails safe.

**The footer veto, decided by the human.** When the box's footer matches `? for shortcuts`, `← <n> ag` or `← for agents`, `Draft` is false whatever the styling says, as Claude drops those words while the box holds text. No recording has text that is not dim above one of them, footer and text change in the same chunk, and a test keeps checking it on the color recordings. A bare footer says nothing the other way: many moments have one with no draft.

For later, when `wdx agent send` is discussed: in bash mode with text the box has a draft, but with only `!` it has none, and a message delivered there may run as a shell command. Untested.

Not recorded: vim mode, `#` and other input modes, image pastes, a running turn in bash mode, a real holder.

The parser costs 317 µs and 52 KB per read at 120x40, since the screen is read twice.

Other facts:

- **Two renderers.** The human's `"tui": "fullscreen"` setting puts Claude on the alternate screen from startup to exit, so `Screen.Lines()` would return nil for the whole session. `"tui": "default"` never enters it, its footer shares a row with `◐ medium · /effort`, and notices can sit below it. Parsers receive `Screen.Text()`, plain text of whichever screen shows, and `Screen.Undimmed()`, the same rows with dim cells blank.
- **The moment is 50 ms.** Below it a blank screen at startup, 18-30 ms long, shows as flicker. From 100 ms real prompts can go missing, though the driver answered those unnaturally fast. Half-drawn frames appear only when a chunk is cut mid-frame, never across a recorded chunk boundary. Not measured on a loaded machine.
- **Parsing costs nothing that matters,** about 0.1% of a core at the 1.4-7.3 reads a second seen while working, measured before the second read below doubled it.
- **Recording without private content.** `--safe-mode` keeps the login and drops CLAUDE.md, settings and plugins. Fixtures have paths and session ids masked at the same length. The banner's plan name, `Haiku 5.5 · Claude Max`, stays.
- **Not recorded:** a foreground subagent (Haiku backgrounded it twice), MCP input, login, a network retry, a limit, plan approval and a shell at 40 columns, and anything under a real holder. Step 2 must re-check under the holder that Claude does not use synchronized output, since the holder's vt answers terminal queries that nothing answered in these recordings.
- `~/.local/bin/claude` has moved to 2.1.316 since. The recordings used 2.1.314 directly, and only the idle footer was compared.

## Where it lives

The holder owns it, as it owns the truth about the process (docs/TERMINALS.md):

```
PTY ─► spool ─► vt screen ─► channel ─► watchScreen ─► frameActivity ─► daemon
                                            │ calls
                  ScreenParser.Parse(lines, undimmed)
```

- The holder stays free of ACA code. Everything in it is generic, `watchScreen` included: the channel, the moment's wait, the recover, pushing a change. Only the parser is ACA-specific.
- `aca.ScreenParser` is the interface, with `Parse(lines, undimmed)` giving a `ScreenState`; `aca.ClaudeCodeScreenParser` is Claude Code's, and `CodexScreenParser` and others come with those ACAs. Parsers are pure functions of the screen's lines. `AiCodingAgent` gets a `ScreenParser()` method, so an ACA cannot compile without one, and `aca.ByID(id)` is the factory.
- The holder is its own process, so the daemon cannot hand it a parser: the daemon passes `--aca <id>`, and the hidden `wdx holder` command in `internal/cli/holder.go` calls `aca.ByID` and gives the holder just a function. So `cli -> aca` joins the dependency directions, not `holder -> aca`. A shell terminal has no parser and no goroutine.
- `agent.Launches` keeps its list of ACAs, through which tests hand in their own; it is not moved onto `aca.ByID`.
- `watchScreen` runs apart from the vt, told over a channel of capacity 1 each time the screen changes. The vt's send never blocks: when a signal is already pending it is enough, since the goroutine reads the latest lines when it wakes. It never holds up the PTY or the vt.
- After a signal it waits a moment before reading, so that it reads a finished redraw: Claude does not use synchronized output, so half-drawn screens do reach the vt. This limits the rate rather than waiting for quiet, so a screen that never stops is still read. The moment is a field tests can shorten; its value comes from replaying the recordings.
- The holder pushes a frame only when the activity changes, as it pushes `exited`, and nothing polls. `Hello` carries the current one, so a daemon that restarts learns it on connecting.
- The daemon keeps the last one of each live holder in memory and tells the stream. No table or migration: it dies with the process.
- A panic in a parser is recovered and gives `unknown`: the holder owns the agent's PTY, and a parser must never take it down.
- Protocol version 3. A running holder of version 2 keeps working and its agents show `unknown` until relaunched.
- Changes are logged at info as the activity's name. The screen's text never leaves the holder, so this step has nothing secret to keep out of the logs.

A parser fixed in a wdx release reaches a running agent only when it is relaunched, since a holder keeps its binary. That matches the ACA: a running ACA keeps its wording until relaunched too, and the relaunch starts a holder of the current wdx.

### Motion, for other ACAs

Another way to read an ACA: ignore the screen while output keeps coming, and identify the activity only once it stops. It is the simple choice for an ACA whose last line does not say enough, so it stays an option when adding Codex, Cursor or Antigravity, chosen per ACA. Not built in this step.

Claude Code uses it only as the fallback for unreadable footers (see The motion fallback): as the main rule it would misread the Bash prompt, whose `●` blinks every 0.6 s for as long as it is open. An ACA that uses it needs the same check first: a recording of whether anything keeps moving at its prompts and at idle.

### Sharing state, later

Decided by the human on 2026-10-31: for now only detect the right values; how they reach the daemon is deferred, and `draft` stays private to the holder until then.

The direction: more attributes will come from watching a terminal, so instead of an event type for each, the holder sends the complete state object over its existing connection on every change, as the project stream does for tide. Parsers detect, the holder emits, the daemon updates what it keeps and emits to tide. So `frameActivity` in the plan below, and in docs/SEND.md's frame numbers, would become one state frame. Whether the daemon keeps it in the database or in memory is to settle then; this doc so far says memory.

## What the user sees

```
$ wdx agent list
NAME          STATE    ACTIVITY  ACA          DIR               ID
calm-otter    running  waiting   claude_code  /home/me/dev/wdx  agent-5e6f7a8b
swift-falcon  running  working   claude_code  /home/me/dev/wdx  agent-1a2b3c4d

$ wdx agent get --name calm-otter --json
{"agent": {"name": "calm-otter", "state": "running", "activity": "waiting", ...}}
```

The project stream carries it, so tide's Agents list shows it live, and marks an agent that is `waiting`.

Known limits at the end of this step:

- An agent at a token limit probably shows `idle`, since its turn ended and its input box is back. Not recorded yet, and `limited` is the next step's.
- Claude rewording its footer gives `unknown`, not a wrong activity, until a wdx release fixes the parser.

## Plan

Each step builds and passes `make test` on its own, and is a `feat:` or `test:` commit. Adding a field and a column breaks nothing, so no `!`.

1. **Prove the rule, before touching the holder. Done.** The parser, tested on recordings: every recorded screen gives its activity, and replaying the raw output chunk by chunk through `internal/screen` gives the right sequence.
2. **The holder.** `--aca`, `AiCodingAgent.ScreenParser()`, `aca.ByID`, `watchScreen`, with the fallback moved out of the tests, `frameActivity` or the state frame, `Hello.Activity`, protocol version 3, `Conn.Activity()`. Tests run the real parser against a test command that prints real footers, so no real ACA is needed: a change is pushed, a repeat is not, a reconnect is told the current one, a parser panic gives `unknown` and the terminal lives on, output keeps flowing while the parser is held up. The `cli -> aca` direction in AGENTS.md. Re-check Claude Code 2.1.316, and decide whether a viewer's mouse movement counts as input.
3. **The daemon and API.** `activity` in `api/openapi.yaml`, `make generate`. The supervisor passes the ACA, keeps the last activity per live holder and notifies the streams. Tests: an agent's activity through `GET /v1/agents`, the project stream, a daemon restart with the holder alive, a version 2 holder's `unknown`, null once the process ends.
4. **The CLI.** The `ACTIVITY` column and `get`'s field, with their tests and help.
5. **Tide.** The activity in the Agents list and the `waiting` mark, with its tests.
6. **Docs and the real thing.** docs/AGENT.md, docs/TERMINALS.md, README, V1_FEATURES.md. Then by hand with the real Claude Code: a prompt, a permission, a decision, Esc, a trust prompt, a daemon restart while waiting, and a narrow tide pane. `make check`, `make test`, `make test-changed`, and `make test-stress PKG=` on `holder`, `terminal` and `agent`.

## Recordings

Real Claude Code 2.1.314 sessions, at widths from 30 to 120 and in both renderers, are in `internal/aca/testdata/claude_code/2.1.314/`: 29 recordings, 2.3 MB. Each holds `raw.out` and `raw.timing`, with input times where a test needs them, and some a `marks.log` of what was done when. The tests replay them through the screen in CI, which needs no real ACA.

They ran in tmux with Haiku and tiny prompts, in `--safe-mode`, which keeps the login and drops CLAUDE.md, settings and plugins, apart from `prompts`, which quotes nothing private. Paths and session ids are masked at the same length, so the layout holds; a mask can leave a character changed, as `re-exable`. Claude's `sleep` was blocked by the human's settings in an early attempt, so they use `ping`.

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

## Later: why it is waiting

The next step, on top of `waiting`. Decided so far: tide shows `limited` at a limit, and what is being asked is shown by `wdx agent get` and tide, kept out of the logs.

| Kind | How the screen shows it |
|---|---|
| `permission` | `Do you want to proceed?` or `Do you want to create <file>?`, numbered options |
| `decision` | radio: options `1. Red`; checkbox: options `1. [ ] Apple` and a `✔ Submit` tab |
| `trust` | `Accessing workspace:`; the cursor starts on `No, exit`, so a bare Enter quits Claude |
| `input`, `auth` | not recorded |
| `limited` | its own activity rather than a kind, since the user cannot answer it. The old warden's capture (`internal/agentbackend/backends/testdata/ratelimit/claude_limited.txt` in `~/dev/warden`, `Usage limit reached, resets 3:45pm (UTC)`) is the only sample, so unproven until a real one is recorded |

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
- The docs do not list `--settings` as a hook source, and `disableAllHooks` can turn hooks off, so a hook-based feature must test the first and survive the second.
