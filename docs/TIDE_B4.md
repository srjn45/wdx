# tide Phase B4: the Agents list and the active agent

Status: implemented. Built in the worktree `.worktrees/tide-agents`, branch `tide-agents`, rebased onto `main` to land.

What B4 builds: the project pane lists the project's agents under an `Agents` heading with a highlighted background, as a linear list, and the agent pane shows the active agent live. The list comes first of the sections the project pane will have: Docs, Plans, Agents and Code.

B4 has no nesting, folding or tree code: the human decided on the linear list first (2026-10-30). Child agents come right after, with their own design; "For child agents" below holds what is already agreed for them.

## Decisions

Agreed with the human (2026-10-30):

- **The cursor and the active agent are separate.** The arrow keys move the cursor; the active agent is the one shown in the agent pane.
- **The active agent shows live in the agent pane.** This step replaces that pane's placeholder.
- **`Enter` makes the agent under the cursor active.** Moving the cursor alone attaches nothing.
- **`Enter` leaves the focus in the project pane.** `Alt+→` moves to the agent pane, as anywhere.
- **The first running agent is active when a project opens.** With none running, no agent is active until `Enter`.

- **`↑` and `↓` move the cursor** and skip the heading; `←` and `→` do nothing in the list.

Following from the design (docs/TIDE.md, docs/TIDE_B2.md, docs/TIDE_B3.md):

- **An active agent that is not running shows its last screen, read-only**, as a terminal does.
- **While the agent pane has the focus**, keys, pastes and the mouse go to the agent, except tide's own keys, as in the terminal pane.
- **`C-q x` removes the agent under the cursor** while the project pane has the focus, after the confirmation popup.
- **A click on a row** moves the cursor there and makes that agent active.
- **Removing the active agent** empties the agent pane; no other agent becomes active by itself.
- **Each project remembers its cursor and active agent** while tide runs, as it does its selected tab.

## The look

Design A, chosen by the human in a preview, with its default colors:

```
┌ [Project: wdx] ──────────────────────┬ swift-falcon ──
│ Agents                             4 │
│  ● ample-deer                        │
│  ✕ bold-heron                 failed │
│▸ ○ calm-otter               exited 0 │  ← the cursor: green ▸, bold name; hidden out of the pane
│  ● swift-falcon                    ◂ │  ← active: the whole row tinted, and ◂
```

- The heading is a band of black on white, bold, with the number of agents at its right end; `style.Section`, in reverse video with color off.
- The active row is tinted bright black; `style.ActiveRow`, nothing with color off, where `◂` tells it.
- A row has the state mark of the terminal tabs, the name, and a muted detail only where it says more than the mark: `exited 0`, `failed`. Running has none. A narrow pane cuts the name with `…`, and drops the detail rather than leave a name under 10 cells.
- The list scrolls to keep the cursor in view, under the heading.

## Creating an agent

`C-q a`, asked for by the human, creates an agent in the same checkout as `C-q t`, as `wdx agent create` would, with a generated name. As `C-q t` does for a terminal, it focuses the agent pane, since an agent is created to talk to; once the answer comes the agent is active with the cursor on it. This is the one way an agent becomes active that moves the focus. A failure shows on the status line until the next key.

## How it is built

- `attachment` has the pane it shows in. `m.term` and `m.agentTerm` are the two, attached and detached the same way (`attachPane`, `detach`); input, mouse, wheel, size, cursor and status go by the pane. An agent reaches the panes as `terminalOf(agent)`, and reads its history through the agent API, since no terminal path accepts its terminal.
- `agentList` holds a project's cursor, active agent and scroll, by project id, for the run.
- `projectScreen` draws the list and takes its keys and clicks (`agents.go`).
- The placeholder of the agent pane is gone, and with it the `input` every pane had to record what it got.
- No change to the daemon or the API.

## Tests

`agents_test.go`: the list, cursor, active agent, scrolling, clicks and narrow rows on a model the stream's events are given; the rest against a daemon with real holders: an agent attached at its pane's size and taking the keys, an agent that exits, `C-q a`, `C-q x`, and switching projects. The test shell ignores the command it is given, so an agent there is that shell. Checked by hand with the real Claude Code: live in the pane, `Enter` on another agent, the menu and the colors in a terminal.

## For child agents

Not built in B4; agreed for the step after it, and built with it (docs/CHILD_AGENTS.md).

### Moving through the tree

Tried and chosen in a preview:

| Cursor on | `→` | `←` |
|---|---|---|
| a closed parent | opens it; the cursor stays | moves the cursor to its parent |
| an open parent | nothing | closes it; the cursor stays |
| a row with no children | nothing | moves the cursor to its parent |

- On a top-level row, moving to the parent does nothing.
- `←` only ever closes the row the cursor is on; it never closes a parent from below.

### Folding

- **An active agent inside a closed parent:** its nearest visible parent carries the active mark until it is opened again.
- **Reopening a parent restores what was open inside it.**
- **A new child starts visible**, unless its parent is closed; then the parent's `n below` count goes up.
