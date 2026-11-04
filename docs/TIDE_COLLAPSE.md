# Collapsing the left pane's sections

Status: approved by the human on 2026-11-04; built on 2026-11-04.

Today every section of the left pane (Agents, Worktrees, Docs, Plans, Code) always shows its rows, and the only way to hide one is to leave its letter out of `tide.section_order`, for every project and until the setting changes again. With a long Code tree or many docs, the other sections get pushed out of view. This lets you collapse a section for as long as you want it out of the way, with a click or a key.

## The rule

- **A collapsed section shows only its heading.** The heading keeps its count, so `Docs  12` still tells you what is in it.
- **Every heading has a mark**: `−` when the section is open, `+` when it is collapsed (` − Agents  3`, ` + Code`). These are the marks the agents' folds already use; `▸` is the cursor's pointer, so it is not used here.
- **A click on a heading toggles its section.** This includes the heading the pane keeps at its top while the list scrolls. The click opens no tab, and the focus moves to the left pane, as any click there does.
- **`z` toggles the section the cursor is in.** It is a line of the `C-q` menu's list part (`z collapse section`, or `z expand section` on a collapsed one), so `C-q z` does the same. Like every letter the menu shows, it works without `C-q` while the left pane has the focus. `z` is free on every level, and it is vim's fold key.
- **The cursor can stop on a collapsed heading**, and only on a collapsed one: an open section's heading stays a line the cursor passes over, as today. Without this, a collapsed section could be reached only with the mouse.
  - `z` on a row collapses its section and leaves the cursor on the heading.
  - `z` on a collapsed heading expands the section and puts the cursor on its first row, or leaves it on the heading when the section has no rows.
  - Collapsing the cursor's section with a click puts the cursor on the heading too.
- **The menu's selection part has nothing on a collapsed heading**, as it is not a row. `-` and `Enter` do nothing there.
- **Kept per project**, as the folds are: the view the daemon keeps (docs/TIDE_STATE.md) gains `collapsed`, the letters of the collapsed sections as `tide.section_order` names them. A project starts with every section open.
- **Collapsing hides only the rows.** The section's data stays up to date from events, its tabs stay open, and its count keeps changing.
- **Anything else that leaves the cursor in a collapsed section puts it on that section's heading**, and the section stays collapsed. This happens when the filter is cleared with the cursor on a match there, and when a project's view comes back. The cursor is never on a hidden row. (The design first said such a section expands. That would undo a collapse the view kept, and the collapse the filter's clearing restores, so it was changed while building.)
- **The filter (`/`) looks in collapsed sections too.** While it filters, a collapsed section with a match shows its matches under a `+` heading, and one with no match shows its heading only. When the filter is cleared, those sections are collapsed again. The filter is for finding something, and a match hidden by a collapse would look like no match.

| Where | Click | `z` |
|---|---|---|
| A heading, open | collapses it | not on it: the cursor passes over open headings |
| A heading, collapsed | expands it | expands it |
| A row | as today | collapses the row's section |

## Not changed

- `tide.section_order`: a section left out is still hidden from every project. Collapsing is per project and done from the list.
- Folds within a section (agents, worktrees, Code directories, plans) work as before, and a section keeps them while it is collapsed.
- The CLI: there is nothing to collapse there. `docs/CLI_TIDE.md`'s row "Panes, tabs, focus, zoom, folding, ..." already covers it as tide's own interface. Its row on keeping the view lists folds, and will list collapsed sections too.

## Build

- `agentList` gains `collapsed map[section]bool`. `projectView` gains `Collapsed []string`, saved and read back like `Folded`.
- `paneItems` puts no rows under a collapsed section, unless the filter is on and the section has matches. The first section's heading becomes an item when that section is collapsed, so that the cursor can stop on it.
- A `paneItem` with `heading` set and a collapsed section is a cursor stop. `moveBy`, `visibleItems` and the cursor's row learn it, through an `l.onHeading` flag for the section the cursor is in.
- `projectScreen.click` takes a click on a heading, including the pinned one at row 0, which today it ignores.
- `menu.go`'s list part gains the `z` item.
- Docs: TIDE.md's list part line and its mouse line, CLI_TIDE.md's row on keeping the view, and the help in `internal/cli/tide.go`.

## Tests

- Clicking a heading collapses and expands its section. This covers the pinned heading and a heading below it.
- `z` on a row, on a collapsed heading, and through `C-q z`, with the cursor where the rule says.
- Moving down and up stops on a collapsed heading, never on an open one. Expanding an empty section from its heading puts the cursor on the nearest row.
- With the filter on, a collapsed section with a match shows it; once the filter is cleared it is collapsed again.
- The collapsed sections are saved in the view and come back.
- The menu shows `z collapse section` or `z expand section`, and nothing in the selection part on a heading.
