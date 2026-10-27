# tide: the wdx Terminal-IDE

Status: design under discussion, nothing implemented.

tide is the interactive terminal UI of wdx, started with `wdx tide`. It ships in the same binary as the CLI and the daemon.

## Architecture

- tide is a client of the daemon API and holds no business logic, like `internal/cli`.
- Agents and terminals run in PTYs owned by the daemon's holders. tide attaches to them; quitting tide stops nothing.
- Built with Bubble Tea. Each PTY pane renders a terminal emulator screen (`charmbracelet/x/vt` behind our own `Screen` interface) into its rectangle.
- Must work in the default terminals of Linux (GNOME Terminal, Konsole, xterm) and macOS (Terminal.app).

## Layout

Three fixed panes. No general split tree.

```
┌ project ──────────┬ claude: auth-fix ──────────┐
│                   │                            │
│       2/5         │           3/5              │
│                   │                            │
├ [shell] [tests] [server] ──────────────────────┤
│ $ _                                    1/4     │
└────────────────────────────────────────────────┘
```

### Left pane: multipurpose

- 2/5 of the width.
- Holds a stack of screens. The project view is the root and is always there.
- Other screens push on top: open or create a project, create an agent, agent detail, and more later.
- Screens can nest, e.g. agent detail → fork agent. `Esc` (or finishing a screen) pops one level.
- A screen may open zoomed by default when it needs room. Closing it restores the pane's previous zoom state.
- Screens are left-only for now. They never take over the agent or terminal pane.

### Right pane: agent

- 3/5 of the width.
- Shows one agent at a time: the one selected in the project view.
- The agent's name is on the pane's frame line.
- Live agent PTY.

### Bottom pane: terminal

- Full width, 1/4 of the height when docked.
- One tab per terminal of the selected project. Selecting another project switches to that project's set of tabs.
- Live PTY per tab.

### Status line

- One line at the very bottom, outside the ratios.
- Shows the zoom indicator, a key hint and fleet status.

## Pane modes

| Pane | Modes |
|---|---|
| Project (left) | normal (2/5 width), zoomed (full screen) |
| Agent (right) | normal (3/5 width), zoomed (full screen) |
| Terminal (bottom) | hidden, docked (1/4 height), zoomed (full screen) |

The top row takes the full height when the terminal pane is hidden, and 3/4 when it is docked.

### State

The whole layout follows from two values and the terminal size:

```go
terminalOpen bool // hidden or docked
zoomed       pane // none, project, agent or terminal
```

- Leaving a zoom returns to the previous layout. Un-zooming the terminal brings it back to docked.
- Zooming the terminal while it is hidden opens it to full screen; un-zooming then docks it.
- Panes that are hidden or covered by a zoom keep running.

### Sizing

- Cells are whole numbers. Project width = ⌊2/5 · width⌋, and the agent pane takes the rest. Terminal height = ⌊1/4 · height⌋, and the top row takes the rest.
- A PTY pane that changes size resizes its PTY, so the program inside redraws to fit.
- Small screens just get cramped for now. No automatic zoom.

## Screens

```go
type screen interface {
    Update(msg tea.Msg) (screen, tea.Cmd)
    View(width, height int) string
    Title() string // shown on the left pane's frame line
}
```

- A screen draws only inside the rectangle it gets, so it works the same at 2/5 width or zoomed.
- When a screen finishes, it returns a result message (project created, agent created, ...). The screen below reacts to it, e.g. the project view selects the new agent and the agent pane switches to it.
- Opening a screen focuses the left pane. Popping a screen keeps focus there.
- Adding a screen means adding one type. The layout does not change.

## Focus

- Exactly one pane has focus and receives keys, except tide's own shortcuts.
- Hidden panes cannot have focus. Hiding the focused terminal pane moves focus to the agent pane.

## Key bindings

Constraint: in basic terminals only `Ctrl+letter`, a few `Ctrl+symbol`, plain keys and arrows get through reliably. macOS Terminal.app does not send `Alt` by default (Option types characters), and macOS takes `Ctrl+arrows` and `Ctrl+Space`. Every key tide takes is lost to the agents and shells inside its panes, so tide takes very few.

### Agreed

| Key | Action |
|---|---|
| `C-]` | zoom or un-zoom the focused pane |
| `C-\` | show or hide the terminal pane (focuses it when it opens) |
| `C-q` | open the shortcut menu |

The keys are configurable, since `C-]` and `C-\` are awkward on some non-US keyboard layouts.

### Shortcut menu (`C-q`)

- A menu of actions with one-letter keys, e.g. `n` new terminal tab, `x` close tab, `a` new agent, `p` open or create project, `d` agent detail, `r` resize mode, `?` help, `q` quit tide.
- Pressing the letter right away fires the action (`C-q n`), with no delay.
- Typing more than one letter turns it into a fuzzy command palette.
- Screens open from this menu.
- The final list of actions and letters is still to be decided.

### Open: switching panes

- Wanted: a modifier plus arrows, moving focus in the arrow's direction. `↓` from either top pane goes to the terminal pane; `↑` from the terminal returns to the top pane that had focus last.
- While the terminal pane has focus, left and right switch tabs, since it has no horizontal neighbor.
- Candidates:
  - `Alt+arrows`: works on Linux. On macOS Terminal.app, Option+←/→ arrive as word-jump codes (`ESC b` / `ESC f`) and ↑/↓ are unverified. Taking it breaks word jump inside panes.
  - `Ctrl+Alt+arrows` (preferred): no word-jump conflict, but GNOME switches workspaces with it by default and macOS behavior is unverified.
- To verify with a key probe in GNOME Terminal and macOS Terminal.app before deciding.

### Panes without a PTY

The project pane and its screens use plain keys for their own navigation (`j` / `k`, `enter`, `n`, ...). tide's shortcuts work there too, so navigation is the same in every pane.

## Mouse

Supported, though most users will mostly use the keyboard.

- Click a pane to focus it.
- Double-click a frame title, or click a `[⤢]` button on it, to zoom.
- Click a terminal tab to switch to it.
- Drag the dividers to resize.
- The wheel scrolls a pane's scrollback.
- Programs that request the mouse (vim, htop) get mouse events inside their pane.
- While tide captures the mouse, native text selection needs `Shift`-drag (Linux) or `Option`-drag (macOS).

## Open questions

- Pane-switching keys (see above).
- Resizing: whether dividers can move away from 2:3 and 1:4 (drag, or a resize mode in the menu), and whether a changed size persists.
- The shortcut menu's actions and letters, and how screens are opened.
- Rendering performance with several live PTY panes, and color, wide-character and cursor fidelity. To measure in a spike.
- Possible later: richer keys (kitty keyboard protocol) in modern terminals such as Ghostty, kitty, iTerm2 and WezTerm.
