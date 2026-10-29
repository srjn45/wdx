# Attach and history

Status: implemented. Extends docs/TERMINALS.md, which this document assumes.

Attach connects a client to a running terminal: it shows the terminal's current screen and its history, streams the output live and sends keystrokes to the shell. History is every line that scrolled off the terminal's screen, kept on disk while the terminal exists, whether or not anyone was attached.

## Why a vt in the holder

On attach, the client's terminal has missed what the shell drew before; the holder must send bytes that bring it to the current screen. The options and what a spike measured (`x/vt` v0.1.0, programs run in a PTY at 100×30, snapshot or replay fed into a fresh vt and compared cell by cell):

| | Result |
|---|---|
| **vt snapshot** | exact for colored `ls`, emoji / CJK / combining marks, `less` after paging and a search, `top`, tmux with a split pane, a zsh login: 0 differing cells, cursor, modes and alternate screen included; 0.4–3 KB per snapshot |
| **ring replay** (resend the raw output) | exact only while the ring has not wrapped and at the same size. Wrapped: hundreds of differing cells; a redraw nudge (resize away and back, as dtach) fixes the picture of full-screen programs but not the modes set before the ring's start (alternate screen, cursor keys, keypad). At another size: garbled, and `x/vt` panicked |

How other tools do it: tmux, screen and zellij run a full emulator per pane and render it to the client, which never sees raw bytes (no native scrollback); dtach and abduco replay nothing and ask the program to redraw; shpool and VS Code's terminal persistence pass raw bytes live and keep an emulator only to restore on attach, as here; mosh syncs screen states and has no scrollback.

A vt also makes history possible: a line that scrolls off its screen is final and can be stored once, rendered, and addressed by number, which raw bytes cannot (they only mean something replayed from the start, and every redraw of a progress bar or TUI is in them).

Measured costs of `x/vt`, which shape the design below: it parses about 1.5 MB/s (5.5 MB/s with two hot spots patched), it keeps about 9.4 KB per scrollback line in memory, and it panics on a scroll region taller than the screen (3 bytes reproduce it at its latest commit). Hence the vt reads from a spool beside the output path, keeps only the screen in memory, and recovers from panics. Alternatives: `hinshun/vt10x` (about 7 MB/s, unmaintained, no grapheme clusters), libghostty-vt and libvterm (fast, but cgo across four release targets), tmux as the backend (an external install that replaces the holder).

## Clients and who draws

The holder never draws. It keeps a headless vt (terminal emulator) to know what the screen looks like; every client brings its own vt to draw with.

```
                          ┌──────────────────────── holder ─────────────────────────┐
 shell ── PTY ──────────▶ │ reader ─┬─▶ spool/ (raw bytes, on disk) ──▶ vt          │
   ▲                      │         │                                 ├─ screen    │
   │                      │         │                                 └─ lines that│
   │                      │         │                                    scroll off│
   │                      │         │                                    ─▶ history/│
   │                      │         └─▶ attached clients (live, full speed)          │
   └──── input ◀──────────│ ◀── input frames                                         │
                          └──────────────────────────────────────────────────────────┘
                                   ▲ socket <home>/run/<id>.sock
            ┌──────────────────────┼─────────────────────────┐
  wdx terminal attach          tide pane                 daemon relay (later)
  → your terminal app draws    → tide's vt draws         → browser, xterm.js draws
```

| Client | Connects | Draws with | Scrolls through history |
|---|---|---|---|
| `wdx terminal attach` | the holder's socket | your terminal app (GNOME Terminal, iTerm...) | its own scrollback, filled with the last N history lines on attach; the rest through `wdx terminal history` |
| tide (later) | the holder's socket | its own vt per pane | lazily, page by page through the API |
| web (later) | the daemon (WebSocket), which relays to the holder | xterm.js | the last N lines on attach, older pages through the API |

Local clients go straight to the holder: an attached session survives `wdx daemon restart` and `wdx update`, and keystrokes take one hop. The daemon is only asked to resolve the terminal and check its state.

## The holder

### Output path

The reader goroutine reads the PTY master and, for each chunk:

1. appends it to the **spool**, and advances the stream position `pos` (bytes since the shell started);
2. queues it to every attached client.

It never waits for the vt or for a client, so the shell never blocks on either. The 1 MiB memory ring goes: the spool replaces it.

### The spool

`<home>/terminals/<id>/spool/`: the raw output in segment files of 8 MiB, named by the stream position they start at. It exists so the vt can fall behind during a flood (`cat` of a large log) without losing anything, and catch up after.

- A segment is deleted once the vt has consumed it and it is not among the last two (16 MiB), which serve attaching clients that need the bytes after the snapshot.
- Unconsumed segments are never deleted below 256 MiB. Past that the vt skips to the oldest segment kept and writes a gap marker into history: only a flood far faster than the vt for minutes gets there.
- The spool is emptied when a shell (re)starts: it belongs to one shell's run.

### The vt

`charmbracelet/x/vt` (MIT), wrapped in a `screen` type of our own, fed from the spool by one goroutine.

- **Screen**: cells with colors and style, cursor position, visibility and style, scroll region, title, and the modes a program sets (alternate screen, application cursor keys and keypad, bracketed paste, mouse reporting, focus events, autowrap), tracked through the vt's mode callbacks since it exposes no getter.
- **History**: the vt's scrollback is harvested after every 1 KiB it parses, which cannot push more lines than its 65,536 (a screen's height per 4 bytes, for screens up to 256 rows): each line is rendered with its style and links and appended to history, then the scrollback is cleared. The vt then holds only the screen: about 100 KB per terminal instead of ~94 MiB for 10,000 lines kept in it (but see the alternate screen in docs/VT.md).
- Only the main screen scrolls into history: what vim, less or htop show on the alternate screen never does, as in every terminal. `clear` pushes the screen into history before clearing it; a program's "clear scrollback" (`ESC[3J`) does not delete history.
- **Panics**: the vt panics on some input (a scroll region taller than the screen, which output racing a resize produces). Every write recovers, resets the vt to an empty screen of the current size and writes a gap marker into history. The shell and the attached clients are untouched; the next screen the program draws is right again.
- **Queries**: programs ask the terminal things (cursor position, device attributes, colors). While a client is attached its terminal answers them, through the live stream; the vt's answers are dropped. With none attached the vt answers, so a program that waits for an answer does not hang.
- Logged: vt resets and gaps, never the bytes.

### Size

The PTY and the vt have one size. The client that attached or resized last sets it, as tmux's `latest`; when it detaches the size stays. A program in the shell gets SIGWINCH and redraws for the new size. A holder starts at 80×24. The vt is resized at once: output it has yet to read from before the resize is drawn at the new size, which only matters while it lags.

### History

`<home>/terminals/<id>/history/`: the lines that scrolled off the screen, numbered from 0 for the life of the terminal (across relaunches).

- Segment files of 10,000 lines, named by their first line number (`0000000000.log`); a line is its rendered text, SGR styles and hyperlinks included, ending in `\n`. Line `n` is in segment `n / 10000`.
- When the holder quits, what the shell left on the main screen goes to history, as a terminal keeps it scrolled back. A relaunched shell continues the numbering after a marker line `── shell restarted <time> ──`; gaps get `── history interrupted ──`.
- When more than `terminal.history_lines` are kept, the oldest segment is deleted: history keeps between `history_lines − 10,000` and `history_lines` lines.
- Lines keep the width they were drawn at: the vt does not record which line breaks were soft wraps, so history cannot be reflowed to another width. A narrower client wraps long lines, a wider one shows them short.
- `<home>/terminals/` is 0700, its files 0600. Terminal output can hold secrets (a printed token, `env`): history is off when `terminal.history_lines` is 0.
- The daemon reads history straight from the files (append-only, written by one holder): it serves it while the holder runs, after the shell exited, and for a `lost` terminal.
- Removing a terminal, or the project, deletes `<home>/terminals/<id>/` once its holder is gone. At startup the daemon deletes the directories of terminals that have no row (a crash between the two).

## Protocol, version 2

Frames as in version 1. The connecting side says what it is in its `hello`: `"role": "daemon"` or `"client"`; a `hello` without one is a version 1 daemon's. The daemon's connection replaces the previous daemon's, as now; clients are added, any number of them.

| Frame | Direction | Payload |
|---|---|---|
| `hello` | both, first | adds `"role"` from the connecting side; the holder adds `"history_lines"`, the next line number |
| `attach` | client → holder | `{"cols": ..., "rows": ..., "history": N}`, all of history when N is negative; answered by `history`, `snapshot`, then `output` |
| `history` | holder → client | `{"from": ..., "lines": ["...", ...]}`: the last N lines before the snapshot, in frames of up to 256 KiB |
| `snapshot` | holder → client | `{"cols": ..., "rows": ..., "pos": ..., "history_lines": ...}`: the screen's size, its position in the output and the history line it continues; the `output` frames after it paint the screen (reset, modes, scroll region, cells, cursor, title), then carry the output from `pos` on |
| `output` | holder → client | raw bytes, up to 32 KiB a frame |
| `input` | client → holder | raw bytes, written to the PTY |
| `resize` | client → holder | `{"cols": ..., "rows": ...}` |
| `exited` | holder → daemon and clients | as in version 1; the holder then closes the clients' connections |
| `stop`, `release` | daemon → holder | as in version 1 |

**Attaching is consistent without waiting.** The holder locks the vt, takes its position `S` (what it has consumed), its history count `H` and the snapshot, and sends history lines up to `H`, the snapshot, then the output from `S` on: from the spool up to the live position, then live. No byte is shown twice or missed, however far the vt is behind. A client that later fetches older history asks for lines before `H`.

**Slow clients**: each client has a queue of 4 MiB. One that falls further behind is disconnected ("too slow" in the holder's log); the client sees its connection closed, the CLI reports `connection_lost` and the user attaches again. Resyncing it with a fresh snapshot instead is for later, with the web.

**Versions**: a holder keeps running the binary it started from. The daemon accepts holders of version 1 and 2 (today it accepts only its own: that check changes first, in its own release, so that rolling back from the release that brings version 2 keeps the terminals its holders run). A client needs version 2: on a version 1 holder it disconnects after the `hello` with `attach_unsupported`. That `hello` makes a version 1 holder drop the daemon's connection, which the daemon redials at once, as it does today.

## API

| Request | | Errors |
|---|---|---|
| `GET /v1/terminals/{terminal}/history?from=<line>&count=<n>` or `?tail=<n>` | 200 `{"history": {"from": 1200, "next": 1300, "first": 0, "lines": ["...", ...]}}` | `not_found` |

- `first` is the oldest line kept, `next` the number the next line will get. `count` and `tail` are at most 10,000; lines are the rendered text, escape codes included.
- Attach itself is not in the API: clients resolve the terminal with `GET /v1/terminals/{terminal}` and dial the holder.

New codes:

| Code | Status | Exit | |
|---|---|---|---|
| `terminal_not_running` | 409 | 1 | the terminal is not running, so there is nothing to attach to |
| `attach_unsupported` | 409 | 1 | the terminal's holder predates attach; remove and create the terminal again |
| `connection_lost` | 503 | 3 | the connection to the terminal ended: its holder is gone, or the client fell too far behind |

`connection_lost` is only reported by clients, as `daemon_unavailable` is.

## CLI

```
wdx terminal attach  (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
                     [--history <lines> | --history all] [--detach-key <key>]
wdx terminal history (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]
                     [--tail <lines> | --from <line> [--count <lines>]] [--plain]
```

### `wdx terminal attach`

- Resolves the terminal through the daemon (`terminalRef`, as `get`), which must be `running`, else `terminal_not_running`; then dials the holder.
- Talks to the controlling terminal, `/dev/tty`, not stdin and stdout: it puts it in raw mode, sends its size, forwards SIGWINCH as `resize`, writes the history, the snapshot and the output to it, and sends what is typed as `input`. Without a controlling terminal: `usage`. stdout stays free for the result, so `--json` works as for every command.
- `--history`: how many history lines to write before the snapshot, into the terminal app's own scrollback; default `terminal.attach_history`. `all` writes everything kept. A screenful of newlines follows them, so what the terminal showed scrolls up into its scrollback rather than being cleared by the screen painted over it.
- `--detach-key`: default `ctrl-\`; `ctrl-<letter>`, `ctrl-\`, `ctrl-]`, `ctrl-^`, `ctrl-_` or `none`. It detaches unless pressed again within half a second, which sends it to the shell once; anything else typed in that half second detaches too.
- Ends with:

| How | Text (after restoring the terminal) | `--json` | Exit |
|---|---|---|---|
| detach key | `detached from terminal api in app` | `{"result": "detached", "terminal": {...}}` | 0 |
| the shell exited | `terminal api in app exited with code 0` | `{"result": "exited", "terminal": {...}}` | 0 |
| the holder vanished | `connection_lost` | Problem Details | 3 |
| too slow | `connection_lost`, hint to attach again | Problem Details | 3 |

- Detaching, exiting or losing the connection restores the terminal's mode, leaves the alternate screen and shows the cursor, whatever the program inside had set, gives the window its title back, and puts the cursor on a new line at the bottom.

### `wdx terminal history`

- Prints history lines through the API; by default the last `terminal.attach_history`, or 1,000 when that is 0.
- Escape codes are kept when stdout gets color (`--color`, the `color` setting, as for our own output) and stripped otherwise; `--plain` always strips them. Agents and scripts get plain text.
- `--json`: the API's body.
- Works in any state, also after the shell exited.

## Settings

| Key | Default | |
|---|---|---|
| `terminal.history_lines` | `100000` | lines of history kept per terminal; `0` keeps none |
| `terminal.attach_history` | `1000` | history lines written on attach and printed by `terminal history` |

`terminal.history_lines` reaches the holder as an argument when the daemon starts it: a change applies to terminals started after it.

## Logs

- Holder: `client attached` and `client detached` (with the client's pid, from the socket's peer credentials, and the reason), `terminal resized`, `vt reset`, `history interrupted`, `history trimmed`. Never bytes, never history lines.
- CLI: as every command, from `command received` to `command returned`; the logged response is the final result.

## Dependencies

`github.com/charmbracelet/x/vt` and what it brings: `charmbracelet/ultraviolet`, `charmbracelet/x/ansi`, `rivo/uniseg`, `clipperhouse/uax29`, `lucasb-eyer/go-colorful`, `mattn/go-runewidth` and others, all MIT. `x/vt` is pre-1.0: we pin it, wrap it in `screen` so only one file uses it, and keep a round-trip test (below) that catches a regression on upgrade.

`x/vt` has bugs, gaps and a speed limit, found by the spike and step 2; docs/VT.md lists them with their repros, fixes and the workarounds in `screen`. Attach is built on upstream as is first; the fixes go to our fork `srjn45/x` afterwards, and upstream.

## Tests

- `screen`: recorded output of colored `ls`, wide characters and emoji, `less`, `top`, tmux with split panes and a zsh prompt is fed in, the snapshot fed into a fresh vt, and both compared cell by cell with cursor, modes and alternate screen (the spike's check). The panic input resets and marks a gap. History harvest: lines in order, alternate screen excluded, `clear` and `ESC[3J`.
- Spool and history files: positions, segments, trimming, the 256 MiB skip, numbering across relaunches.
- Holder, with real PTYs and `/bin/sh`: attach gets history, snapshot and output with nothing missed or doubled while output flows; input reaches the shell; latest-wins resize (`stty size`); several clients; a slow client is dropped; `exited` reaches clients; queries answered only without clients; a version 1 hello still works for the daemon.
- Daemon: history endpoint per outcome; directories deleted on terminal and project removal and swept at startup.
- CLI: `attach` and `history` per outcome with `--json`, the controlling terminal given by the test as a PTY through `cli.Env` (no `/dev/tty` in tests); `usage` without one; detach key; restore of the terminal's mode.

## Out of scope

tide, the web relay, a history mode inside `wdx terminal attach` (scrolling all history in place, like tmux's copy mode), reflow of history, resyncing slow clients, recording with timing, read-only attach, `wdx terminal send` for scripts to type into a terminal.

## Implementation plan

Each step builds and passes `make check` and `make test` on its own. All of them are done.

1. **Daemon accepts holder versions 1 and 2**, so that rolling back from the release that brings version 2 keeps its terminals. It needs no release of its own, but a release carrying it must be out and installed before step 4 is released, since a rollback returns to the binary installed before.
2. **`screen`** in `internal/holder`: the `x/vt` wrapper with mode tracking, snapshot, history harvest, recovery; `make notices`. Tests from recorded output.
3. **Spool and history files** in `internal/holder`: writers, readers, segments, trimming; the reader exported for the daemon.
4. **Holder version 2**: the reader writing the spool and queueing to clients, the vt goroutine, roles in `hello`, the new frames, size, queries, slow clients, `exited` to clients, `--history-lines`; the ring removed. The setting `terminal.history_lines`, which the daemon passes.
5. **Attach client** in `internal/holder`: `Attach(ctx, socket, id, size, history)` returning the history, the snapshot, an output reader, input and resize; used by the CLI now and tide later.
6. **API and daemon**: the history endpoint, the new codes, `terminal.attach_history`, deleting terminal directories on removal and sweeping them at startup.
7. **CLI**: `wdx terminal attach` and `wdx terminal history`, help, examples, completions; `cli.Env` gains the controlling terminal; tests per outcome.
8. **Docs**: TERMINALS.md's holder, protocol and API sections point here, AGENTS.md's layout line for `internal/holder`, V1_FEATURES.md.

## Decisions

Taken while designing and building it:

1. Clients dial the holder directly; the daemon will relay only for the web.
2. A headless vt in the holder keeps the screen; `charmbracelet/x/vt` upstream as is first, its fixes in the fork later (docs/VT.md).
3. `/dev/tty` for attach, keeping stdout for the result so `--json` works.
4. History on by default (0600, `terminal.history_lines: 0` turns it off).
5. Defaults: `terminal.history_lines` 100,000, `terminal.attach_history` 1,000, spool 2 × 8 MiB kept and 256 MiB before a gap, client queue 4 MiB.
6. A slow client is disconnected for now; resync comes with the web.
7. `ESC[3J` (clear scrollback, sent by `clear` in some setups) does not delete history.
8. The detach key is `ctrl-\`; the latest client to attach or resize sets the size.
9. The new codes `terminal_not_running`, `attach_unsupported`, `connection_lost`.
10. Step 1 in any release before step 4's, not necessarily its own.
