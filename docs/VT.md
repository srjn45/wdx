# The vt and its fork

wdx keeps terminals' screens in `github.com/charmbracelet/x/vt` (MIT), wrapped by `internal/screen`: the holder's copy of each terminal (docs/ATTACH.md), and each attached pane's in `internal/termview` (docs/ATTACH_VIEW.md). No other package uses the vt (a depguard rule), so the fork is one `replace`. This lists what is wrong with it, or missing, as found by the attach spike and by building `screen`. Each item is to be fixed in our fork and offered upstream.

- Fork: `github.com/srjn45/x`, branch `wdx`, cloned at `~/dev/x`; `upstream` is `charmbracelet/x`.
- wdx uses upstream for now: `x/vt` at `v0.1.1-0.20261028143050-ac49f62f807e` and `ultraviolet` at `v0.0.0-20260303162955-0b88c25f3fff`. Once the fork has fixes worth having, `go.mod` gets `replace github.com/charmbracelet/x/vt => github.com/srjn45/x/vt <version>`, and `make notices`. Each fix also goes upstream as a pull request; the `replace` goes when upstream has them all.
- When a fix lands, remove its workaround in `screen` (the last column) and its test's comment, keeping the test.

## Bugs

| # | Bug | Repro | Fix | Workaround in `screen` | Status |
|---|---|---|---|---|---|
| 1 | The alternate screen keeps its own scrollback, up to 10,000 lines, that no API reaches or clears | Enter `CSI ?1049h`, print 20,000 lines, leave: 107 MiB held for good | `NewEmulator` gives the alternate screen no scrollback (`scrs[1].scrollback = nil`; `Screen` already checks for nil) | none: accepted | open |
| 2 | A bottom or right margin past the screen panics on the next scroll | 80×24: `CSI 1;30r`, `CSI 30;1H`, `CSI S` | Clamp the stored margins to the screen; ignore a top or left margin past it | `feed` recovers, resets the vt, feeds the rest of the piece a byte at a time and reports a gap | fixed in the fork (`68419ef`), not pushed |
| 3 | Lines scrolled out of a region that does not start at the top go to scrollback; so do lines deleted by `DL` (`CSI M`) at the region's top | `CSI 2;3r`, `CSI 3;1H`, print lines | Push to scrollback only when scrolling (index, `SU`) a region whose top is the screen's top, as xterm; never on `DL` | none: such lines appear in history | open |
| 4 | The `CursorStyle` callback is given "steady" as its `blink` argument | `CSI 6 q`: the callback says blink | Pass `blink` in the vt's `Screen.setCursorStyle` | the callback reads `CursorStyle()` back | open |
| 5 | `Close` sets `closed` while `Read` reads it in another goroutine: a data race | Read answers in one goroutine, `Close` in another | Guard `closed` with the pipe, or a mutex or atomic | `close` closes the answer pipe's writer instead of the vt | open |
| 6 | `SetScrollbackSize(0)` is ignored: the scrollback stays at 10,000 | `SetScrollbackSize(0)`, scroll: lines kept | 0 disables the scrollback | not needed: `screen` empties it after every piece | open |

## Speed

About 1.5 MB/s of typical output; about 5.5 MB/s with both fixes below (spike).

| # | Cause | Fix | Where |
|---|---|---|---|
| 7 | Every scrolled line moves the screen up cell by cell (`Buffer.DeleteLineArea`) | For a full-width area, move the line slices and reuse the deleted ones, marking the moved lines touched | `charmbracelet/ultraviolet`, a separate repository: needs a fork of its own |
| 8 | A full scrollback drops its oldest line with `slices.Delete`, moving all 10,000 | A ring buffer, or reslicing and nil'ing the dropped line | `x/vt` `scrollback.go`; does not affect `screen`, which keeps the scrollback near empty |

## Missing

What `screen` has to track itself, or cannot do at all.

| # | Missing | Effect today | Wanted |
|---|---|---|---|
| 9 | Getters for the modes, title, scroll margins | `screen` tracks them through callbacks and its own handlers, mirroring the vt's logic | `Modes()`, `Title()`, `ScrollRegion()` |
| 10 | The cursor's pen (current style and link), character sets, left/right margins | A snapshot does not carry them: a program writing on after an attach with a style set but not repeated, or in line-drawing characters, looks wrong until it sets them again | Getters for the current screen's cursor, pen and charsets |
| 11 | The main screen's cells while the alternate screen is active | A snapshot taken under `less` or vim paints only the alternate screen: after the program quits, the client's main screen is blank where the shell's output was | Access to both screens' cells |
| 12 | A soft-wrap flag on lines | History cannot be reflowed to another width; a long line wrapped at 80 columns stays two lines | Mark lines that continue on the next |
| 13 | Compact scrollback lines | A line costs ~9.4 KB (a full `uv.Cell` per column); not a problem for `screen`, which keeps none | Smaller cells for stored lines |
| 14 | One scroll region per terminal | The vt keeps one per screen (main and alternate), unlike xterm; `screen` tracks both | One region, as xterm |

## Not bugs

- `ED 3` clears the scrollback, as xterm: history keeps what scrolled off, so `screen` harvests the scrollback before the vt clears it. Keep the workaround.
- `ED 2` pushes the screen's non-empty lines to scrollback before clearing (blank lines are dropped): what `clear` leaves in history.
- `Write` blocks until the vt's answers to queries are read: `screen` reads them in a goroutine.
