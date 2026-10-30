# tide Phase B3: terminals in the terminal pane

Status: planned. Built in the worktree `.worktrees/tide-attach`, branch `tide-attach`, rebased onto `main` to land.

What B3 builds: the terminal pane shows the terminal of the selected tab live, through `internal/termview` as `wdx terminal attach` does (docs/ATTACH_VIEW.md), and takes its keys, mouse and scrolling; switching tabs or projects attaches the terminal now selected. A terminal that is not running shows its last screen, read-only. The agent pane stays a placeholder. This file is the plan; docs/TIDE.md stays the design.

## Decisions

Agreed with the human (2026-10-30):

- **Only the selected tab is attached.** One session at a time: switching tabs detaches the terminal shown and attaches the one selected, which repaints from its snapshot. Nothing else of the project is attached, so memory and CPU do not grow with the number of terminals.
- **A terminal that is not running shows its last screen, read-only**: the end of its history, under a line with its state and its exit code or error, scrolled with PageUp / PageDown and the wheel. Keys do nothing else there.
- **The terminal stays attached while the pane is hidden** (`C-\`), as hidden panes keep running: showing it again is instant. Its size stays as it was while hidden.

Following from the design (docs/TIDE.md, docs/ATTACH_VIEW.md):

- **Keys.** While the terminal pane has the focus, every key goes to the program, encoded by the pane's vt for its modes, except tide's own: `C-q`, `C-]`, `C-\`, Alt+arrows (Alt+← / → switch tabs, Alt+↑ leaves the pane) and Alt+Shift+← / →. The attach view's prefix keys (`C-\ d`, `[`, `]`, `?`, `s`) are the CLI's, not tide's; `C-\` stays tide's terminal toggle. Pasting goes to the program (bracketed when it asks).
- **Scrolling** as in the attach view: PageUp / PageDown scroll a page while the program is on its main screen and go to it on the alternate screen; the wheel goes to a program that asked for the mouse, else scrolls the history over the main screen, else is sent as arrows. A key for the program returns to the live screen. While scrolled, the status line says `history 1234 of 56789 · PgDn down · any key: back`.
- **Mouse.** A program that asked for the mouse gets clicks and drags inside the pane, at the pane's coordinates; tide's own drag-to-copy works where it does not. Clicks on the frame, tabs and `[⤢]` stay tide's.
- **Size.** The terminal takes the pane's inner size when attached and whenever the pane changes size (docked, zoomed, the window resized), so the program redraws to fit; another client's size is drawn cropped or padded until a key takes it back, as in the attach view.
- **Cursor** is the program's while the terminal pane has the focus and the live screen shows; hidden otherwise.
- **After a project switch**, the selected terminal attaches once the selection has rested for about 200 ms, so tapping past projects with Alt+Shift+arrows attaches nothing on the way. Switching tabs attaches at once.
- **States.** While attaching the pane says "attaching…". When the terminal exits, its session ends and the pane turns to its last screen once the stream says so. A connection lost while the stream still says running is attached again after 0.5 s, doubling to 5 s, as the stream is reopened, with the reason in the pane meanwhile. A holder too old to attach says so in the pane, with the hint the CLI gives.
- **The tab's title** stays the terminal's name; the title the program sets is not shown for now.

## The last screen of a terminal that is not running

History holds the lines that scrolled off the main screen, so what the screen showed when the shell exited is in no file. The holder writes its main screen's lines to history when the shell exits, before telling the daemon, trailing blank lines left out. Then a terminal that is not running is all history: tide fetches its tail (`?tail=`) and the pane draws its last lines, fetching older pages as it scrolls, as the attach view does. `wdx terminal history` shows those lines too. A terminal `lost` (its holder died) has no such lines; it shows the history it has.

`termview` gets a read-only pane over history alone (no session): the same scrolling and fetching, no keys for a program, no cursor.

## Steps

Each step ends with `make check` and `make test` passing, and is committed when the human asks.

1. **`docs:`** this file and the docs/TIDE.md changes.
2. **`feat:` the holder writes the last screen to history when the shell exits.** Tests: a shell that prints and exits, its lines then in `history`; trailing blanks left out; the alternate screen's content never written.
3. **`feat:` termview shows history alone, read-only.** Tests with real history files: the tail drawn, scrolling fetches older pages, keys ignored.
4. **`feat:` tide attaches the selected terminal.** `Options` gets the config home for the holder socket; a `termview.Model` for the selected tab, its output messages routed by session; keys, paste and mouse to it; size from the pane; the cursor; detaching on tab switch, project switch (after the 200 ms rest) and quit; reattaching after a lost connection; the status line while scrolled. The placeholder inputs go.
5. **`feat:` tide shows a terminal that is not running** from its history, read-only, and turns to it when the terminal exits.
6. **`docs:`** docs/TIDE.md marks B3 implemented; this file records what was built.

Tests stay light (AGENTS.md): one holder at a time with the test shell, small panes, waits on sync points. In tide, follow_test.go's daemon with `holdertest`: a tab's terminal echoes what is typed; switching tabs shows the other terminal's screen; a terminal exiting turns to its last screen; zooming resizes the terminal (`stty size`). The cli's `TestTide` types into a terminal and sees the output.

## Progress

Nothing built yet.
