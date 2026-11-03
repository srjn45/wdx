# tide's `C-q` menu, by level

Status: approved by the human on 2026-11-03, not built.

Today the `C-q` menu is one flat list of letters, each offered when it applies (`internal/tide/terminals.go`). It has grown to sixteen lines, mixing what acts on the thing in focus with what acts on tide or the project, and `n` means a new session in the menu but a new file in Code. This restructures the menu only: the keys of the right pane's tabs (diff, PR, read-only file, doc, settings, info) and of the popups stay as they are.

## Levels

The menu is in parts, one per level, from the narrowest to the widest, each with a title:

- **selection**: the thing the action is about. In the left pane, the row under the cursor; in the right pane, the tab's agent; in the bottom pane, the tab's terminal. The title names it: `ample-deer (agent)`.
- **section**: the left pane's section the cursor is in (`worktrees`).
- **pane**: the left pane as a whole (`list`).
- **global**: tide and the project shown.

The right and bottom panes have selection and global only: one thing is in focus there, and what acts on it is clear.

Rules:

- **A part with nothing in it is not shown**, and a line shows only when it applies, as today.
- **An action lives at one level**: the narrowest at which it always makes sense. New agent works from any pane, so it is global and not in the Agents section.
- **No two parts shown together share a letter.** In the left pane the letters work without `C-q`, so the menu is its key map, and a letter can mean one thing only.

## Global

```
a new agent · t new terminal · p project… · s settings · q quit
```

- `a` and `t` while a project is shown; `s` while a project is shown and the focus is not already on its settings tab, as today.
- `p` opens the **Project** submenu, a popup of the same shape, in place of the menu: `o` open, `s` switch, `c` close. `Esc` closes it. It is the one submenu: global has five lines, and a submenu costs a key and a popup on every use, so one is added only when a group grows.
- With no project shown, global alone: `p`, whose submenu then has `o` and `s`, and `q`.

## From each place

Lines in parentheses show only when they apply.

**Left pane, Agents, cursor on an agent**
```
ample-deer (agent)   x remove · c new child · i info · R new session · (g start again)
list                 / filter
global               a new agent · t new terminal · p project… · s settings · q quit
```

**Left pane, Worktrees, cursor on a worktree or a file**
```
feat-x (worktree)    v verify
worktrees            f refresh
list                 / filter
global               …
```

**Left pane, Code, cursor on a file or a directory**
```
main.go (file)       e edit · n new file · N new dir · r rename · x delete
list                 / filter
global               …
```

`n` and `N` start where they do today: in the directory under the cursor, or the file's.

**Left pane, Docs**
```
list                 / filter
global               …
```

**Right pane, an agent's tab**
```
ample-deer (agent)   x remove · c new child · i info · R new session · (g start again)
                     - close tab · \ ] C-q M-arrow send to the program
global               …
```

**Right pane, a diff, file, doc, settings or info tab**
```
<tab title>          - close tab
global               …
```

**Right pane, an editor's tab**
```
main.go (editor)     - close tab, asking to save or discard · \ ] C-q M-arrow send to the program
global               …
```

**Bottom pane, a terminal's tab**
```
ample-fox (terminal) x remove · (g start again) · \ ] C-q M-arrow send to the program
global               …
```

## Without `C-q` in the left pane

Kept, and simpler than today: every letter the menu shows works without `C-q` in the left pane, at every level, `p` opening the Project submenu. The filter's line, while open, takes the letters until `Enter` or `Esc`, as today. Code's letters no longer come first, as none is the same as a global one. In the right and bottom panes the letters go to the program or to the tab's own keys, so those keep `C-q`.

## What changes for the person

| Before | After | Why |
|---|---|---|
| `C-q n` new session | `C-q R` | `n` is a new file or directory, as in Code |
| `C-q o` open project | `C-q p o` | Project submenu |
| `C-q p` switch project | `C-q p s`; `C-q p s Enter` flips back | Project submenu. `Alt+Shift+←/→` still switch at once, and tide still starts on the switcher |
| `C-q w` close project | `C-q p c` | Project submenu |
| `C-q f` refresh worktrees, anywhere | `f` in the Worktrees section | The narrowest level where it makes sense |
| `C-q r` reload tide | gone | Quitting and starting again does the same, now that the daemon keeps tide's view (docs/TIDE_STATE.md); agents and terminals keep running either way |
| Code's `d` delete | `x` | `x` removes the selection everywhere |
| Renaming an agent in its info tab | also `r` on it | Quicker (step 2) |

Removing reload leaves the hidden `--resume` flag accepted, because an older tide still open reloads into the binary installed and passes it: rejecting it would fail that reload. It does what it does today when the daemon keeps no view, and nothing more.

## Steps

1. **`feat!:` the menu by level.** The parts and their titles, the Project submenu, the letters above, the left pane's letters without `C-q` following them; `C-q r` removed with its code and tests but `--resume`. docs/TIDE.md's shortcut menu section, docs/TIDE_CODE.md's keys and docs/CLI_TIDE.md's rows updated. Tests: the menu's parts from each place in the list above; no two parts shown together share a letter, checked over every place; each moved key does its action, and its old letter does not.
2. **`feat:` `r` renames the agent selected**, in a popup with its name, as the info tab's field does, through the same request. On the agent's selection in the left pane and on its tab.
3. **`feat:` `x` in the Project submenu removes the project shown**, as `wdx project remove` does: from wdx only, files on disk untouched, its agents, terminals and their history gone. A confirmation says that and names the agents and terminals running; tide then goes where it goes after close. Its own design before it is built.

The `!` in step 1 is for people, not scripts: no command, flag or JSON changes, but the keys a person uses do.
