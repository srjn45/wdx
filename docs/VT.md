# The vt and its fork

wdx keeps terminals' screens in `github.com/charmbracelet/x/vt` (MIT), wrapped by `internal/screen`: the holder's copy of each terminal (docs/ATTACH.md), and each attached pane's in `internal/termview` (docs/ATTACH_VIEW.md). No other package uses the vt (a depguard rule). The vt parses with `github.com/charmbracelet/x/ansi`, in the same repository, which the fork also holds for #16; every package that uses `ansi`, Bubble Tea among them, builds with the fork's. This lists what is wrong with it, or missing, as found by the attach spike and by building `screen`. Each item is fixed in our fork, whose branch we merge into ourselves; once the changes are stable, they go upstream as one or a few pull requests.

- Fork: `github.com/srjn45/x`, branch `wdx`, cloned at `~/dev/x`; `upstream` is `charmbracelet/x`.
- Every change on the fork, with the problem that made it: docs/VT_FORK.md.
- wdx uses the fork: `go.mod` has `replace github.com/charmbracelet/x/vt => github.com/srjn45/x/vt <version>` and the same for `ansi`, both the branch's last pushed commit, with `make notices` run after each move. It forks upstream at `v0.1.1-0.20261028143050-ac49f62f807e`. Each `replace` goes when upstream has its module's fixes.
- When a fix lands, remove its workaround in `screen` (the last column) and its test's comment, keeping the test.

## Keeping the fork current

While wdx depends on the fork, its branch `wdx` is rebased onto upstream's `main` on request, and before every release (`make release-dev`, `make release`), by `make vt-sync` (`scripts/vt-sync.sh`, decided 2026-10-30), run from a wdx worktree, with the fork's checkout in `VT_FORK` (default `~/dev/x`):

1. Fetch upstream and fast-forward the fork's `main` to `upstream/main`, pushed: the fork's `main` mirrors upstream.
2. Stop when `wdx` already contains `upstream/main`.
3. Tag `wdx`'s tip `wdx-<UTC date>-<time>` and push the tag: a rebase rewrites the branch, and the tags keep on the fork every commit wdx has pinned.
4. Rebase `wdx` onto `upstream/main`. A conflict stops the sync, to be resolved by hand. A fix upstream took is dropped by the rebase; the script lists them, for the tables below and to remove the fix's commit from what goes upstream.
5. Run the tests of the vt and `ansi` with the race detector, then push `wdx` with `--force-with-lease`.
6. Move both `replace`s in `go.mod` to the new tip; `go mod tidy`, `make notices`, `make check`, `make test`.
7. Commit `chore: build the vt and ansi from the fork at <tip>, on upstream <sha>` in the worktree, then rebase it into `main`.

Run again after resolving a conflict, it finishes the sync. It also pushes commits made on `wdx` and not pushed yet, once the tests pass, and moves wdx to them.

## Plan

Approved on 2026-10-30, in the order users notice. Each fix is a commit in the fork's branch `wdx` with its test; wdx then moves its `replace` to the fork's new version, runs `make notices`, and removes the fix's workaround in `screen`, in a commit of its own. Each group ships in a dev release.

1. **What an attach shows.** #11: a snapshot paints the main screen, then enters the alternate one, so quitting less or vim after an attach shows the shell's output. #10: the snapshot replays the cursor's pen and link, the character sets and the left and right margins. **Done**: the fork's `02f5ccb` (both screens) and `44d7b55` (the character sets), with #15, found on the way; `screen` reads the margins from the vt (part of #9). The reset tests no longer have an input the vt fails on: `screen.FailOn` makes one.
2. **History.** #3: lines go to history only when scrolled out of a region at the screen's top, as xterm; never on `DL`. **Done**: the fork's `83b7692`.
3. **Memory.** #1: the alternate screen has no scrollback. **Done**: the fork's `8cd959a`.
4. **Workarounds.** #4, #5, #6; #9's getters, so `screen` stops tracking the modes and title itself. `screen`'s tests stay. **Done**: the fork's `3e7b84b`, `ab55c1a`, `0b22536` and `8a7127d`. `screen` keeps its recovery from a vt panic, though no known input causes one: a panic in the holder would end the terminal's session.

Later:

- **Speed, deferred.** #7 lives in `charmbracelet/ultraviolet`, a repository of its own: it needs a fork `srjn45/ultraviolet` and a second `replace`, run as above. With #8, which alone gains `screen` little, it takes typical output from about 1.5 MB/s to about 5.5 MB/s, the holder's CPU under heavy output. A fork of `ultraviolet` reaches further than the vt's: Bubble Tea draws and reads input with it, and so does tide, so a `replace` changes every screen wdx draws; offering the fix upstream as a pull request comes first. Taken up when that cost shows, or when upstream takes the fix.
- **Features.** #12 (reflowing history to a new width), #13 and #14, each discussed on its own.
- **Upstream.** The fork's fixes are offered to `charmbracelet/x` one per pull request, each from a branch off upstream's `main` with that one commit cherry-picked, never from `wdx`, which carries them all: #1027, #1028 and #1029, opened 2026-11-04. A fix that an open upstream PR from someone else already makes is not offered again: its row names that PR, and `make vt-sync` drops our commit once upstream merges either. The getters (#9, #10, #11) and the kitty keyboard protocol (#18) are features, which charm's CONTRIBUTING.md asks to go through a GitHub Discussion before a PR: #1030 for the getters and #1031 for the kitty protocol, opened 2026-11-04; a PR for each follows a maintainer's answer.

## Bugs

| # | Bug | Repro | Fix | Workaround in `screen` | Status |
|---|---|---|---|---|---|
| 1 | The alternate screen keeps its own scrollback, up to 10,000 lines, that no API reaches or clears | Enter `CSI ?1049h`, print 20,000 lines, leave: 107 MiB held for good | `NewEmulator` gives the alternate screen no scrollback (`scrs[1].scrollback = nil`; `Screen` already checks for nil) | none: accepted | fixed in the fork (`8cd959a`), used by wdx; upstream has the same fix open as #948, so ours is not offered and drops at the sync that takes it |
| 2 | A bottom or right margin past the screen panics on the next scroll | 80×24: `CSI 1;30r`, `CSI 30;1H`, `CSI S` | Clamp the stored margins to the screen; ignore a top or left margin past it | `feed` recovers, resets the vt, feeds the rest of the piece a byte at a time and reports a gap | fixed in the fork (`68419ef`), used by wdx; upstream has the same fix open as #1003 (and #954, #956, #908), so ours is not offered |
| 3 | Lines scrolled out of a region that does not start at the top go to scrollback; so do lines deleted by `DL` (`CSI M`) at the region's top | `CSI 2;3r`, `CSI 3;1H`, print lines | Push to scrollback only when scrolling (index, `SU`) a region whose top is the screen's top, as xterm; never on `DL` | none: such lines appeared in history | fixed in the fork (`83b7692`), used by wdx; offered upstream as #1027 |
| 4 | The `CursorStyle` callback is given "steady" as its `blink` argument | `CSI 6 q`: the callback says blink | Pass `blink` in the vt's `Screen.setCursorStyle` | the callback read `CursorStyle()` back | fixed in the fork (`3e7b84b`), used by wdx; upstream has the same fix open as #986 and #1006, so ours is not offered |
| 5 | `Close` sets `closed` while `Read` reads it in another goroutine: a data race | Read answers in one goroutine, `Close` in another | Guard `closed` with the pipe, or a mutex or atomic | `Close` closed the answer pipe's writer instead of the vt | fixed in the fork (`ab55c1a`), used by wdx; upstream has the same fix open as #881, so ours is not offered |
| 6 | `SetScrollbackSize(0)` is ignored: the scrollback stays at 10,000 | `SetScrollbackSize(0)`, scroll: lines kept | 0 disables the scrollback | not needed: `screen` empties it after every piece | fixed in the fork (`0b22536`); offered upstream as #1029 |
| 15 | Shift out and shift in (`SO`, `SI`) do nothing: their handlers are registered among the C1 controls, which never reach them | `ESC ) 0`, `SO`, `q`: prints `q`, not `─` | Register them with the C0 controls | none | fixed in the fork (`c3a44c3`), used by wdx; offered upstream as #1028 (#1008 carries the same fix among 19 commits) |
| 16 | An OSC or DCS string ends at any `0x9C`, the C1 string terminator, even a byte inside a UTF-8 character, and the rest prints on the screen; in `x/ansi`'s parser. Claude Code's idle title starts with `✳` (`E2 9C B3`): the title showed at its prompt, and stayed, as it redraws only cells it thinks changed | `OSC 0 ; ✳ title BEL` | Bytes that continue a UTF-8 character are text; a `0x9C` elsewhere still ends the string, and so does `C2 9C` (U+009C). `ansi`'s functions that read sequences without the parser (`DecodeSequence`, `Strip`, `Truncate`, `Wrap`) still end at any `0x9C`, in the strings of text they are given | none | fixed in the fork (`59a343f`), used by wdx; upstream has the same fix open as #946 (and #976, #1008), so ours is not offered |
| 17 | A key pressed with Shift, Alt or Ctrl that has no character (an arrow, Home, End, Insert, Delete, PageUp, PageDown, F1 to F12) is sent as `Esc` and the key without its modifiers, or as `Esc` alone: `SendKey` matches only unmodified keys after taking Alt off | Alt+Up: `ESC ESC O A`; Alt+Shift+Left: `ESC`; Ctrl+Right: nothing | Encode them as xterm: `CSI 1 ; m A` to `D`, `H`, `F`, `P` to `S`, and `CSI n ; m ~`, with `m` 1 plus the modifiers' bits | `screen.SendKey` encodes them itself (2026-11-02) | not fixed in the fork |

## Speed

About 1.5 MB/s of typical output; about 5.5 MB/s with both fixes below (spike). Short lines are slower, since each scrolled line costs the same: `seq` reaches about 75,000 lines/s, about 0.6 MB/s (measured once, 2026-10-30).

The holder logs what the speed costs. The shell never waits for the vt, which reads the output from the spool at its own pace; when it falls 8 MiB behind, about 5 s of its work, the holder logs `vt fell behind` (`lag_bytes`), and when it catches up, `vt caught up` with how long it was behind (`duration_ms`), the most (`max_lag_bytes`), what it parsed meanwhile (`bytes`) and its rate on that output (`bytes_per_second`). Meanwhile history comes late and an attach shows a screen from the past. Past the spool's backlog (256 MiB), it skips ahead: `history interrupted: the screen fell too far behind`, and a gap in history. The speed work is due when, in normal use:

- history is interrupted for being too far behind, even once; or
- `vt fell behind` shows up most weeks, or an episode lasts more than about 10 s; or
- a shell's exit waits for the vt long enough to notice: the `duration_ms` of `screen kept in history`, as the exit is told only after it (docs/ATTACH.md).

`docs/OBSERVABILITY.md` has the query.

| # | Cause | Fix | Where |
|---|---|---|---|
| 7 | Every scrolled line moves the screen up cell by cell (`Buffer.DeleteLineArea`) | For a full-width area, move the line slices and reuse the deleted ones, marking the moved lines touched | `charmbracelet/ultraviolet`, a separate repository: needs a fork of its own |
| 8 | A full scrollback drops its oldest line with `slices.Delete`, moving all 10,000 | A ring buffer, or reslicing and nil'ing the dropped line | `x/vt` `scrollback.go`; does not affect `screen`, which keeps the scrollback near empty |
| 19 | Every emulator allocated the parser's string buffer whole, 4 MB, though almost none ever fills it, and every `screen.New` and `Screen.Reset` makes an emulator: the 35 alive at the cli tests' heap peak were 140 MB of a 220 MB live heap, 72% of it (found 2026-11-04, measuring `go test -race ./internal/cli/` for docs/FLAKES.md's memory-cap row) | Done in the fork (`dfddc21`), used by wdx (`b5dcca0`): the size is a ceiling, and the buffer starts empty and doubles as a string's data arrives, never past it; what came past was dropped before too. The cli test binary's live heap peak went from 205 MB to 80 MB and its heap from 348 MB to 149 MB (`GODEBUG=gctrace=1` on the binary alone, one run each); its process peak under `-race`, mostly not heap, from 1.5–2.4 GB to 1.3–1.7 GB across three runs each on a shared machine | `x/ansi` `parser.go`, upstream's own code: issue #973 and PR #1001 upstream ask for a knob instead; ours to offer once it has run in wdx a while |

## Missing

What `screen` has to track itself, or cannot do at all.

| # | Missing | Effect today | Wanted |
|---|---|---|---|
| 9 | Getters for the modes, title and scroll margins | Done in the fork (`8a7127d`): `Modes()`, `Title()`, and each screen's `ScrollRegion()` through #11; `screen` reads them all from the vt | — |
| 10 | The character sets; the cursor's pen (current style and link) and left/right margins are on the vt's screens, which #11 reaches | Done in the fork (`44d7b55`): `CharSets()`; a snapshot carries all of them | — |
| 11 | The main screen's cells while the alternate screen is active | Done in the fork (`02f5ccb`): `MainScreen()`, `AltScreen()`; a snapshot under less or vim paints the main screen first, so it shows after the program quits | — |
| 12 | A soft-wrap flag on lines | History cannot be reflowed to another width; a long line wrapped at 80 columns stays two lines | Mark lines that continue on the next |
| 13 | Compact scrollback lines | A line costs ~9.4 KB (a full `uv.Cell` per column); not a problem for `screen`, which keeps none | Smaller cells for stored lines |
| 14 | One scroll region per terminal | The vt keeps one per screen (main and alternate), unlike xterm; `screen` tracks both | One region, as xterm |
| 18 | The kitty keyboard protocol: a program could not ask for it, and `Shift+Enter` was sent as nothing | Done in the fork (`4d06ff8`, `5fddd20`) for levels 1 and 8: each screen's stack of flags, `CSI ? u` answered, keys encoded by the flags, `KittyFlags()`; a snapshot carries both stacks (docs/KITTY_KEYS.md) | Levels 2, 4 and 16, and xterm's modifyOtherKeys, when a program needs them |

## Not bugs

- `ED 3` clears the scrollback, as xterm: history keeps what scrolled off, so `screen` harvests the scrollback before the vt clears it. Keep the workaround.
- `ED 2` pushes the screen's non-empty lines to scrollback before clearing (blank lines are dropped): what `clear` leaves in history.
- `Write` blocks until the vt's answers to queries are read: `screen` reads them in a goroutine.
