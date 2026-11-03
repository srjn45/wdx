# tide's state

Status: approved 2026-11-03; steps 1 and 2 built on 2026-11-03.

When you come back to tide, it shows what you left: the same project, tabs, cursor and focus. wdx is one binary (the daemon, the CLI and tide) with one state. The daemon keeps tide's state, so anything done through wdx, from the CLI or from tide, shows up in it whether tide is open or not.

A daemon has one tide. Several tides on one daemon would need their changes reconciled, which is left out.

## Who owns what

| state | owner |
|---|---|
| tide's tabs and view | the daemon the tide is part of |
| a web-tide's tabs and view (later) | hub-be, per user, per node, per web-tide |
| a machine's agents, worktrees, docs, plans, code | that machine's daemon |

Later, a wdx on another machine reaching this one through the hub keeps its own tide's state in its own daemon, while this machine's daemon keeps both in sync on the resources. Both see the same filesystem, so a change made outside wdx shows as missing in both. That is the hub's design, not this one. This design is local only, and its one part that will have to change for remote is noted below.

## Today

- The view (tabs, current tab, cursor, focus, zoom, the Code section's worktree) lives in tide's memory (`agentList`, `internal/tide/agents.go:38`) and ends with it.
- `C-q r` hands one project's view to the next run through `--resume` (`Resume`, `internal/tide/reload.go:44`), and drops a tab whose agent is gone.
- A new tide opens only the first running agent.
- Nothing stops a second tide.

## One tide per daemon

- `wdx tide` takes an exclusive lock on `<home>/tide.lock` (flock) and keeps it for as long as it runs. The operating system frees it when tide ends, even when killed, so a crash never leaves it stuck.
- A second `wdx tide` fails with a new CLI-only error, `tide_running` (exit 1): `a tide is already running for this home`, with the pid of the one running.
- `C-q r` execs the new release, which closes the lock's file and takes the lock again.
- `docs/TIDE_CODE.md`'s test of two tides on one editor goes.

## Tabs

Table `tide_tabs`, in the migration `0017_tide_tabs.sql`: one row per open tab per project, in order. The package `internal/tidestate` keeps it.

| column | |
|---|---|
| `project_id` | the project, `ON DELETE CASCADE` |
| `position` | its place in the strip, from 0; the key is (`project_id`, `position`) |
| `kind` | `agent`, `info`, `settings`, `file`, `diff`, `pr`, `doc` |
| `agent_id` | for `agent` and `info`: `REFERENCES agents (id) ON DELETE CASCADE` |
| `doc_id` | for `doc`: `REFERENCES docs (id) ON DELETE CASCADE` |
| `worktree` | for `file`, `diff` and `pr`: the worktree's name, `main` for the clone, as the worktree API and tide name it. Not a foreign key, since git lists checkouts wdx never registered, as `verified_files` and `review_comments` say |
| `path` | for `file` and `diff`: the file, relative to the worktree |

A `CHECK` gives each kind exactly the columns it needs. No label is kept: only a file or a worktree can go missing (agents and docs are only removed through wdx), and its path names it.

An editor's tab is not a row: editors are terminals, and their tabs follow the terminals, as now. Which view a `pr` tab shows (commits or what is not committed) is view state, step 4.

**What removes or changes rows**
- A project, an agent or a doc deleted: the foreign keys' cascades.
- An agent removed while it has children stays as a tombstone (`agents.removed_at`), so no cascade fires. A trigger on that column deletes its tabs. It sits in the same migration, so no domain code has to remember it.
- A worktree removed through wdx (`RemoveWorktree`): the daemon deletes its tabs, as it already forgets its pull request.
- A file or directory deleted through wdx (`RemoveWorktreeFile`): the daemon deletes the tabs of that path and of every path under it.
- A file or directory renamed (`RenameWorktreeFile`): the tabs of that path and under it follow, with the new path.
- A closed project keeps its tabs.
- **Remote (later):** a tab will then point at another daemon's resources, which no cascade reaches. That part will be redesigned with remote access.

**API**
- `PUT /v1/projects/{project}/tabs` (`replaceTabs`), body `{"tabs": [Tab]}`, replaces the project's whole list in one transaction. It answers 200 with `{"result": "updated", "tabs": [...]}`.
  - A `Tab` is `{"kind", "agent_id", "doc_id", "worktree", "path"}`, with absent fields `null`.
  - It fails with `invalid_argument` (422) for a kind given the wrong fields, or an agent or doc that is not the project's. The same tab twice is refused too. It fails with `not_found` for no such project.
  - It is not refused for a closed project, as tide can show one's tabs.
- The project stream's events gain `tabs`, always present, `[]` for none. A change to the tabs, by `replaceTabs` or by a removal above, sends an event, as any change to the project does.
- `getProject` and `wdx project get` do not show them, as tabs are tide's alone.

**Step 3a** builds the migration, `internal/tidestate`, the endpoint, the stream's `tabs` and the removals, tested from the daemon. tide does not use them yet. **Step 3b** has tide send its list on every change and show the list the stream brings, and a project starts on its saved tabs.

## View

Table `tide_view`, one row, and `tide_project_views`, one row per project.

- `tide_view`: the project shown, focus, zoom, whether the terminal pane is open.
- `tide_project_views` (`project_id`, `ON DELETE CASCADE`): the current tab, the terminal tab selected, the cursor's section and what it is on (an agent's id, a worktree, a file, a doc), the folded parents and the unfolded worktrees, and the Code worktree.

What is in progress or follows the window is not kept: an open prompt or confirm dialog, text being typed, scroll offsets and the mouse hover.

A current tab or cursor whose target is gone falls back as it does today: the first tab, and the nearest row.

## How tide reads and writes it

- tide writes its tabs and view through the API on each change, with endpoints only tide calls. There is no `wdx tab` command, since tabs are tide's alone. CLI_TIDE.md gets a row.
- At start, tide reads the view and the shown project's tabs. A project with no saved view starts as today.
- The project's stream carries its tabs, so a tab removed by the CLI goes from tide at once.
- **`C-q r`:** the new release reads the view from the daemon. `--resume` is still accepted, because older releases reload into it. When the daemon has a view, the daemon's view wins.

## Missing tabs

A tab whose row is still there but whose target is gone was removed outside wdx. Examples: a file deleted or moved with `rm` or `mv`, or a worktree's directory deleted by hand (wdx already marks that worktree `missing`).

- Such a tab shows as `[✗ label(-)]`. Its pane says what is gone and where it was, for example `file internal/x.go no longer exists in worktree feat-a`.
- It stays until it is closed with `C-q -` or `(-)`.
- If the file comes back, the tab shows it again.

## `tide.section_order`

- The default becomes `awdpc`: Agents, Worktrees, Docs, Plans, Code.
- `p` is accepted now and shows nothing until Plans is built.
- A letter left out **hides** its section, where today it moves to the end.
- An empty value, an unknown letter or a letter twice is refused.
- This changes what an existing value means, for example `awc` now hides Docs. So it ships as `feat!:` with a `BREAKING CHANGE:` footer.

## Steps

1. `section_order`.
2. One tide per daemon.
3. The `tide_tabs` table, its API and stream, the cascades and the file rename and delete (3a), with tide reading and writing its tabs through them (3b).
4. The view tables.
5. Missing tabs.
