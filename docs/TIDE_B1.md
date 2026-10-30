# tide Phase B1: projects

Status: implemented, steps 1 to 8 (see Progress). After trying step 2 the human asked for steps that each end with something to use, so they were built in the order 1, 2, 3 with 4, 7, 5, 6, 8. Built in the worktree `~/dev/warden-rebuild-tide`, branch `tide-skeleton`, rebased on `origin/main`.

What B1 builds is the user journey in docs/TIDE.md: starting on `[Open Project]` or the switcher, `[Open Project]` complete (local, clone, create, reopen) with the directory browser and the choice screens, `[Project: <name>]`, the daemon-not-running screen, and switching projects with the strip, `Alt+Shift+←/→` and `C-q p`. The agent and terminal panes stay placeholders. The daemon does not change. This file is the plan to build it; docs/TIDE.md stays the design.

## Decisions

Agreed with the human, beyond what docs/TIDE.md says:

- B1 is the whole project journey at once, switching included; nothing of it is left for a later slice.
- No project is remembered across runs: tide is meant to stay open. With no open project tide starts on `[Open Project]`; with any, even one, on the switcher, whose `Esc` leaves `[Open Project]`. `C-q p` and `C-q o` work everywhere, the empty window included.
- Projects are ordered by name everywhere (the strip, the switcher's open ones then its closed ones), as `listProjects` returns them, so the daemon needs no `opened_at` or `last_used_at`.
- tide changes things only through create, update and delete requests, and reads with get and list requests in B1. An event stream for a project's detail (terminals, agents, their states) comes later, with the project detail screen and the agents; following changes made elsewhere (a project closed from the CLI while tide shows it) waits for it.
- The `C-q` menu lists no projects and never waits on the daemon. No `C-q 1`…`9`: `C-q p` searches and switches. The strip shows no numbers.
- The strip and `C-q p` use the open projects tide fetched at start and after each change it made, refreshed in the background whenever they show, so switching never waits.
- The switcher selects first the project shown before the current one in this run, so `C-q p enter` flips back; tide keeps it, not the daemon.
- A whole tide session logs under its one debug id.
- `main` passes the user's home directory to the cli, as it does the working directory, for tide to show paths with `~`.
- A name field appears only when a name is taken: clone and local open name the project after the URL or directory, as the CLI does.
- Tests: tide's own tests drive its model with keys against a real daemon in the test process and cover the journey's cases; the cli's pty tests (as Phase A's `TestTide`) run one or two journeys end to end in a real terminal.

## Steps

Each step ends with `make check` and `make test` passing, and is committed when the human asks.

1. **`docs:`** commit the docs/TIDE.md changes made since `db3f6d6` and this file.
2. **`feat:` tide talks to the daemon.**
   - `tide.New` takes the client, the working directory and the user's home besides the logger and style; `main` reads the home into `Env`.
   - Every request runs as a `tea.Cmd` and comes back as a message; screens show "loading…" meanwhile.
   - At start: list the projects; open the switcher when any is open, `[Open Project]` otherwise. No daemon: the daemon-not-running screen with the hint `wdx daemon start` and `r` to retry.
   - AGENTS.md: `cli -> tide -> client/style`. `.golangci.yml`: tide's test files, and only those, may import the daemon, so tide's tests run a real daemon in the test process as the cli's do.
3. **`feat:` the left pane's screen stack.** The `screen` type of docs/TIDE.md drawing into the rectangle it gets; `Esc` pops; a finished screen hands its result to the one below; titles on the frame (`[Project: <name>]`, `[Open Project › Local]`). While no project is shown: the terminal pane hidden, the agent pane saying "no project open".
4. **`feat:` `[Open Project]` and reopening.** The list of local, clone and create, then the closed projects by name (listed when it opens); `enter` on a closed project reopens it (PATCH state open). `Esc` returns to the project shown, if any. `C-q o` opens it.
5. **`feat:` the directory browser and opening a local repository.** Only directories, repositories marked `(git)` (a `.git` inside), the path on the first line with `~`; ↑ / ↓, → or `enter` into a directory, ← or Backspace up; typing filters case-insensitively, prefix matches first, hidden directories only when the filter starts with `.`; PageUp / PageDown, Home / End; unreadable directories say so. Directories are read in a `tea.Cmd`. Local: `enter` on a repository opens it; "open this directory" when the current one is in a repository. Starts in tide's working directory.
6. **`feat:` cloning and creating.** Clone: a URL field, "cloning…" (Esc cannot cancel), a name field only on `name_taken`. Create: the browser from `~/.wdx/projects`, "create the project here", a name field. The choice screens: create's conflict (open that project, another name, cancel) and a missing checkout on clone or reopen (clone it again, remove it). Errors show in the screen, which stays.
7. **`feat:` switching projects.** The switcher (`C-q p`, and at start): a box over the window listing every project, open ones then closed, by name; fuzzy filter; ↑ / ↓; `enter` switches, reopening a closed one; `Esc` closes. The strip: one row above the status line, the open projects by name, the current one marked, scrolled to keep it in view, hidden a second after the last tap or on any other key. `Alt+Shift+←/→` switch at once on each tap, going round. The status line names the current project.
8. **`docs:`** docs/TIDE.md marks B1 implemented; this file records what was built and what changed on the way.

## Notes for resuming

What Phase A left in the code, which B1 builds on:

- `internal/tide/layout.go`: the layout state (`terminalOpen`, `zoomed`, `focus`, `lastTop`), its actions and `frames`, the pure sizing; `draw.go`: rendering on an ultraviolet `ScreenBuffer` (borders joined by `joins` bits, labels on the top borders, the menu box, the status line); `tide.go`: the Bubble Tea model, keys and mouse; `placeholder.go`: the stand-in panes.
- Bubble Tea v2 names keys as `ctrl+]`, `ctrl+\`, `ctrl+q`, `alt+up`; both encodings of Alt+arrow arrive as `alt+…`, and Alt+Shift+arrow as `alt+shift+…`.
- `internal/cli/tide.go` runs it on the controlling terminal, like attach, and prints `tide closed` on quit (every cli run must log the response it printed). `internal/cli/attach_test.go`'s `startInTerminal` runs any command line in a pty for end-to-end tests; `internal/cli/tide_test.go` uses it.
- The branch tracks no upstream on purpose (it was created tracking `origin/main`); push it with an explicit `git push -u origin tide-skeleton`.
- Run the checks from the worktree: `make check`, `make test`. Use git and make directly in this repo, not warden's tools.

## Progress

- Step 1: committed (`f7a86b6`).
- Step 2: committed (`c7c5683`). tide starts focused on the project pane, which says "loading…", then the daemon-not-running message (`r` retries there), the error of a failed list, or for now the open and closed projects as text, until steps 3 and 4 replace it. The switcher is for now a box listing the open projects, which `Esc` closes; step 7 makes it the real one. A daemon of another version gets its warning once tide gives the window back, since a write while tide draws would land in it. Fixed on the way: `.golangci.yml`'s rule against importing `internal/cli` matched `internal/client` too; it is anchored now.
- Steps 3 and 4: committed together (`1c1769b`). The `screen` interface draws into the rectangle it gets (docs/TIDE.md); screens pop and ask for requests through messages, which the model handles, so screens hold no client. `[Open Project]` lists its three sources, which say "not built yet" on `enter` until steps 5 and 6, then the closed projects; `enter` reopens one, a failure shows in the screen, wrapped to its width. A project shown becomes the root, `[Project: <name>]`, and brings the terminal pane the first time; until then `C-\` does nothing and the agent pane says "no project open". `C-q o` pushes `[Open Project]` (once), focusing the project pane and moving a zoom to it; `C-q p` opens the switcher box, still without selection until step 7. While the switcher is open it takes every key but `C-q`. The status line starts with the project shown.
- Step 7: committed (`35e105d`), then the popups framed as the human sketched (`5beec4e`). The switcher lists every project, open ones then closed, by name; typing filters by letters in order (`gm` finds `gamma`), Backspace undoes, ↑ / ↓ select, `enter` switches or reopens, a failed reopen shows in the box, which stays. While it is open it takes every key but `C-q`, so `j` / `k` type. `Alt+Shift+←/→` step through the open projects, going round, from no project to the first or last; each tap switches at once from the projects tide already has. The strip is a bar row above the status line, hidden a second after the last tap or on any other key. Terminals attaching after the selection rests waits for terminals in the panes.
- Step 5: committed (`8ff102f`). `[Open Project › Local]` starts in tide's working directory (the user's home when unknown). Directories are listed in a `tea.Cmd`, links to directories included; the path line loses its start when too long, since its end matters. Typing filters (so letters type, as in the switcher), Backspace undoes, then goes up; Esc pops even while a directory is being read, the listing then dropped. The status line shows the browser's keys while it has the focus. `enter` on a repository opens it through `createProject` with a local source; a failure (a name taken, for now) shows in the browser. The name field on `name_taken` comes with step 6. Tried by hand, it works; ← and → to move between directories did not feel natural, so how the browser moves is to be looked at again after B1.
- Step 6: committed (`5aa0078`). `[Open Project › Clone]` is a URL field (paste works, line breaks dropped; a long value keeps its end in view); `[Open Project › Create]` is the directory browser in create mode, from `~/.wdx/projects` or its closest parent that exists, whose first entry "create the project here" asks the name. A field and a choice wait for the daemon's answer and ignore keys meanwhile, Esc included. The answers to the CLI's prompts are screens pushed over the one that asked, which shows the failure too, so `Esc` goes back to it: a name taken on local open or clone asks another name and sends the request again with it; create's conflict (name taken, or the directory in a repository) offers open that project, try another name (back to the name field) and cancel (back to `[Open Project]`); a missing checkout offers clone it again (only with an origin) and remove it, through `on_missing_checkout` for a create and `RecloneProject` / `DeleteProject` for a reopen. From the switcher such a question closes it and goes to the project pane. A removed project leaves `[Open Project]` saying "removed <name>". Screens may say their keys on the status line (`hint()`).
- Step 8: docs/TIDE.md marks B1 implemented and says what B1 left for later.

## What changed on the way

- The steps were reordered so each ends with something to use; steps 3 and 4 were committed together.
- The popups (the switcher, the `C-q` menu) got a heavy frame with a bold title, the body and the keys set apart by lines, as the human sketched; docs/TIDE.md shows the shape.
- Requests are answered by messages to the model, which holds the client; screens only send messages. The strip's timer is injectable (`Model.tick`), so tests run what follows from each key synchronously.
- A failure needing an answer pushes a screen over the one that asked, which keeps the failure; from the switcher it closes the switcher and goes to the project pane.
- Left for later: terminals attaching once a switch rests (it needs terminals in the panes), following changes made outside tide (the event stream), and how the directory browser moves, which did not feel natural with ← / →.
