# tide: tabs in the right pane

Status: implemented. Designed with the human on 2026-11-01.

The right pane shows one agent at a time today, the active one. This gives it a tab per agent opened. A tab is only a view: closing one stops nothing. Other tab types (`docs/IDE.md`) come later and are not part of this.

## Decisions

### The strip

- A tab is `[<mark> <name>(-)]`. The mark is the one the Agents list shows for that agent (`●`, `?`, `○`, ...), which comes from the project's stream, so a tab that is not attached still shows it.
- The current tab is bold, the others muted, as terminal tabs are.
- The strip starts after the frame's corner, with the border line between tabs, and ends before the zoom button. The border line fills what is left up to the button.
- With no tabs, the title is the pane's name, as today.
- Terminal tabs keep their format and how they work.

### Too many tabs for the width

```
┬─[● swift-falcon(-)]─[● auth-fix(-)]─[? docs(-)]─▶────[⤢]─┐
┬─◀─[● auth-fix(-)]─[? docs(-)]─[● tests(-)]─▶─────────[⤢]─┐
┬─◀─[? docs(-)]─[● tests(-)]─[○ server(-)]─────────────[⤢]─┐
```

- Only whole tabs are shown, as many as fit: room is left before the zoom button only when the next hidden tab, on either side, would not fit in it, counting the arrows it would change. The others stay hidden until the user switches towards them.
- Names are not shortened to fit more tabs, so that every tab shown stays readable.
- `▶` follows the last tab shown when tabs are hidden to the right, and `◀` comes first when tabs are hidden to the left. They take room only when shown, so the tabs shift by two columns when `◀` comes or goes.
- They are `◀` and `▶` in the text's own colour, not muted, the human's choice of 2026-11-03: the dim `←` and `→` first used were lost in the border line. A terminal or font that draws them as wide emoji puts the title line one column out; `▸` is not used, as it is the Agents list's cursor.
- There is no animation. The strip moves only as far as it must to keep the current tab shown, so moving to a tab already shown does not move it.
- When closing a tab or widening the window leaves room, hidden tabs come back into it.
- A name too long for the strip on its own is cut, keeping the mark and `(-)`: `[● very-long-na…(-)]`.
- A click on `◀` or `▶` selects the nearest hidden tab on that side, so the current tab is never out of view.

### Keys

- `Alt+←` and `Alt+→` walk the chain `left pane - tab 1 - tab 2 ... tab n`. `Alt+←` on tab 1 moves the focus to the left pane. `Alt+→` on the last tab does nothing.
- `Alt+→` from the left pane returns to the current tab, not always tab 1.
- `Alt+↑` in the right pane moves the focus to the left pane, leaving the current tab as it is. It is on trial: the human keeps both this and `Alt+←` and sees whether it stays.
- `Alt+↓` goes to the terminal pane, as today.
- `C-q -` closes the current tab. The menu offers it while the right pane has the focus, never with the terminal pane's, so that it cannot seem to close a terminal tab; in the list it closes the row's tab (docs/TIDE_CLOSE_ROW.md).
- A zoom follows the focus, as it does for every move between panes.

### Mouse

- A click on a tab selects it; a click on its `(-)` closes it.
- A click on `(-)` never counts towards a double-click.
- A double-click zooms only when both clicks are on the same thing. Two quick clicks on different tabs just select them. This goes for terminal tabs too, which zoom on such clicks today.

### What opens and closes a tab

- `Enter` or a click on an agent in the list opens its tab at the right end, or goes to it when it is open. The focus stays in the list, as today.
- An agent created in tide (`C-q a`, `C-q c`) opens a tab. One spawned by another agent does not, or tabs would pile up.
- Closing a tab selects the tab to its right or, when it was the last, the one to its left. Closing the only tab moves the focus to the left pane.
- An agent that exits keeps its tab, showing its last screen read-only, as today.
- Removing an agent (`C-q x`, or from the CLI) closes its tab, even when the agent stays in the list as a tombstone because it has children. A tombstone can be opened again from the list. Since 2026-11-03 the daemon closes them and the stream brings it (docs/TIDE_STATE.md).
- Closing a tab asks nothing, since nothing is lost.

### Attaching

- Only the current tab's agent is attached. Every other tab is a name and a mark from the project's stream.
- Tabs switch at once. One switch with the keys attaches at once, so that it shows the agent without a wait; a switch soon after waits for the keys to rest, so that walking past tabs attaches none of them.

### What is remembered

- Each project keeps its tabs for the run, as it keeps its cursor and active agent.
- `C-q r` keeps them: `--resume` gets the open tabs, and `agent` stays the current one. A tab whose agent is gone is dropped. Only adding a field keeps resuming working from the run before.
- A fresh start opens only the first running agent, as today.
- Since 2026-11-03 the daemon keeps each project's tabs, so a fresh start and `C-q r` open the tabs kept, and `--resume` only says which is current (docs/TIDE_STATE.md).

## The active agent

The current tab and the active agent are one thing, so what reads the active agent keeps working. The Agents list keeps marking it. `agentList` gains the tabs, in order, and where the strip starts; `active` is `""` exactly when there are no tabs.

## Tests

- The strip is worked out by a pure function of the tabs, the current one, where the strip started and the room. Table tests cover it, with an invariant test over random widths and moves: the current tab is shown, nothing passes the zoom button, an arrow shows exactly when tabs are hidden on its side, no hidden tab would fit in the room left, and a move to a tab already shown does not move the strip.
- Action sequences cover the keys, the clicks, closing and removing, and one test covers `C-q r` keeping the tabs.
- A test checks that only the current tab is attached.

## Build order

1. Tabs in `agentList`, the title drawn from them, `Enter` opening and `C-q -` closing; only the current tab attached.
2. `Alt+←`, `Alt+→` and `Alt+↑`.
3. The sliding strip, with `◀` and `▶`.
4. The mouse, with the double-click fix.
5. `C-q r` keeping the tabs, then `docs/TIDE.md`, the help and `docs/ACA.md` if it applies.
