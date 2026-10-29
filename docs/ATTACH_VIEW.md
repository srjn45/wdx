# The attach view

Status: implemented. Replaced how `wdx terminal attach` shows a terminal (docs/ATTACH.md, CLI); the holder, its protocol, history and the API stay as they are.

`wdx terminal attach` is a full-window app, as tmux is: it takes over the terminal it runs in, shows the attached terminal in a pane above a status line, scrolls through the whole history inside the pane, and on detach gives the window back exactly as it was. Nothing of the session is left behind in the calling terminal, which never sees what happened in the attached one. The terminal keeps running, with its history, attached or not, as many times as it is attached and detached.

The pane is a component of its own, `internal/termview`, which tide reuses for its terminal and agent panes.

## Why it draws itself

Attach used to pass the holder's bytes straight to the calling terminal and write history into that terminal's scrollback, so the session stayed in it after detaching. Running on the alternate screen alone would not fix that: the bytes come from programs that switch screens themselves, and vim's "leave the alternate screen" would drop the calling terminal back to its own screen mid-session. So, as tmux does, the client keeps its own vt, fed by the holder's snapshot and output, and draws that vt: the calling terminal only ever receives what the app draws.

## What it looks like

```
┌──────────────────────────────────────────────────────────┐
│ $ go test ./...                                          │
│ ok   github.com/srjn45/wdx/internal/holder   6.5s        │  the pane: the terminal's screen,
│ ...                                                      │  or its history when scrolled
│ $ █                                                      │
│ api · warden-rebuild                   C-\ ? for keys    │  the status line
└──────────────────────────────────────────────────────────┘
```

- The pane fills the window but for the status line; the terminal takes the pane's size (the latest client to attach or resize sets it, as now).
- The status line shows the terminal and its project on the left, and on the right what the keys do now: `C-\ ? for keys`; `C-\ …` once the prefix is pressed; `history 1,234 of 56,789 · PgDn or C-\ ] down · any key: back` while scrolled. Its color tells the state, in the terminal's basic colors: black on green, yellow after the prefix, cyan while scrolled, white on red when history cannot be fetched; reverse video with color off, so it still stands apart. Later it can show more (state, size, other clients). `C-\ s` hides and shows it for the session.
- The calling terminal's title becomes the terminal's, as programs inside set it, and is given back on detach.
- Detaching leaves the alternate screen: the calling terminal shows what it showed before, and the result line (`detached from terminal api in warden-rebuild`, or `--json`) is printed after it, as now.

## Keys

Every key goes to the program in the terminal, except:

| Keys | Does |
|---|---|
| `C-\` then `d` | detach |
| `C-\` then `[` / `]` | scroll a page up / down through history |
| `C-\` then `?` | show every shortcut, over the pane; any key closes it |
| `C-\` then `s` | hide or show the status line |
| `C-\` then `C-\` | send `C-\` to the program |
| PageUp / PageDown | scroll a page, when the program is on its main screen (the shell); on the alternate screen (less, vim, htop) they go to the program |
| mouse wheel, touchpad | scroll 3 lines; see Mouse |

- `C-\` is the prefix, as tmux's `C-b`: one ctrl key, which nothing common uses (it sends SIGQUIT in a shell), and a plain key after it. After the prefix the status line says so, and a key with no shortcut does nothing. `--prefix-key` changes it (`ctrl-<letter>` or `ctrl-]`).
- Arrow keys never scroll: they go to the program, for the shell's history and the line being edited.
- While scrolled back, a key that is not a scroll key returns to the live screen and goes to the program, as in a terminal's own scrollback; so does scrolling past the bottom. New output does not pull the view down.
- `C-?` (ctrl+shift+/) for the shortcut list: most terminals send it as DEL, the same byte as Backspace, so it cannot be told apart. Where the terminal speaks the kitty keyboard protocol (kitty, WezTerm, foot, Ghostty), which Bubble Tea can ask for, `C-?` opens the list too; `C-\ ?` works everywhere.

## Mouse

- A program that asked for the mouse (vim, htop, less with `--mouse`) gets it, wheel included, as it would in a terminal.
- Otherwise the wheel scrolls the history when the program is on its main screen; on the alternate screen it is sent as arrow keys, which is what terminals do so that less and man scroll with the wheel.
- The app takes the mouse, so selecting text to copy needs Shift+drag (Shift+Option on macOS terminals), as in tmux. Selecting and copying inside the pane, and pasting, are for later; pasting with the terminal's own paste works (bracketed when the program asks).

## Scrolling through history

- Every line is addressed by its history number (docs/ATTACH.md). The snapshot says which number the screen continues (`history_lines`), so the lines the pane's vt scrolls off after attaching take the next numbers, as the holder's vt gives them the same numbers from the same output.
- The pane keeps the last 10,000 lines it scrolled off; older ones, and everything from before attaching, are fetched from the daemon (`GET /v1/terminals/{terminal}/history?from=&count=`) a page of 1,000 at a time as the view nears them, with a few pages cached. Scrolling to the top of a 100,000-line history loads what is looked at, not all of it.
- History holds the main screen's lines only: what vim or less show never enters it (docs/ATTACH.md).
- Lines are shown as drawn, at the width they were drawn at: a narrower pane cuts them, a wider one shows them short (no reflow).
- A gap marker (`── history interrupted ──`) and restart markers show as they are written.

## The pane: `internal/termview`

A Bubble Tea v2 model, `termview.Model`, around a `holder.Session` and a vt (`charmbracelet/x/vt`):

- a goroutine reads the session and hands the output to the model in messages; the model feeds its vt and asks for a frame. Bubble Tea draws at most 60 frames a second from the vt's screen, so a flood is drawn as it stands, not byte by byte (the vt draws through `ultraviolet`, as Bubble Tea v2 does; step 1 settles how a frame takes it);
- keys and mouse events are encoded by the vt for the program inside (`SendKey`, `SendMouse`, `Paste`), honouring its modes (application cursor keys, mouse reporting, bracketed paste), and the vt's input stream is written to the session; the vt also answers the program's queries (cursor position, colors) on that stream, which the holder leaves to an attached client;
- a window size message resizes the vt and the session;
- the cursor, its shape and the title come from the vt;
- scrolling and fetching history as above; the model takes a function that fetches history lines, so it knows nothing of the API.

The CLI runs it full-window with the status line and the prefix keys; tide runs several in its layout, with keys of its own. The model knows nothing of either.

The vt's known issues (docs/VT.md) now show on the client too: its speed bounds how fast a flood is drawn, and a panic resets the pane's vt to an empty screen, which the program's next drawing fills (a shell's next prompt, a full-screen program's next redraw); attaching again would repaint it at once.

## Changes to `wdx terminal attach`

- `--history` goes: nothing is written to the calling terminal's scrollback. `terminal.attach_history` becomes `terminal.history_tail`, the lines `wdx terminal history` prints by default.
- `--detach-key` becomes `--prefix-key`; detaching is `C-\ d`.
- `--json`, the results (`detached`, `exited`) and the error codes stay. The terminal must be at least 10 columns and 3 rows, else `usage`.
- Both changes break scripts that pass the flags or set the setting: `feat!:` with a `BREAKING CHANGE:` footer.

## Logs

The CLI logs attach and detach as now, scroll mode entered and history pages fetched (from, count), and pane resets. Never keys, output or history lines.

## Dependencies

`charm.land/bubbletea/v2` (MIT) and what it brings; it draws through `charmbracelet/ultraviolet`, already in for the vt. The status line uses `internal/style`. `make notices`.

## Tests

- `termview`, with real data: recorded output and a history fetcher reading real history files. Rendering the screen, keys encoded per mode (application cursor keys, bracketed paste), mouse passed or used for scrolling, scrolling across the live screen and fetched pages, line numbers continuing the snapshot's, resize.
- CLI, in a PTY as now: the alternate screen entered and left with the calling screen intact, `C-\ d` detaches, `C-\ C-\` sends one `C-\`, PgUp scrolls at the shell and goes to less on the alternate screen, the status line, `C-\ ?`, `--prefix-key`, `--json` results and errors.

## Out of scope

Selecting and copying inside the pane, searching history, several panes in the CLI (tide), more in the status line, a setting for the prefix key.

## Implementation plan

1. **`termview` pane**: the model, output, rendering, key and mouse encoding through the vt, resize, queries; tests; `make notices`.
2. **Scrolling**: line numbers, the pane's own scrolled lines, fetched pages, the wheel and scroll keys in the model; tests.
3. **CLI**: attach runs the pane full-window on the calling terminal, with the status line, the prefix keys, the shortcut list and detaching; `--prefix-key`; `--history` and `--detach-key` removed and `terminal.history_tail`; tests.
4. **Docs**: ATTACH.md's CLI section and VT.md (the vt is now used by `termview` too).

All of them are done.

## Decisions

1. The shortcut list is `C-\ ?`; `C-?` too where the terminal tells it apart (kitty keyboard protocol), since elsewhere it is Backspace.
2. PageUp/PageDown scroll only while the program is on its main screen; in less, vim or htop they go to the program. `C-\ [` and `]` always scroll.
3. The wheel goes to a program that asked for the mouse; on the alternate screen otherwise it is sent as arrow keys, as terminals do.
4. `--history` and `--detach-key` removed, `--prefix-key` added, `terminal.attach_history` renamed `terminal.history_tail`: breaking.
5. The status line is on by default and `C-\ s` toggles it for the session; a setting for it later.
6. Copying needs Shift+drag for now.
7. A key with no shortcut after the prefix does nothing (tmux too); no timeout on the prefix.
