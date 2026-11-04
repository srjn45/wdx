# Opening from the left pane: stay or go

Status: approved by the human on 2026-11-03; built on 2026-11-03.

Today `Enter` on a row of the left pane's list opens its tab in the right pane, but where the focus goes after that depends on the section:

| Row | `Enter` today | Focus |
|---|---|---|
| Agent | opens its tab | stays in the list |
| Worktree, or one of its changed files | opens its PR view, or the file's diff | moves to the tab |
| Code file | opens it read-only | stays, unless the list is zoomed |
| Doc, plan | opens its tab | stays, unless the list is zoomed |
| Code directory | folds or unfolds it | stays |

The worktree rows are the odd ones out, and an agent opened from a zoomed list opens out of sight. This makes every row behave the same way.

## The rule

- **`Enter` opens and stays**: the row's tab opens in the right pane (or comes to the front if it is open already), and the focus stays in the list, so you can go on through it.
- **`Shift+Enter` opens and goes**: the same tab opens, and the focus moves to it.
- **`o` is the same as `Shift+Enter`**, for terminals that send `Shift+Enter` as a plain `Enter` (below).
- **In a zoomed list, `Enter` opens and goes**, as `Shift+Enter` does: a tab opened behind the zoomed list could not be seen. The zoom moves to the tab, as it does today for Code, docs and plans.
- **A row that only folds folds on both**: `Enter` and `Shift+Enter` fold or unfold a Code directory, since it has no tab to open. `o` is not offered on it.
- **A row that both opens and folds keeps `←` / `→` for folding**: an agent with children, a worktree with changed files and a plan of plans open on `Enter`, as now.
- A click on a row stays as it is: it acts as `Enter` does.

| Row | `Enter` | `Shift+Enter`, `o` |
|---|---|---|
| Agent | opens its tab, focus stays | opens its tab, focus goes |
| Worktree | opens its PR view, focus stays (**changed**) | opens it, focus goes (today's `Enter`) |
| Worktree file | opens its diff, focus stays (**changed**) | opens it, focus goes (today's `Enter`) |
| Code file, doc, plan | opens its tab, focus stays | opens it, focus goes |
| Code directory | folds or unfolds | folds or unfolds (`o` does nothing) |
| any, in a zoomed list | opens, focus and zoom go | the same |

## `Shift+Enter` in the terminal

Most terminals send `Shift+Enter` as the same byte as `Enter` (`\r`). It can be told apart only through the kitty keyboard protocol (kitty, WezTerm, foot, Ghostty, recent iTerm2) or xterm's modifyOtherKeys, both of which tmux passes on only with `extended-keys` set. The probe run on 2026-11-03 showed `\r` for all three of `Enter`, `Shift+Enter` and `Ctrl+Enter` without either.

tide asks for nothing new: Bubble Tea v2 already asks the terminal for both at start (`cursed_renderer.go`, modifyOtherKeys 2 and kitty flag 1), and where one is spoken the key arrives as `shift+enter`. Keys typed into a pane's program are unaffected: termview hands the key to the pane's vt, which encodes it as that program asked: a program that asks for the kitty keyboard protocol gets `Shift+Enter` as `CSI 13;2u` (docs/KITTY_KEYS.md).

So `Shift+Enter` works where the terminal tells it apart, and `o` works everywhere. Elsewhere `Shift+Enter` is an `Enter`, and opens and stays.

## `o` in the menu

`o` is a selection-level item of the `C-q` menu (docs/TIDE_MENU.md), `o open and go`, offered on a row that opens a tab. Like every letter the menu shows, it works without `C-q` with the left pane in focus. The Project submenu's `o` (open a project) is in another menu, so the two do not meet; `/`'s filter line takes `o` as a letter while it is open.

## Not changed

- The right pane's tabs and the popups: their `Enter` stays as it is.
- The CLI: this is about where tide's focus goes, which the CLI has no counterpart of, so `docs/CLI_TIDE.md` has no row to change.

## Build

One step:

1. A `go bool` on the opening of each row kind: agents (`activate`), worktree rows (`openWorktreeRow`), Code files (`openCodeFile`), docs (`openDoc`) and plans (`openPlan`), each moving the focus when it is set or the list is zoomed, and nowhere else. `Enter` passes false, `shift+enter` and `o` pass true; on a Code directory `shift+enter` toggles as `Enter` does. The `o` menu item, and the docs: TIDE.md's keys, TIDE_WORKTREES.md (Enter no longer moves the focus), TIDE_CODE.md, DOCS.md and the plans doc, and `wdx tide --help` if it names Enter.

Tests, in `internal/tide`:

- For each row kind, `Enter` opens the tab and the focus stays in the list; `shift+enter` (a key press with the shift modifier) and `o` open it and move the focus.
- In a zoomed list, `Enter` moves the focus and the zoom to the tab.
- On a Code directory, `Enter` and `shift+enter` fold it, and `o` does nothing.
- The menu offers `o` on rows that open a tab and not on a directory, and `TestMenuParts` shows it.
