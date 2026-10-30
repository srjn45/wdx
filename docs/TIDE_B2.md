# tide Phase B2: the live project

Status: planned; not started.

What B2 builds: the daemon streams the project tide shows, whole, every time it changes, and tide draws the terminal pane's tabs from it. `C-q t` creates a terminal in the current project and `C-q x` removes the entity in focus (for now a terminal), after a confirmation. Terminals created or removed from the CLI show in tide at once, and switching projects swaps the tabs. Rendering a terminal in its tab is out of scope: each tab stays a placeholder until the next phase (termview in the panes). This file is the plan; docs/TIDE.md stays the design.

## Decisions

Agreed with the human:

- **The stream carries the whole project, every time.** The first event is the project as it is; after every change under it the daemon sends it whole again, never a delta. A client does not interpret events: it replaces what it holds. So nothing is missed or applied out of order, and a client that reconnects is up to date with its first event; no event ids and no replay. A project's detail is small, so sending it whole costs nothing that matters.
- **Transport: server-sent events over the daemon's socket.** `GET /v1/projects/{project}/events` answers `text/event-stream` on the HTTP API tide already uses: plain HTTP, one way, on the same socket and server. Changes still go through the usual requests, so nothing needs a two-way channel; WebSocket stays for the web IDE's terminal attach, which is two-way. A browser reads the same stream with `EventSource`.
- **Inside the daemon, a notifier on Go channels.** Every change to a project or its terminals goes through the daemon's stores, whoever asked: the CLI, tide, or the supervisor seeing a shell exit. After each change is committed the store notifies "project <id> changed"; the notice carries no data. Each open stream then reads the project again, as `getProject` does, and sends it. Several notices arriving while a stream is sending collapse into one: a stream holds at most one pending notice, so a slow reader gets the latest state, never a queue.
- **`C-q x` removes the entity in focus, after a confirmation.** It is the one key for removing: the terminal of the selected tab when the terminal pane has the focus; later the agent in the agent pane, or the agent selected in the project pane's list. The menu names what it would remove (`x  remove ample-deer`) and leaves the line out when nothing in focus can be removed. Removing is destructive, so a popup asks first, and says when a running terminal will be stopped.
- **Every terminal has a tab**, whatever its state, marked with a symbol in its state's color: the symbol alone tells the state with color off.
- **`C-q t` creates a terminal in the current project's main checkout.** Choosing among worktrees comes later.

## The stream

```
GET /v1/projects/{project}/events
Accept: text/event-stream

event: project
data: {"project": {...}}          the body getProject returns: worktrees with present, terminals in every state

event: project
data: {"project": {...}}          again, whole, after each change

event: removed
data: {"project_id": "proj-..."}   the project was removed; the stream ends
```

- `{project}` is a name or `id:` and an id, as elsewhere. The stream follows the project by id, so a rename does not lose it.
- A project closed stays streamed: an event with `state: closed`, then whatever follows (a reopen).
- An unknown project is `not_found` (404) before the stream starts, as Problem Details.
- The daemon ends every stream when it shuts down, before waiting for the requests in flight, and a stream ends when its client leaves. (Changes run to their end when the client leaves; a stream is the opposite.)
- Logging: opening the stream is `request received`, its end `request returned`, as for every request, under the client's debug id; each event sent is logged at debug, without its body.
- What is only on disk is not watched: a checkout deleted by hand shows as `present: false` in the next event sent for another reason.
- The spec describes the response as `text/event-stream`; the client reads it with a small SSE reader of its own in `internal/client` (`ProjectEvents(ctx, ref)`), which returns the projects one by one until the context ends or the stream does.

## In tide

- **Following the project shown.** Showing a project opens its stream; switching closes the old one and opens the new, and the tabs swap when its first event arrives. Until then the terminal pane says "loading…". When the stream breaks (the daemon restarted), tide opens it again after a short wait, growing to a few seconds, and says so in the pane meanwhile.
- **Tabs.** One per terminal, in the order of their names, as `[● ample-deer]`. Symbols: `●` running (green), `◌` starting (yellow), `○` exited or stopped (muted), `✕` failed or lost (red). The selected tab is remembered per project for the run, by terminal id, so switching back selects it again; when it is gone, the one beside it is selected. With no terminal the pane says "no terminals: C-q t creates one".
- **The tab's content** stays a placeholder: the terminal's name, state, directory and exit code or error, with the size and the input as now.
- **`C-q t`** sends `createTerminal` with the project and its main checkout; tide selects the new tab once the answer comes back (the tab itself arrives with the stream). It shows in the menu only while a project is shown. A failure shows in the terminal pane.
- **`C-q x`** with the terminal pane focused opens the popup:

  ```
  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
  ┃ Remove terminal                       ┃
  ┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
  ┃ Remove ample-deer?                    ┃
  ┃ It is running: its shell is stopped.  ┃
  ┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
  ┃ y remove  n or Esc keep               ┃
  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
  ```

  `y` sends `deleteTerminal` with `stop_running`, since the popup asked; the tab goes with the stream's next event. A failure shows in the popup's place, in the terminal pane.

## Steps

Each step ends with `make check` and `make test` passing, and is committed when the human asks.

1. **`docs:`** this file and the docs/TIDE.md changes.
2. **`feat:` the daemon streams a project.** The spec (`streamProject`), the notifier and the stores' notices, the SSE handler, ending streams on shutdown and when the client leaves, the client's `ProjectEvents`. Tests against an in-process daemon: the first event; a terminal created, stopped, exiting by itself and removed each send the project again; closing and reopening; removal sends `removed` and ends; a slow reader gets the latest; shutdown ends streams; every stream request logged from received to returned.
3. **`feat:` tide follows the project shown.** The stream as a Bubble Tea subscription, reopened after a break; tabs from its terminals with their states; switching swaps them; the selection kept per project.
4. **`feat:` `C-q t` creates a terminal.**
5. **`feat:` `C-q x` removes the terminal in focus**, through the confirmation popup.
6. **`docs:`** docs/TIDE.md marks B2 implemented; this file records what was built.

Suggested, not in scope unless asked: a CLI command printing the stream (`wdx project watch --name api`, one JSON object per line with `--json`) for scripts and AIs.

## Progress

Not started.
