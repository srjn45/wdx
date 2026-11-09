# Cursor's footer, checked by hand

On 2026-11-09, for the plan `cursor-footer`'s finish task, with the plan's worktree at 666c96a.

## Setup

- Cursor 2026.11.05-86047df, `--model gpt-5.4-nano-none`, prompts asking for one word back.
- `go build ./cmd/wdx` of the worktree; its daemon (`daemon start --foreground`) on a scratch home, a scratch git repository (branch `main`) opened as a project, each agent made with `wdx agent create --aca cursor` and attached with `wdx agent attach` in a private `tmux -L` session at 120x40. The attach's own bar takes a row, so the holder logged `terminal resized` 120x39, and 40x23 after the resize.
- wdx takes the user's home from the user database, not from `$HOME` (`holder.LookupAccount`). So that Cursor's settings and the user's `statusLine` came from a scratch home, never the real `~/.cursor`, the daemon ran under `bwrap --dev-bind / / --ro-bind <copy> /etc/passwd`, the copy naming the scratch home as the user's home. Cursor's login and `cli-config.json` were copied into it, as `record/README.md` says. The scratch home was deleted afterwards.
- Load: a quiet machine, a 1-minute load average of about 1 (0.95 at the third agent's start, 0.38 at the end).

## Result

Every case passed. Across 3 starts and 12 messages, nothing logged `message submitted again`, `message not submitted`, `message left in the box`, `message not delivered` or `paste not shown`. `message held` with `no input box` came only before Cursor had drawn its box at each start, and the prompt was delivered once it was drawn.

- **The box and the model.** Under the box, the attached screen shows the footer wdx's status line prints: `GPT-5.4 Nano None` before the first message, `GPT-5.4 Nano None · 3.5%` after it, then `~/w/repo · main`. The holder logged `input_box: true` from the first ready screen. `wdx agent list` gave `model: GPT-5.4 Nano None`, `activity: idle` at rest, `queued: 0`, `held: 0`.
- **Context size** still shows: `context: {tokens: 9520, window: 272000}` after the first turn, 10064 after more.
- **Delivery at rest**: `message delivered` each time, the draft cleared after the first Enter.
- **Delivery during a turn**: with Cursor's terminal tool running `sleep 25`, a general message was delivered and steered into it (`steer_pending` true, then false). Cursor moved the command to a task (`1 task` above the footer) and answered. An `--instant` message right after logged `message steered`, and its draft cleared.
- **A user's own statusLine**: the scratch home's `cli-config.json` had `statusLine: {type: command, command: "printf 'my own line\n'", padding: 1, updateIntervalMs: 2000, timeoutMs: 1500}`. The agent's own config kept `padding`, `updateIntervalMs` and `timeoutMs` with wdx's `sh <backup>/cursor-status.sh` as the command, and its script ran the user's command after wdx's lines. The screen showed the model line, then the directory line, then `my own line`. The agent read a box and got its messages.
- **40x24 after a resize**: both agents, with and without the user's line, were resized from 120x39 to 40x23. No `screen state changed` followed: they stayed `input_box: true`, idle. Then a message at rest to the one with the user's line, and to the other a `sleep 20` turn with a general message and an `--instant` one during it: all delivered (`message delivered`, `message steered`), with the footer and the user's line still drawn at 40 columns.

## The holder's numbers

`paste_shown_ms` and `submitted_ms` come from `message delivered` / `message steered`. "Draft gone" runs from Cursor's first Enter (taken as that line's time minus 150 ms) to the first `screen state changed` with `draft: false`.

| agent | size | message | log line | paste_shown_ms | submitted_ms | draft gone (ms) |
|---|---|---|---|---|---|---|
| first try* | 120x39 | start prompt | delivered | 61 | 301 | 87 |
| no user line | 120x39 | start prompt | delivered | 62 | 301 | 83 |
| no user line | 120x39 | at rest | delivered | 54 | 301 | 71 |
| no user line | 120x39 | starting the `sleep 25` turn | delivered | 55 | 300 | 69 |
| no user line | 120x39 | general, mid-turn | delivered | 28 | 301 | 59 |
| no user line | 120x39 | `--instant`, mid-turn | steered | 53 | 300 | 69 |
| user's line | 120x39 | start prompt | delivered | 58 | 300 | 82 |
| user's line | 120x39 | at rest | delivered | 56 | 301 | 70 |
| user's line | 40x23 | at rest | delivered | 53 | 300 | 69 |
| no user line | 40x23 | starting the `sleep 20` turn | delivered | 55 | 301 | 71 |
| no user line | 40x23 | general, mid-turn | delivered | 39 | 300 | 56 |
| no user line | 40x23 | `--instant`, mid-turn | steered | 53 | 301 | 67 |

\* On the first try, the scratch home had no zsh startup files, so the login shell stopped at zsh's new-user menu before Cursor started. It was dismissed by hand, and that agent was removed. The rest ran with an empty `.zshrc` in the scratch home.

## Not checked

- The approval prompt and its tell box at 40 columns on the real Cursor: the agents ran with `--permissions full`. The recordings of `2026.11.05-wdx-status` and the parser's tests cover them.
- A `display_name` or `cwd` the script's `sed` cannot read: the script's tests cover it.
- What `padding: 1` looks like on screen: the setting was checked in the agent's config, not measured on screen.
