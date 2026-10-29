# tide Phase B1: projects

Status: in progress; steps 1 and 2 are built (see Progress). Built in the worktree `~/dev/warden-rebuild-tide`, branch `tide-skeleton`, rebased on `origin/main`.

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
- Step 2: built. tide starts focused on the project pane, which says "loading…", then the daemon-not-running message (`r` retries there), the error of a failed list, or for now the open and closed projects as text, until steps 3 and 4 replace it. The switcher is for now a box listing the open projects, which `Esc` closes; step 7 makes it the real one. A daemon of another version gets its warning once tide gives the window back, since a write while tide draws would land in it. Fixed on the way: `.golangci.yml`'s rule against importing `internal/cli` matched `internal/client` too; it is anchored now.
