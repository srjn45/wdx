# Closing a tab from the left pane

Status: approved by the human on 2026-11-03.

Today `-` (`C-q -`) closes a tab only with the right pane in focus: to close the tab of a row in the left pane's list, you go to the tab first. This lets the list close what it opens.

## The rule

- **`-` closes the row's tab**: with the cursor on a row of the left pane, `-` closes the tab `Enter` on that row opens (docs/TIDE_ENTER.md), when that tab is open.
- It is the selection level's `- close tab` of the `C-q` menu (docs/TIDE_MENU.md), so `C-q -` does the same, and like every letter the menu shows it works without `C-q` with the left pane in focus.
- **Only when the tab is open**: on a row whose tab is not open, the menu has no `close tab` line and `-` does nothing.
- **The focus stays in the list**, zoomed or not. When the tab closed was the current one, the one to its right takes over, or to its left when it was the last, as when a tab is closed in the right pane.
- **An editor's tab asks first**: a Code file's tab that an editor runs in gets the save or discard box, as `C-q -` in the right pane does, and the box shows the tab and moves the focus to it, since what is unsaved is what is being decided on. `-` never stops an editor out of sight.

| Row | `-` closes |
|---|---|
| Agent | its tab; its info tab, if open, stays |
| Worktree | its PR view |
| Worktree file | its diff |
| Code file | its tab in the worktree chosen |
| Code directory, the worktree selector | nothing: no `close tab` line |
| Doc | its tab |
| Plan | its tab |

## Not changed

- The right pane's `C-q -`.
- The CLI: tide's tabs have no counterpart there, so `docs/CLI_TIDE.md` has no row to change.

## Build

One step:

1. `rowTab`, the tab of the row under the cursor, by the same sections as `rowOpener`; the selection part offers `close tab` with the left pane in focus when that tab is open, calling `askCloseTab` as the right pane does. The docs: TIDE.md's menu section and `wdx tide --help`.

Tests, in `internal/tide`:

- For each row kind, `Enter` then `-`, and `Enter` then `C-q -`, close the tab, and the focus stays in the list.
- `-` on a row whose tab is not open closes nothing, and the menu has no `close tab` line there.
- On a Code file with an editor running, `-` asks with the box.
