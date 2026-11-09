# send-confirmed: checked by hand

For the finish task: what the `checked` task saw on 2026-11-09 with the holder of fe4c323, for the wdx doc SEND_CONFIRMED's "Checked by hand". Not for the repository: the finish task moves what it needs into the doc and removes this file.

## How

- `go build ./cmd/wdx` of the plan's worktree, its daemon on a scratch home (`WDX_HOME=/tmp/wdxc`), a scratch git repository `/tmp/wdxc-proj` opened as a project. Its logs stay in `/tmp/wdxc/logs/wdx-2026-11-09.log` for anyone who wants the full lines.
- Each agent created with `wdx agent create --aca <aca> --model <cheapest> --prompt <7088 bytes>`, then attached in a private `tmux -L wdxchk` window at 120x40. The holder starts at 80x24, so a delivery may come before or after the attach's resize; the boxes and drafts read the same either way.
- The prompt: one line asking for the one word OK, then 60 padding lines; 7087 bytes as the holder counts them. The short messages asked for one word each.
- Load: `go test -race -count=1 ./...` of this repository in a loop under a 3 GB scope, and from the Codex runs on eight busy shell loops (as `stress -c 8`, which is not installed) beside it. The 1-minute load average on 8 cores is given per run.
- Numbers below come from the holder's lines: `paste_shown_ms` and `submitted_ms` of `message delivered` / `message steered`, and "draft gone" is the time from that line, logged just after the submit, to the first `screen state changed` with `draft: false`. For Cursor, whose Submit is Enter, 150 ms, Enter, "draft gone" is from the first Enter (`submitted_ms` - 150 after the paste).
- At rest and during a turn: a short general message at rest, a message asking for `sleep 20` (Cursor: `sleep 25`, see below), then during that turn a short general message and a short `--instant` one.

## Results

No run, of 18 starts and 20 short messages on the four ACAs, logged `message submitted again`, `message not submitted`, `message left in the box` or `message not delivered`. `paste not shown` showed once, in the forced case of Claude Code below. Every draft went after the first submit, at rest and mid-turn: no ACA kept a message in the box once it was queued during a turn, so the doc's open question "whether any ACA keeps a message in the box after it is queued mid-turn" is answered no for all four. The retry path itself was not reached on a real ACA: no lost submit happened, and none was provoked.

### Claude Code 2.1.319, `--model haiku`

| run | load | paste_shown_ms | submitted_ms | draft gone (ms after submit) |
|---|---|---|---|---|
| warm-up (stopped at the folder trust prompt, answered by hand) | ~8 | 845 | 846 | 529 |
| 1 | 24.6 | 971 | 971 | 329 |
| 2 | 23.3 | 641 | 641 | 478 |
| 3 | 16.3 | 362 | 362 | 217 |
| short, at rest | ~16 | 50 | 150 | 94 |
| short, starting the `sleep 20` turn | | 50 | 151 | 89 |
| short, general, mid-turn | | 28 | 150 | 68 |
| short, `--instant`, mid-turn (steered) | | 51 | 151 | 64 |

- All three loaded starts showed the paste later than the old fixed 150 ms (362 to 971 ms), and the holder waited for it: this is the case the two lost Enters came from.
- The steered message: Ctrl+X Ctrl+S moved the running `sleep 20` to the background and both mid-turn messages were answered in one reply; the turn's `done` came after the background command ended.
- **Forced fallback, one run** (load ~37): Claude Code's process stopped with SIGSTOP the moment the box showed, for 3 s. The holder logged `paste not shown` at 2001 ms (`waited_ms`), then `message delivered` with `submitted_ms` 2001 and no `paste_shown_ms`, its Enter written while Claude was stopped. Resumed, Claude read the paste and the Enter together and submitted: the draft showed and went 244 ms later, and it replied. So an unshown paste is delivered as before, and here it worked; whether a starved Claude reads that Enter as a newline instead, as on the two stuck starts, is luck, as before this plan.

### Codex 0.167.0, `--model gpt-5.6-luna`

| run | load | paste_shown_ms | submitted_ms | draft gone |
|---|---|---|---|---|
| warm-up | ~8 | 62 | 150 | 52 |
| at low load (the test loop was in its tail), not counted | 1.6 / 1.6 / 1.0 | 82 / 74 / 53 | 150 / 150 / 151 | 51 / 29 / 56 |
| 1 | 4.4 | 239 | 239 | 64 |
| 2 | 6.2 | 54 | 150 | 29 |
| 3 | 7.4 | 55 | 150 | 54 |
| short, at rest | 7.9 | 58 | 151 | 47 |
| short, starting the `sleep 20` turn | | 56 | 150 | 54 |
| short, general, mid-turn | | 33 | 151 | 55 |
| short, `--instant`, mid-turn | | 61 | 150 | 56 |

- No folder prompt. Codex was the lightest-loaded ACA of the four (load 4 to 8 for the counted runs).
- **To note for the design:** during a Codex turn the parser's `in_turn` flips between true and false about every 600 ms (`screen state changed` each time), and the confirm's settle restarts on every published change. So were a Codex submit lost mid-turn, its retry would wait until the flicker stops, at the turn's end. Not hit here, as every draft went within 60 ms.

### Antigravity 1.5.3, `--model gemini-3.8-flash-low`

| run | load | paste_shown_ms | submitted_ms | draft gone |
|---|---|---|---|---|
| warm-up | 15.5 | 69 | 151 | 116 |
| 1 | 26.2 | 68 | 150 | 120 |
| 2 | 34.4 | 68 | 151 | 118 |
| 3 | 37.0 | 67 | 150 | 118 |
| short, at rest | 36.0 | 84 | 150 | 119 |
| short, starting the `sleep 20` turn | | 73 | 150 | 106 |
| short, general, mid-turn | | 20 | 150 | 71 |
| short, `--instant`, mid-turn | | 58 | 151 | 107 |

- No folder prompt. Each start showed Antigravity's own `⚠ Sign-in update needed` notice above the reply; it did not affect the box or the delivery.

### Cursor 2026.11.05-86047df, `--model gpt-5.4-nano-none`

**Failed as built, before the holder's new code is reached, and not caused by this plan.** The first start (load ~32) never delivered: the holder read `no input box` (`activity: unknown`, `input_box: false`) for minutes and held the prompt. Cursor drew its box (`→ Plan, search, build anything`) with no footer under it, and `CursorScreenParser` counts the box only with the footer, the status line and the directory.

Why, checked by running `cursor-agent` by hand in the same tmux:

- with the agent's `cli-config.json` minus its `statusLine`, Cursor draws the footer (`GPT-5.4 Nano None ... Run Everything`, `/tmp/wdxc-proj · master`);
- with the `statusLine` wdx pins (`sh <agent>/cursor-status.sh`, since 37fd840, the wdx doc CURSOR_CONTEXT), whose script saves the JSON and prints nothing when the user has no status line of their own, Cursor draws no footer at all;
- with a status line that prints `wdx`, the footer is that one line, with neither model nor directory.

So on this Cursor every wdx Cursor agent reads `no input box` and gets no message. It is on main too: the human's own Cursor agent `term-740862f8` in `~/.wdx/logs/wdx-2026-11-09.log` (10:53, the released wdx) logged only `input_box: false`. The holder was left as it is; this needs its own fix (the parser or the status script), outside this plan.

To still check the holder's delivery on Cursor, each later scratch agent's `cursor-status.sh` was changed, in the scratch home only, to print the two footer lines Cursor would draw (`GPT-5.4 Nano None` and `/tmp/wdxc-proj · master`) right after `agent create`. With that:

| run | load | paste_shown_ms | submitted_ms | draft gone (ms after the first Enter) |
|---|---|---|---|---|
| 1 | 10.2 | 108 | 301 | 123 |
| 2 | 10.4 | 113 | 300 | 130 |
| 3 | 10.1 | 86 | 300 | 124 |
| short, at rest | 9.5 | 58 | 300 | 89 |
| short, starting the `sleep 25` turn | | 61 | 300 | 109 |
| short, general, mid-turn (during `$ sleep 25`) | | 68 | 302 | 108 |
| short, `--instant`, mid-turn | | 57 | 300 | 147 |

- The draft went after the first Enter every time, before the second, which did nothing visible at rest and steered mid-turn, as docs/SEND.md says.
- The first try of the turn, asking for `sleep 20` as on the others, was answered `done` in 2 s without the tool, so its two "mid-turn" messages landed at rest (paste_shown 55/60, submitted 301/301, gone 96/102 ms after the first Enter). The mid-turn rows above are from a second try asking for the terminal tool by name, which ran `sleep 25`.
- Load was lower for Cursor (~10) than for Claude Code and Antigravity, as the test loop was between runs.

## Not checked

- A real lost submit, so the retry (`message submitted again`) and `message left in the box` on a real ACA: none happened under load, and the holder's tests cover them.
- Cursor as built: blocked by the footer, above.
