# tide's state

Status: approved 2026-11-03; steps 1, 2, 3a, 3b and 4a built on 2026-11-03.

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

## Step 3b: tide keeps its tabs through the daemon

Today a project's tabs live in its `agentList` (`internal/tide/agents.go`), as strings: an agent's id, `info:` and an id, `settings`, `doc:` and an id, `file:`, `diff:` or `pr:` and a worktree and a file. tide follows only the stream of the project shown, and reads its tabs from that stream's events.

**tide writes**
- Every change to a project's tabs (one opened, closed or moved, or the list's first fill) sends the whole list with `replaceTabs`, in the background. An empty list is sent as `[]`.
- The string form stays inside tide. Two functions turn a tab into an `api.Tab` and back. A string they do not know is not sent.
- One request at a time per project. A change made while one is on its way waits, and only the latest list is sent once it returns. Requests sent at the same time could reach the daemon in any order, and the older list could win.
- A refusal or a daemon that does not answer is logged, and the status line says `tabs not saved: <why>`. Nothing is retried: the next change sends the list again, and the next event brings the daemon's.

**tide reads**
- An event's `tabs` replace the project's when no request of tide's is waiting or on its way. Otherwise they are older than what tide has and are dropped. So what differs is what something else removed: a cascade, a trigger, or a file deleted or renamed through wdx. A file renamed through wdx keeps its tab under the new path.
- The current tab stays when it is still in the list, and otherwise falls back as closing a tab does today: to the tab on its right, or on its left when it was the last. The focus leaves the agent pane when no tab is left, as today.
- `tabsChanged`, which closes the tabs of agents gone or just removed, goes: the cascades and the trigger do it in the daemon, and the next event brings it.

**A project's first event**
- Its saved tabs open, and the first is current until step 4 keeps which one was.
- With none saved, tide does as today: the first running agent gets a tab, which is then sent.
- **`C-q r`:** a project with saved tabs takes them, and `--resume`'s `agent` picks the current one if it is among them. A project with none saved takes `--resume`'s `tabs` and sends them. That is what an older release reloading into this one hands over, before the daemon has any.

**A tombstone's tab.** Today tide lets you open the tab of an agent that is already a tombstone, to read what it did, and keeps it. 3a's `replaceTabs` refuses an agent kept only as a tombstone. 3b drops that refusal: the agent is still the project's. The trigger closes the tabs open at the moment the agent becomes a tombstone, as `tabsChanged` does now. One opened after that stays until the row goes with its last child, which the cascade then closes. The API's description and the store's test change with it.

**Not in 3b:** which tab is current, the cursor, focus and zoom (step 4), and missing tabs (step 5). A file deleted outside wdx keeps showing that it is gone, as today.

**CLI_TIDE.md** gets a row under Projects: keep the tabs open across runs: tide only, by design, since the CLI opens no tabs.

**Tests**
- tide's model tests, with a daemon in the test process:
  - opening and closing tabs sends the list;
  - a removal through the API closes its tab from the stream;
  - a new tide on the project opens the saved tabs, and with none the first running agent;
  - a reload with `--resume` and no saved tabs sends those tabs;
  - a stream event that comes while a request is on its way does not undo it.
- The CLI's `TestTideReload` keeps passing.

## View

What tide shows besides its tabs, so that it comes back as it was left. The daemon keeps it and reads nothing in it: only tide reads it, and one tide runs per home.

**What is kept**
- For tide as a whole: the project shown, the pane focused, the one zoomed, the top pane focused last (where ↑ from the terminal goes), and whether the terminal pane is open.
- For each project:
  - the current tab;
  - the terminal tab selected;
  - the cursor: its section and what it is on (an agent's id, a worktree's row, a file in Code, a doc's id);
  - the agents folded, the worktrees unfolded, and in Code the worktree chosen and the directories unfolded in each.
- What is in progress or follows the window is not kept: an open popup or confirm, text being typed, the filter, scroll offsets, and the mouse hover.
- A tab, a cursor or a fold whose target is gone falls back as it does today: the first tab, the nearest row, and folds that name nothing are ignored.

**Where**
- Table `tide_view`, one row, and `tide_project_views`, one row per project (`project_id`, `ON DELETE CASCADE`), each holding the view as JSON in a `TEXT` column with a `json_valid` `CHECK`.
- It is JSON rather than a column per field because the daemon does nothing with it but keep it. It refuses only what is not a JSON object. tide adds fields as its view grows, as it does for `--resume` today, and ignores what it does not know, with no migration for each.
- `GET` and `PUT /v1/tide/view` (`getTideView`, `replaceTideView`), and `GET` and `PUT /v1/projects/{project}/view` (`getProjectView`, `replaceProjectView`). The body is `{"view": {...}}`. A `PUT` answers `{"result": "updated", "view": {...}}`. A `GET` of a view never saved answers `{"view": {}}`.
- The project's stream does not carry it, and a `PUT` of a view wakes no stream: tide is its only writer and reader. A stream event costs the daemon a read of the project, with git, and the cursor moves at every key.
- A `PUT` of a view does not wait behind other changes, as a setting does not: it touches only these tables, and must not wait out a clone.

**When tide writes**
- When the view changed and then stayed the same for half a second (`viewSettle`, which tests shorten). Holding ↓ sends one request, not one per row. One request is on its way at a time, as with tabs.
- On `C-q q` and `C-q r`, at once, waiting at most a second, so what was just done is not lost. A tide killed loses at most the last half second.
- A failure is logged and not shown: the view is a convenience, and the next change sends it again.

**When tide reads**
- At start, the view for tide as a whole: the project it names is shown when it is still open, and the switcher shows otherwise, as today.
- A project's view, when tide first shows the project, before its stream's first event places what it names.
- **`C-q r`** still passes `--resume`, for a reload into an older release. A release that reads the daemon's view uses it, and `--resume` only when the daemon has none.

**Steps**
- **4a:** the tables, the four endpoints, and the `PUT`s let past the line of changes and waking no stream, tested from the daemon.
- **4b:** tide writes and reads the view, and `C-q r` leans on it.

**CLI_TIDE.md:** the row for keeping the tabs becomes keeping what tide showed, tabs and view.

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
4. The view: its tables and API (4a), and tide writing and reading it (4b).
5. Missing tabs.
