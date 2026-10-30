# tide: the wdx Terminal-IDE

Status: Phase A, the layout skeleton, Phase B1, opening and switching projects, Phase B2, the project followed live with its terminals as tabs, Phase B3, the selected terminal live in the terminal pane, and Phase B4, the Agents list with the active agent live in the agent pane, are implemented in `internal/tide` (see Phases; docs/TIDE_B1.md, docs/TIDE_B2.md, docs/TIDE_B3.md and docs/TIDE_B4.md record how they were built). The rest is design.

tide is the interactive terminal UI of wdx, started with `wdx tide`. It ships in the same binary as the CLI and the daemon.

## Architecture

- tide is a client of the daemon API and holds no business logic, like `internal/cli`. It lives in `internal/tide`; `wdx tide` in the cli starts it.
- `wdx tide` is interactive by nature: with `--json` or without a terminal it fails with a `usage` error, the one exception to every command supporting `--json`. When you quit it gives the window back and prints `tide closed`.
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
- Holds a stack of screens. The root is the project view, `[Project: <name>]`, or `[Open Project]` while no project is open (see User journey).
- Other screens push on top: open or create a project, create an agent, agent detail, and more later.
- Screens can nest, e.g. agent detail → fork agent. `Esc` (or finishing a screen) pops one level.
- A screen may open zoomed by default when it needs room. Closing it restores the pane's previous zoom state.
- Screens are left-only for now. They never take over the agent or terminal pane.

### Right pane: agent

- 3/5 of the width.
- Shows one agent at a time: the one made active in the project view.
- The agent's name is on the pane's frame line.
- Live agent PTY.

### Bottom pane: terminal

- Full width, 1/4 of the height when docked.
- One tab per terminal of the selected project. Selecting another project switches to that project's set of tabs.
- Live PTY per tab.

### Status line

- One line at the very bottom, outside the ratios.
- Shows the current project's name, the focused pane, the zoom indicator, a key hint and, later, fleet status. The open projects are listed in the project strip instead.

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
- Panes that are hidden or covered by a zoom keep running.

### Sizing

- Cells are whole numbers. Project width = ⌊2/5 · width⌋, and the agent pane takes the rest. Terminal height = ⌊1/4 · height⌋ but at least 3 rows (its borders and a line), and the top row takes the rest. Sizes are of frames, borders included; neighbours share the border line between them.
- A PTY pane that changes size resizes its PTY, so the program inside redraws to fit.
- The status line takes the last row; the ratios share the rows above it.
- Neighbouring panes share one border line, as in the drawing above; the border around the focused pane is highlighted.
- Small screens just get cramped for now. No automatic zoom. No size is ever negative and a pane with no room is not drawn; when a shown pane would have no cell inside its frame, tide shows only "window too small": below 5 × 5 cells for the panes above the status line, 3 × 3 when one is zoomed.

## Screens

```go
type screen interface {
    title() string // shown on the left pane's frame line
    update(msg tea.Msg) tea.Cmd // a key, or the answer to a request it asked for
    draw(buf uv.Screen, area uv.Rectangle, st style.Style)
}
```

- A screen changes the stack through the messages of the commands it returns (`pushScreen`, `popScreen`), and asks for requests the same way (`reopenProject`, `createProject`): tide sends them, so screens hold no client, and hands a failure back to the screen that asked.
- A failure that needs an answer (a name taken, a conflict, a missing checkout) also pushes a screen asking for it over the one that asked, so `Esc` goes back to it.
- A screen may say what its keys do on the status line (`hint()`), while the left pane has the focus.

- A screen draws only inside the rectangle it gets, so it works the same at 2/5 width or zoomed.
- When a screen finishes, it returns a result message (project created, agent created, ...). In B1 a project opened replaces the stack with `[Project: <name>]`; later the screen below will react to results, e.g. the project view selecting a new agent.
- Opening a screen focuses the left pane. Popping a screen keeps focus there.
- Adding a screen means adding one type. The layout does not change.

## User journey

### Starting tide

- tide is meant to stay open, so it does not remember a project across runs: the user picks one.
- With no open project (the first run, or every one closed since), the left pane opens `[Open Project]` with the focus.
- With open projects, even one, tide starts with the switcher (`C-q p`) open over the window. `Esc` closes it, leaving `[Open Project]` in the left pane.
- While no project is shown, the terminal pane is hidden and the agent pane says "no project open". `C-q p` and `C-q o` work there as anywhere.
- With no daemon, the left pane says so with the hint `wdx daemon start` and a key to try again. tide never starts the daemon itself, as no command does.

### `[Open Project]`

A list chosen with ↑ / ↓ and `enter`:

1. **Open a local repository** → the directory browser, from the directory tide was started in.
2. **Clone a git repository** → a URL field.
3. **Create a new project** → the directory browser, from `~/.wdx/projects`, then a name field.
4. **The closed projects**, one entry each, under the three: `enter` reopens one at once. Shown only when there are some.

Sub-screens are titled as a path, `[Open Project › Local]`, `[Open Project › Clone]`, `[Open Project › Create]`, so it is clear where `Esc` goes: back to `[Open Project]`. When a project is already open, `Esc` on `[Open Project]` goes back to it.

### Directory browser

Built to be obvious without help:

- Lists only directories, git repositories marked `(git)`. The current path is on the first line, `~` for the home.
- ↑ / ↓ select. → or `enter` on a directory goes into it; ← or Backspace (with nothing typed) goes up.
- Typing filters the list; hidden directories show only when the filter starts with `.`.
- Local: `enter` on a repository opens it; a first entry "open this directory" appears when the current one is inside a repository (the repository root opens, as with the CLI).
- Create: a first entry "create the project here" asks for its name, then creates `<dir>/<name>`. It starts in `~/.wdx/projects`, or the closest directory above it that exists.
- Moving with ← / → did not feel natural when tried by hand; how the browser moves is to be looked at again.
- A directory that cannot be read says so where it is listed.
- tide reads the directories itself and sends absolute paths, as the CLI does. A remote client (the web IDE) will need the daemon to list them.

### Clone

- Only the URL is asked: the project is named after it, as with the CLI, and cloned into `~/.wdx/projects/<name>`.
- Only when that name is taken does a name field ask for another; the same goes for a local repository whose name is taken.
- While it clones the screen says "cloning…". `Esc` cannot cancel it: the daemon finishes a change even when its client leaves.

### Questions and errors

The CLI's prompts become small choice screens in the left pane, chosen with ↑ / ↓ and `enter`:

- Create, when the name is taken or the directory is in a repository: open that project, try another name, cancel.
- Clone or reopen, when none of a known project's checkouts is left: clone it again, remove it from wdx.

A failure (not a git repository, a clone that failed) shows in the screen, which stays so the input can be fixed.

### After opening

- The left pane's root becomes `[Project: <name>]`, showing the name for now; the project detail screen comes later.
- The project becomes the current one: the terminal pane shows its terminals.

## Data from the daemon

- tide changes things only through the API's create, update and delete requests.
- tide reads with get and list requests where they fit: the projects at start, the closed ones for `[Open Project]`, the open ones for the strip and `C-q p`.
- The current project's detail comes from an event stream the daemon pushes: every change under the project (a terminal started or exited, an agent going idle or waiting for input) reaches tide at once. Each event is the whole project, never a delta, so tide replaces what it holds and a reconnect needs no replay. Server-sent events on the daemon's socket, `GET /v1/projects/{project}/events`; designed in docs/TIDE_B2.md.
- So that switching never waits on the daemon, the strip and `C-q p` use the open projects tide fetched at start and after each change it made, refreshed in the background whenever they show.

## Focus

- Exactly one pane has focus and receives keys, except tide's own shortcuts.
- Hidden panes cannot have focus. Hiding the focused terminal pane moves focus to the top pane that had it last.
- Opening the terminal pane (`C-\`) while a top pane is zoomed leaves the zoom, docks the terminal and focuses it.
- While a pane is zoomed, moving focus moves the zoom with it: the neighbour becomes the zoomed pane.

## Key bindings

Constraint: in basic terminals only `Ctrl+letter`, a few `Ctrl+symbol`, plain keys and arrows get through reliably. macOS Terminal.app does not send `Alt` by default (Option types characters), and macOS takes `Ctrl+arrows` and `Ctrl+Space`. Every key tide takes is lost to the agents and shells inside its panes, so tide takes very few.

### Agreed

| Key | Action |
|---|---|
| `C-]` | zoom or un-zoom the focused pane |
| `C-\` | show or hide the terminal pane (focuses it when it opens) |
| `C-q` | open the shortcut menu |

These three keys are configurable, since `C-]` and `C-\` are awkward on some non-US keyboard layouts.

### Shortcut menu (`C-q`)

- A menu of actions with one-letter keys, e.g. `t` new terminal, `x` remove the entity in focus (the selected terminal, later an agent; always after a confirmation), `a` new agent, `o` open project (`[Open Project]`), `p` switch project, `d` agent detail, `r` resize mode, `?` help, `q` quit tide.
- Pressing the letter right away fires the action (`C-q n`), with no delay.
- Typing more than one letter turns it into a fuzzy command palette.
- Screens open from this menu.
- Built so far: `o` open project, `p` switch project, `t` new terminal and `a` new agent (while a project is shown), `x` remove the entity in focus (the terminal of the tab selected, the agent under the cursor or the active agent, after a confirmation popup), `q` quit. The menu lists only the lines that apply now, and the status line its keys.
- The final list of actions and letters is still to be decided.

### Switching panes

Switching focus is the most frequent action, so it is one chord, never a prefix and a letter.

- A modifier plus arrows, moving focus in the arrow's direction. `↓` from either top pane goes to the terminal pane; `↑` from the terminal returns to the top pane that had focus last.
- While the terminal pane has focus, left and right switch tabs, since it has no horizontal neighbor.
- `Alt+arrows`. It works out of the box in GNOME Terminal, Konsole and xterm, and costs little inside panes, where word jump is usually `ESC b` / `ESC f` or `Ctrl+←/→`. Both encodings terminals send for it are accepted (`CSI 1;3 A` and `ESC` before the arrow).
- `ESC b` / `ESC f` are never taken: they are word jump in every shell. A macOS terminal set to send them for Option+←/→ needs Option+arrows mapped back to the arrow codes, which tide's help explains.
- `Ctrl+arrows` are out: macOS takes them, and they are word jump on Linux. `Ctrl+Alt+arrows` never reach the terminal on GNOME, which switches workspaces with them.
- The chord is fixed: `Alt+arrows` only, not configurable.
- Key probe on Linux (2026-10-29): `Alt+arrows` arrive as `CSI 1;3 A`–`D`; `Ctrl+arrows` as `CSI 1;5 A`–`D`; `Ctrl+Alt+arrows` never reach the terminal (the desktop takes them); `C-]`, `C-\` and `C-q` arrive as 0x1d, 0x1c and 0x11. So `Alt+arrows` works on Linux.
- Key probe on macOS (2026-10-29): Option+arrows arrive as `CSI 1;3 A`–`D`, Ctrl+Option+arrows as `CSI 1;7 A`–`D`; `Ctrl+arrows` never reach the terminal (macOS takes them); `C-]`, `C-\` and `C-q` arrive as on Linux. So `Alt+arrows` works on both, and is the only chord that does.

### Switching projects

Switching projects is frequent too, so it is one chord.

- `Alt+Shift+←` / `Alt+Shift+→`: the previous or next open project, going round. Projects are in the order of their names, as the daemon lists them, so each keeps its place: only a project opened with an earlier name moves it.
- Each tap switches at once, so holding Alt+Shift and tapping an arrow feels like Alt+Tab, though terminals send nothing when keys are released (only the kitty keyboard protocol does). The name and titles change at once; the project's terminals will attach once the selection has rested for about 200 ms, so tapping past projects stays cheap (with terminals in the panes, after B1).
- The first tap shows the project strip, one row just above the status line, over the bottom of the panes:

  ```
  │  api   web  ▸ docs ◂  infra   dotfiles               │
   docs · agent                      Alt+arrows move  C-q menu
  ```

  It lists the open projects, the current one marked, and scrolls sideways to keep the current one in view when they overflow the width. It hides about a second after the last tap, or at once on any other key.
- `C-q p`: the switcher, a box over the middle of the window, to find a project by name, with ↑ / ↓ or by typing:

  ```
  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
  ┃ Switch project                     ┃
  ┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
  ┃ > do_                              ┃
  ┃                                    ┃
  ┃   docs                  open       ┃
  ┃ ▸ dotfiles              closed     ┃
  ┃   docker-images         closed     ┃
  ┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
  ┃ ↑↓ select  enter switch  Esc close ┃
  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
  ```

  Every popup (the switcher, the `C-q` menu) has this shape: a heavy frame, a bold title, the body, and the keys at the bottom, set apart by lines.

  It lists every project, open ones first, then closed ones, each by name. Typing filters by fuzzy match on the name; ↑ / ↓ select; `enter` switches, reopening a closed project first; `Esc` closes it. The project shown before the current one in this run is selected at first, so `C-q p enter` flips back to it; at start, the first one.
- The `C-q` menu lists no projects: it opens at once, never waiting on the daemon. `C-q p` is the way to search and switch.
- Key probe (2026-10-29): `Alt+Shift+arrows` arrive as `CSI 1;4 A`–`D` on Linux and macOS. `Alt+1`…`9` never reach the terminal on GNOME, which switches its own tabs with them, and type characters on macOS (`¡`, `™`), as Option+letters do. `Ctrl+Tab` arrives as a plain Tab on Linux and not at all on macOS; `Alt+PageUp` / `PageDown` do not reach the terminal on macOS.
- `Alt+Shift+↑` / `↓` are free for later.

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
- Dragging selects text inside the pane where the drag started, never past its edges, highlighted in reverse; releasing copies it, as terminals do on Linux. A click or a key clears it. The copy goes both through OSC 52, which works over ssh, and through the system's tool (`wl-copy`, `xclip` or `xsel` on Linux, `pbcopy` on macOS), since GNOME Terminal and Terminal.app ignore OSC 52. In a pane whose program takes the mouse, the program will get the drags instead.
- The terminal's own selection, `Shift`-drag (Linux) or `Option`-drag (macOS), still works but crosses the panes, as the terminal knows nothing of them.

## Phases

- **Phase A, the layout skeleton.** Placeholder panes that show their name, size and the keys they receive; the layout state and pure sizing; focus, zoom and the terminal toggle; Alt+arrows; placeholder terminal tabs switched with ←/→; the `C-q` menu with only `q` (quit) and `Esc`; mouse: click to focus, `[⤢]` or double-click on a title to zoom, click a tab. Tested by table tests, action sequences and an invariant test over random actions and sizes (the panes tile the screen exactly, focus is always visible, leaving a zoom restores the layout). No daemon needed.
- **Phase B1, projects (implemented):** the whole journey above, with list requests for reading (see Data from the daemon) and no change to the daemon. In tide: starting on `[Open Project]` or the switcher; the screen stack; `[Open Project]` complete, with local, clone, create and reopen, the directory browser and the choice screens; `[Project: <name>]`; the daemon-not-running screen; switching projects with the strip and `Alt+Shift+←/→`, and `C-q p`. The agent and terminal panes stay placeholders. The `C-q` menu has `o` (open project), `p` (switch project) and `q` (quit).
- A tide run logs every request it sends under its one debug id, so a whole session traces as one.
- **Phase B2, the live project (implemented, docs/TIDE_B2.md):** the daemon streams the project shown, whole on every change; tide's terminal tabs come from it, with a state symbol each; `C-q t` creates a terminal and `C-q x` removes the entity in focus after a confirmation. Tabs stay placeholders.
- **Phase B3, terminals in the terminal pane (implemented, docs/TIDE_B3.md):** the selected tab's terminal attached through termview, taking keys, mouse and scrolling; only that one attached, switching tabs or projects attaching the one selected (after a project switch rests); kept attached while the pane is hidden; a terminal not running shows its last screen from history, read-only.
- **Phase B4, the Agents list (implemented, docs/TIDE_B4.md):** the project pane lists the project's agents; `Enter` or a click makes one active, live in the agent pane through termview; `C-q a` creates an agent and `C-q x` removes one. The list is linear; child agents come next.
- **Later:** configurable keys, dragging dividers, the project detail screen, and how the directory browser moves.

## Open questions

- Resizing: whether dividers can move away from 2:3 and 1:4 (drag, or a resize mode in the menu), and whether a changed size persists.
- The shortcut menu's actions and letters, and how screens are opened.
- Rendering performance with several live PTY panes, and color, wide-character and cursor fidelity. To measure in a spike.
- Possible later: richer keys (kitty keyboard protocol) in modern terminals such as Ghostty, kitty, iTerm2 and WezTerm.
