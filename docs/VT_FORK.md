# What wdx changed in its fork of x/vt, and why

wdx keeps every terminal's screen in a headless terminal emulator, `github.com/charmbracelet/x/vt`, and parses its output with `github.com/charmbracelet/x/ansi`, from the same repository. Both come from our fork, `github.com/srjn45/x`, branch `wdx`, which `go.mod` names in two `replace` lines. This file lists every change on that branch, oldest first, with the problem that made us change it. docs/VT.md says how the fork is kept current (`make vt-sync`) and keeps the tables of bugs, speed and missing pieces; this file is the story of the commits themselves.

Every change is one commit with its test. The hashes are those of the branch as of 2026-11-04 (`dfddc21`); `make vt-sync` rebases the branch onto upstream and rewrites them, so look a commit up by its title if a hash no longer exists. Tags `wdx-<date>-<time>` on the fork keep every tip wdx has ever pinned.

## Why a fork at all

wdx uses the vt in a way its authors did not design for first: not to show a terminal, but to keep one. A holder process follows a shell's output with a vt for as long as the shell runs, days on end, and when a person attaches, wdx repaints that vt's screen in their real terminal and must leave the program running in it none the wiser (docs/ATTACH.md). That needs the vt to be right about what a program did, to hold no memory it does not need, and to let wdx read back state that a displaying terminal would only ever act on. Each change below is one of those three.

Fixes go upstream one per pull request, from a branch off upstream's `main`; a fix someone else already has open upstream is not offered again. Features wait on a GitHub Discussion, as charm's CONTRIBUTING.md asks.

## The changes

### Keeping the vt right about what a program did

**`1514f04` fix(vt): keep scroll margins within the screen** (2026-10-29)
A program setting a scroll region past the screen's bottom or right, as `CSI 1;30r` on 24 rows, made the next scroll index past the buffer and the holder panic, taking the terminal with it. The margin now ends at the screen's edge; a top or left margin past the screen is ignored. Not offered upstream: #1003 (and #954, #956, #908) fix the same thing.

**`eb0f00a` fix(vt): handle shift out and shift in** (2026-10-30)
`SO` and `SI` switch between the G0 and G1 character sets, the way older programs draw box lines with the DEC special graphics set. Their handlers were registered in the loop over C1 controls, which never reaches two C0 bytes, so `SO` did nothing and lines came out as letters. Offered upstream as #1028.

**`889aefc` fix(vt): push to the scrollback only lines scrolled off the screen's top** (2026-10-30)
Lines scrolled out of a region below the screen's top, and lines deleted by `DL` at a region's top, went to the scrollback, so a program with a status line or one deleting lines while redrawing filled wdx's history with lines that never left the screen. As in xterm, only a full-width region starting at the top feeds the scrollback, and only when it scrolls. Offered upstream as #1027.

**`eb0b774` fix(vt): give the cursor style callback the cursor's blink** (2026-10-30)
The `CursorStyle` callback's `blink` argument was the opposite of the truth, so wdx showed a blinking cursor as steady and a steady one blinking. Not offered upstream: #986 and #1006 fix the same thing.

**`09f7f7b` fix(vt): close the terminal without racing a reader** (2026-10-30)
The holder reads the vt's answers to a program's queries in one goroutine and closes the vt from another; `closed` was a plain bool read by both, a data race the race detector flagged in our tests. It is atomic now. Not offered upstream: #881 fixes the same thing.

**`4724e9a` fix(ansi): keep a UTF-8 character with a 0x9C byte in an OSC or DCS string** (2026-10-30)
The parser ended an OSC or DCS string at any byte `0x9C`, the C1 string terminator, even one inside a UTF-8 character. Claude Code's idle title starts with `✳` (`E2 9C B3`): the title was cut at `\xe2` and the rest of it printed on the screen at the prompt, where it stayed. A byte that continues a character is text now; a lone `0x9C`, and `C2 9C` (U+009C), still end the string. Not offered upstream: #946 (and #976, #1008) fix the same thing.

### Holding no memory the vt does not need

A holder lives as long as its shell, and wdx keeps one vt per terminal plus one per pane showing it, so what the vt holds for nothing is held many times over, for good.

**`2204d30` fix(vt): keep no scrollback on the alternate screen** (2026-10-30)
The alternate screen, which `less`, `vim` and Claude Code use, kept its own scrollback of up to 10,000 lines that no API reached or cleared: a program printing a lot there left over 100 MiB behind. xterm keeps nothing scrolled off the alternate screen, and now neither does the vt. Not offered upstream: #948 fixes the same thing.

**`3cb2f7b` fix(vt): disable the scrollback when its size is set to 0** (2026-10-30)
wdx keeps a terminal's history itself and reads lines off the vt as they scroll, so it wants no scrollback at all, but `SetScrollbackSize(0)` was ignored and the default 10,000 lines stayed. 0 or less now disables it, and a later size enables it again. Offered upstream as #1029.

**`dfddc21` perf(ansi): allocate the parser's data buffer as a string's data arrives** (2026-11-04)
Every emulator told its parser to keep a 4 MB buffer for OSC and DCS strings and the parser allocated it whole, up front, though almost no string ever needs it. Measuring the cli tests for docs/FLAKES.md's memory-cap row found the 35 emulators alive at the heap's peak were 140 MB of a 220 MB live heap. The size is a ceiling now: the buffer starts empty, doubles as data arrives and never passes the size; what came past was dropped before too. The cli test binary's live heap peak went from 205 MB to 80 MB. Offered upstream as PR #1032, closing issue #973; PR #1001 asks for a knob instead.

### Reading state back, to repaint the terminal elsewhere

A terminal that displays only has to act on what a program sets; wdx has to read it back later, at attach time, and set it again in the person's terminal (docs/ATTACH.md, docs/KITTY_KEYS.md). The vt reached this state only through callbacks, which tell of changes as they happen, so wdx would have had to mirror the emulator's state itself. These are getters, new API, so they go upstream through a Discussion first: #1030, opened 2026-11-04.

**`5b44726` feat(vt): give access to the main and alternate screens** (2026-10-30)
Only the active screen was reachable. Attaching while `less` had the alternate screen painted just that, and when `less` quit the shell came back blank, as the main screen behind it was never painted. `MainScreen()` and `AltScreen()`; a snapshot paints the main screen first.

**`002f129` feat(vt): report the character sets** (2026-10-30)
`CharSets()` returns how G0 to G3 were designated and which of them GL and GR invoke, so a program drawing lines in the DEC special set goes on drawing lines after an attach. The vt now remembers the final byte of each designation, which it did not keep before.

**`034ba5e` feat(vt): report the modes and the window title** (2026-10-30)
`Modes()` returns a copy of the terminal's modes (autowrap, bracketed paste, cursor keys, mouse reporting, the cursor's visibility...) and `Title()` the window title, so the attaching terminal is put in the same modes and shows the same title.

### Keys the kitty keyboard protocol disambiguates

**`4d06ff8` feat(vt): keep the kitty keyboard flags** and **`5fddd20` feat(vt): send keys by the kitty keyboard flags, and report them** (2026-11-04)
A program in a wdx terminal could not use the kitty keyboard protocol: the vt did not answer `CSI ? u`, dropped a push, and `SendKey` knew only the legacy encodings, so Shift+Enter was sent as nothing at all. Claude Code asks `CSI ? u` at startup and enables the protocol only when answered, so on a Mac with iTerm2 Shift+Enter never reached it. The vt now keeps each screen's stack of flags (levels 1 and 8 only, 16 deep, as the protocol says), answers the query, encodes keys by the flags in force (Shift+Enter is `CSI 13;2u`), and `KittyFlags()` lets a snapshot carry the stacks to the attaching terminal. Design in docs/KITTY_KEYS.md. New behaviour, so upstream through Discussion #1031, opened 2026-11-04.

## Where each change stands upstream

| Change | Upstream |
|---|---|
| scroll margins, SO/SI... see above | — |
| `889aefc` scrollback only from the top | PR #1027 |
| `eb0f00a` SO/SI | PR #1028 |
| `3cb2f7b` scrollback size 0 | PR #1029 |
| `1514f04`, `eb0b774`, `09f7f7b`, `2204d30`, `4724e9a` | someone else's open PR makes the same fix (#1003, #986/#1006, #881, #948, #946); ours drop from the branch at the `make vt-sync` that follows their merge |
| `5b44726`, `002f129`, `034ba5e` getters | Discussion #1030; a PR once a maintainer answers |
| `4d06ff8`, `5fddd20` kitty | Discussion #1031; a PR once a maintainer answers |
| `dfddc21` parser buffer | PR #1032, closing #973; #1001 is a knob for the same 4 MB |

When upstream takes a change, `make vt-sync` drops our commit, and when both modules' changes are all upstream the two `replace` lines go and so does the fork.
