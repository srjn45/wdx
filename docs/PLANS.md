# Plans

Status: being designed with the human, started on 2026-11-03. Step 1, plans written, read and checked with nothing running, built on 2026-11-03; step 2, running a plan, designed in detail and approved on 2026-11-03 (Step 2 in detail), built on 2026-11-03; step 3, the person's part, designed in detail and approved on 2026-11-04 (Step 3 in detail), built on 2026-11-04; step 4 is next. "Agreed" is what the human said; "Proposed" came from the AI and is not yet approved. docs/IDE.md has the wider picture, docs/DOCS.md the docs plans are kept as.

A plan is the work of a feature broken into tasks, each a prompt for an agent, with the tasks it must wait for. wdx runs it: it starts an agent for every task that can run, lands each finished task on the plan's branch, and starts what that unblocked, until every task is done. The person steps in where an agent cannot go on.

## Agreed (2026-11-03)

- A plan is a DAG of tasks, which makes it a pipeline: wdx has no pipelines apart from plans. Warden's four run modes were the same DAG run in more complicated ways; wdx has one.
- wdx itself runs a plan, with no agent in charge: it starts an agent for each task that can run, and moves on as each finishes.
- A plan has a branch of its own, as warden's autopilot had.
- Each finished task is landed on the plan's branch by rebase, so that its commits are kept and the plan goes on locally. Once the plan is done, the person lands its branch on the base by PR or by rebase, as they choose.
- Tasks run in slots: worktrees that belong to the plan, not to a task, each lent to one task at a time and taken by the next once it has landed. A plan starts with one slot and adds one only when a task is ready and none is free, up to the most tasks a plan runs at once. Its slots go with it: when it is done, they are removed.
- Inside, a plan is a doc, to reuse revisions and their conflicts. To the person it is a plan: `wdx plan …`, a Plans section of its own in tide. Plans have no comments or approvals from peers.
- A task is done when its agent says so, with a summary (warden's `emit`).
- When a task cannot go on (its agent waits for input, a landing conflicts, an agent ended without finishing), the plan waits for the person. For a conflict they choose to have an agent resolve it or to resolve it themselves in the diff; either way it starts only when they ask. Resolving such things by itself, with a brain agent of wdx's per plan, comes later, as the plan's autonomous mode.
- Landing a task runs the project's landing check, so that the plan's branch stays green and a break shows on the task that made it. How many tasks run at once is configurable: a setting for every plan, which a run can override.
- A plan has a lifecycle (below): `draft`, `running`, `paused`, `done`, `cancelled`, and `archived` to hide it. It runs once; running it again means copying it.
- A task retried resumes its agent, which keeps what it knew, when there is one to resume; otherwise, or when asked for a fresh start, a new agent.
- tide: a plan chosen in the left pane opens in a right-pane tab listing its tasks in DAG order, each with its status. `Enter` on a done task shows its summary, on a running one goes to its agent, on a pending one shows its prompt.
- A plan's name is kept with a `plan:` prefix, so that it never clashes with a doc's (below).
- Plan names and task ids have only letters, numbers and `-`, no `_`: they go into agents' names (`<plan>-<task>`), which cannot hold `_`.
- A child plan's parent is stored with it (`docs.parent_id`), written when the parent is saved, rather than found by reading every parent's YAML.
- An AI writes most plans, through the CLI: the YAML comes on stdin (`--path -`) or from a file.
- **Plans of plans.** A big plan is broken into smaller ones, each a PR, so that no branch lives long. A parent plan lists child plans, with `after` between them, as a plan lists tasks: it is their group, and the DAG they run in. A parent has no branch, worktree or slots, and lands nothing: each child lands itself, and the parent is done when they all are.
- **Stacking.** When a child's `after` plans are done but their PRs are not merged: without autonomous mode the parent waits for the person, who either has the PR reviewed and merged (the child then starts from the default branch) or lets the child go on now, stacked: its branch starts from the earlier plan's, and its PR targets that branch. Autonomous mode always stacks. A child that waits for more than one plan (after reduction) is never stacked, in either mode: it waits until all of them are merged into the default branch, and starts from it.
- **A DAG is reduced when it is saved.** An `after` that another path already covers is dropped: with C after B, B after A, and C after A, C's `after: [A]` goes, as B already waits for A. For tasks and child plans alike.

## Why no split and join steps

Warden's pipelines inserted hidden jobs (`internal/pipeline/spec.go`, `injectSpanNodes`): a span-out after a job with several children made a branch for each child off the parent's, and a span-in before a job with several parents made a branch from the first parent and merged the others into it, adding a conflict job when the merge failed. A chain of jobs shared one worktree. The task agents never touched branches.

Landing each task on the plan's branch does the same with nothing inserted. Every task starts from the plan's branch as it is when the task starts, so:

- Tasks that can run at once each start from the same branch, in slots of their own: the split.
- A task with several tasks before it starts only once all of them have landed, so the branch already holds all their work: the join.
- A conflict shows when a task lands after another changed the same lines, on that task alone, the one whose agent knows the change.

A task also gets the work of tasks it does not wait for, if they landed first. That is the plan's state at the time, which is what a person working on the branch would see.

Warden made fewer worktrees than a worktree per task would, as a chain shared one. Slots make fewer still: warden made one for every child at a split and one at every join, while a plan makes one only when more tasks run at once than it has slots. For A, then B and C at once, then D after both, warden made four worktrees and a plan makes two; both have two at most at once. A slot reused also keeps what is ignored in it, such as built files and installed dependencies, which a new worktree would make again.

## Proposed

### The definition

YAML, checked whenever it is saved:

```yaml
goal: >-
  Users log in with SSO.
constraints:
  - Run make check before each commit.
tasks:
  - id: schema
    prompt: >-
      Add the sso_identities table ...
  - id: api
    after: [schema]
    prompt: >-
      ...
  - id: cli
    after: [schema]
    prompt: ...
  - id: docs
    after: [api, cli]
    prompt: ...
```

- `goal` is required: every task's agent gets it, so that it knows what its part is for. `constraints` is optional, and every agent gets it too.
- A task has an `id` (a name, as agents' names are), a `prompt`, and `after`, the tasks it waits for. Tasks with no `after` start at once. Nothing is chained by itself, as warden did with tasks that had no edges: a plan says what waits.
- Refused with `invalid_plan`, every problem listed with its line in `details.problems`: YAML that does not parse, an unknown key, a missing `goal` or `prompt`, a duplicate id, an `after` naming no task or the task itself, a cycle (naming its tasks), no tasks.
- "DAG order", wherever tasks are listed, is a topological order that keeps the file's order among tasks free to go in either, so that it reads as written.
- Left out from warden: `done_when` (the agents' summaries and the person say when it is done), per-task worktree, ACA, model or role (they come with the agent parameters of docs/CHILD_AGENTS.md, if wanted), `run_if` (a failure waits for the person).

### Kept as a doc

- `docs` gets a column `kind`, `doc` or `plan`, `doc` by default, and a plan's name is stored as `plan:<name>`, which no doc's can be, as a doc's name cannot hold `:`. So a plan and a doc can have the same name (Step 1 in detail says why the prefix). A binary from before it, after `wdx update --rollback`, would list plans among the docs, as `plan:…`.
- Every `doc` command and request leaves plans out, and every `plan` one docs.
- A plan has revisions, `plan_changed` on a stale base (the plan's `doc_changed`), and the hidden copy (`<home>/plans/<project>/<name>.yaml`), as a doc. The doc statuses do not apply: a plan's state is its run's.
- `sync-to-git` and the hub for plans come later, with the hub. Only the definition, the coarse state and the owner are shared (docs/HUB.md); the run stays on the machine.

## Architecture (proposed)

### The parts

```
  wdx plan …  /  tide ──API──▶ daemon: plans.go handlers
                                  │            ▲ notices (project stream)
                                  ▼            │
                     internal/plan ─────────────┤
                     ├ definition: parse, check, DAG order
                     ├ Store: docs (kind plan), plan_runs, plan_tasks, plan_slots
                     ├ Next: what to do now, from the state alone
                     └ Runner: does it, through RunnerOptions the daemon gives
                                  │
             ┌────────────────────┼─────────────────────────┐
             ▼                    ▼                         ▼
     agent.Manager        slots: git worktree,       landing: worktree's rebase
     create, start,       switch, update-ref         and move, with the plan's
     stop, send           (internal/git)             branch as the base
```

- **`internal/plan`**, a new domain package, holds everything about plans and nothing about agents or terminals:
  - `definition.go`: the YAML's types, its check, DAG order. Pure, no I/O.
  - `store.go`: plans through `doc.Store` (kind `plan`), and the run's three tables.
  - `next.go`: `Next(run) []Action`, which reads a run's state and says what to do: start this task in that slot, land that one, finish. Pure, so that every case of the DAG is a table test with no process, as warden's `pipeline.Plan` was.
  - `runner.go`: the `Runner`, which wakes on changes, calls `Next` and carries out its actions through `RunnerOptions`, a struct of functions the daemon fills (as `worktree.LanderOptions`): `CreateAgent`, `StartAgent`, `StopAgent`, `SendAgent`, `RunCheck`. So `plan` uses `doc`, `git` and `worktree`'s landing, never `agent` or `terminal`.
- **`internal/daemon/plans.go`**: the API handlers, the `RunnerOptions` wiring, and `AgentEnded` telling the runner.
- **`internal/worktree`**: the rebase-and-move of `Lander.run` becomes a function both use, `LandOnto(ctx, path, base, check)`, as the lander's base is always the default branch and a task's is the plan's branch.
- **`internal/agent`**: `Manager.Stop(id)`, stopping one agent, which nothing needed until now.
- Dependencies: `daemon -> plan -> doc/git/worktree/db`. AGENTS.md's layout and depguard get the line.

### The tables

```sql
CREATE TABLE plan_runs (
	plan_id     TEXT PRIMARY KEY REFERENCES docs (id) ON DELETE CASCADE,
	revision    INTEGER NOT NULL,           -- of the plan, as it started
	base        TEXT NOT NULL,              -- the default branch it started from
	branch      TEXT NOT NULL,              -- plan/<name>
	state       TEXT NOT NULL CHECK (state IN ('running', 'paused', 'done', 'cancelled')),
	parallel    INTEGER,                    -- the most tasks at once; NULL follows plan.parallel
	started_at  INTEGER NOT NULL,
	finished_at INTEGER,                    -- done or cancelled
	archived_at INTEGER                     -- hidden from plan list
) STRICT;

CREATE TABLE plan_tasks (
	plan_id     TEXT NOT NULL REFERENCES plan_runs (plan_id) ON DELETE CASCADE,
	task_id     TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('pending', 'running', 'landing', 'land_failed', 'failed', 'done', 'skipped')),
	failure     TEXT,                       -- why it is failed or land_failed, below
	conflicts   TEXT CHECK (conflicts IS NULL OR json_valid(conflicts)), -- the files in conflict, when land_failed with rebase
	agent_id    TEXT REFERENCES agents (id) ON DELETE SET NULL,
	slot        INTEGER,                    -- the slot it holds, while it holds one
	summary     TEXT,
	started_at  INTEGER,
	finished_at INTEGER,
	PRIMARY KEY (plan_id, task_id)
) STRICT;
CREATE INDEX plan_tasks_agent ON plan_tasks (agent_id);

CREATE TABLE plan_slots (
	plan_id    TEXT NOT NULL REFERENCES plan_runs (plan_id) ON DELETE CASCADE,
	slot       INTEGER NOT NULL,            -- 1, 2, …
	path       TEXT NOT NULL,               -- <root>/.worktrees/<plan>-<slot>
	created_at INTEGER NOT NULL,
	PRIMARY KEY (plan_id, slot)
) STRICT;
```

- A plan runs once, so a run is keyed by its plan, and a plan with no run is a `draft`. Its definition is the revision it started with.
- The tasks' rows are written whole when the run starts, from that revision. A slot is free when no task's `slot` names it.
- All three tables are new, so the migration only adds.

### The plan's lifecycle

```
draft ──run──▶ running ⇄ paused
                 │    └──cancel──▶ cancelled
                 ▼
                done
done, cancelled ──archive──▶ archived ──unarchive──▶ done or cancelled again
```

| State | What it means |
|---|---|
| `draft` | Not run: no row in `plan_runs`. The only state in which it can be edited, renamed or removed; anything else is refused with `plan_started`. |
| `running` | wdx starts and lands its tasks. |
| `paused` | Nothing new starts; running agents go on and land. |
| `done` | Every task `done` or `skipped`; its worktree waits for the person to land it. |
| `cancelled` | Given up with `plan cancel`: its agents are stopped, slots holding work become ordinary worktrees and free ones are removed, as wdx never deletes work. Its branch stays. |
| `archived` | `done` or `cancelled`, with `archived_at` set: left out of `plan list` unless `--all`, and kept with its summaries. |

- From warden's `pending ⇄ in_progress → completed`, plus `archived`: `draft` is `pending`, and there is no way back to it, as a plan runs once. `plan copy --name <new>` makes a new draft of a plan's latest revision, to run again or to start from.
- No `landed` state: under a `done` plan, tide shows its worktree's landing (landed, or a PR open), which the worktree already knows. A state of its own would need following PRs on GitHub, which wdx does only when asked.
- Only a `done` or `cancelled` plan is archived; a draft not wanted is removed.
- Removing a plan that has run is refused (`plan_started`); archiving hides it. Removing its project removes it with everything.

### The states

A run:

| From | To | When |
|---|---|---|
| (none) | `running` | `plan run` |
| `running` | `paused` | `plan pause`: nothing new starts; running agents go on and land |
| `paused` | `running` | `plan resume` |
| `running`, `paused` | `cancelled` | `plan cancel` |
| `running` | `done` | every task `done` or `skipped` |

A task:

| From | To | When |
|---|---|---|
| `pending` | `running` | every task in its `after` is `done` or `skipped`, a slot is had, its agent started |
| `running` | `landing` | `task done` from its agent, or from the person |
| `running` | `failed` | its agent ended without `done` (`agent_ended`), or did not start (`start_failed`) |
| `landing` | `done` | rebased onto the plan's branch, checked, and the branch moved |
| `landing` | `land_failed` | the rebase stopped on a conflict (`rebase`), or the check failed (`check`) |
| `land_failed` | `landing` | `task land`, once the person or an agent they asked has dealt with it |
| `failed` | `running` | `task retry` |
| `failed`, `land_failed` | `landing` | `task done` from the person, who finished it themselves |
| `failed`, `land_failed`, `pending` | `skipped` | `task skip`: what waits for it goes on without its work |

Two states mean the person is needed, named by what did not happen: `failed`, the work did not finish; `land_failed`, it finished but did not land, with `failure` saying why (`rebase` or `check`).

`pending` is stored as one state, and shown as two, worked out when shown: `waiting for api, cli` while tasks in its `after` are not `done` or `skipped`, and `ready` when only a slot is missing. So there are no more changes of state to keep right.

Every change is one `UPDATE … WHERE state = <from>`, so that a request and the runner acting on one task at once cannot both win; the loser reads again.

A slot is free or held, which the tasks say. Free, it is detached at the tip of the plan's branch as it was when freed: a branch checked out in a worktree cannot be deleted, and detached it holds none.

### The runner

One goroutine for the daemon. It keeps a set of plans to look at, which anything can add to without blocking (as `notices`), and goes through them one at a time:

1. Read the run whole.
2. `Next` says what to do. Done when it says nothing.
3. Do the first step, write its outcome, and go back to 1.

What wakes a plan: `plan run` and `resume`, `task done`, `land`, `retry` and `skip`, a landing that ended, an agent of a task that ended (the supervisor's `AgentEnded`, looked up through `plan_tasks_agent`), and the daemon starting, which wakes every run not `done`. Never a timer.

`Next`, in order, for a run not `done`:

1. A task in `landing` with no landing going: land it. One landing per plan at a time, so that each rebases onto the last.
2. If `running`: for each `pending` task in DAG order whose `after` are all `done` or `skipped`, while a slot is free or fewer exist than the run's `parallel` (else `plan.parallel`): start it.
3. Every task `done` or `skipped`, and no landing going: finish.

A landing runs in a goroutine of its own, as a check can take many minutes, and wakes the plan when it ends; everything else is quick and runs in the runner.

### Starting a task

1. Hold a slot: write the task's `slot`, a free one or the next number, with the task still `pending`, so that a crash leaves a slot held by a task that never started, which step 1 of the next go finds.
2. Put it on the task's branch: in a free slot `git switch -c task/<plan>/<task> plan/<name>`, which rewrites only the files that differ; for a new one `git worktree add <root>/.worktrees/<plan>-<n> -b task/<plan>/<task> plan/<name>`, and its `plan_slots` row. A branch of that name already there is used if the plan's branch holds it, as a retry after a crash; otherwise `start_failed`.
3. `CreateAgent` in the slot, named `<plan>-<task>`, with the project's default ACA; the task becomes `running` with its `agent_id`, in the same transaction as the agent's row.
4. `StartAgent`, then `SendAgent` with its first message.
5. Any step failing makes the task `failed` with `start_failed`, keeping its slot and its message in the log, for the person to `retry` or `skip`.

The first message, built by wdx and never logged:

```
You are doing task <task> of the plan <plan>, run by wdx.
Goal: <goal>
Constraints: <each>
Done before you, which this task waits for:
- <task>: <summary>
Your task:
<prompt>
When it is done, commit your work, then run:
    wdx plan task done --agent-id $WDX_AGENT_ID --summary "<what you did, in a few lines>"
```

### A task's end

- `wdx plan task done --summary <text>` from an agent finds its task by `WDX_AGENT_ID`. Refused if the agent is no task's (`not_found`), if the task is not `running` (`task_state`, with its state), or if the slot has uncommitted changes (`not_committed`), so that the agent commits; nothing committed is fine, a task that needed no change. It writes the summary and `landing`, and returns; the runner then stops the agent, keeping its session to resume. The summary is the agent's output, so it is never logged.
- Landing is `worktree.LandOnto` in the slot with the plan's branch as the base: rebase onto it if it moved on, run the check if the plan has one, then `git update-ref refs/heads/plan/<name> <commit> <old>`, as no checkout has the plan's branch. Only the plan's landings move it, one at a time, so it cannot move during the check unless a person moved it, which fails the landing as `wdx worktree land` does.
- Landed: `git switch --detach` and `git branch -d task/<plan>/<task>` in the slot, as its commits are on the plan's branch; the task is `done`, its `slot` cleared.
- A rebase that conflicts is aborted, leaving the slot as it was, and the task is `land_failed` with `rebase` and its files. A check that fails is `land_failed` with `check`, its terminal kept to read. The person then chooses, in tide or the CLI:
  - an agent: the task's own, resumed with a message saying what to do (`Rebase task/<plan>/<task> onto plan/<name> and resolve the conflicts in …`, or the check's command); or
  - themselves: the slot is a worktree like any, with its diff, terminal and editor.

  Then `wdx plan task land`, which refuses while the task's agent is working (`agent_working`), as landing a worktree does.
- An agent that ends while its task is `running` makes it `failed` with `agent_ended`.
- `task retry` depends on why the task failed:

  | `failure` | Comes from | Retry |
  |---|---|---|
  | `agent_ended` | the AI CLI ended before `task done`: it crashed, was killed for memory, or someone quit it or stopped its agent | resumes the agent in its slot, told it ended before `task done`; its session holds what it had done |
  | `agent_ended`, agent removed | someone removed the agent | a new agent in the same slot, told a previous attempt left its commits there |
  | `start_failed` | git failed in the slot, the ACA was not found, or the agent did not start | starts again from the step that failed; there is no session |
  | `slot_missing` | the slot's directory was deleted | a new agent in a new slot, as Claude Code keeps sessions by directory |

  `task retry --fresh` starts a new agent in the same slot even when one could be resumed, for an agent gone wrong or with its context full, told a previous attempt left its commits there. Its commits stay: wdx never resets them, a person can.
- A conflict or a failed check is not retried but landed again, once dealt with. An agent waiting for input or held up by a rate limit is still running, and the plan shows `needs you`.
- An agent that waits for input needs nothing: its activity (docs/AGENT_STATE.md) shows on the plan.

### Finishing

When every task is `done` or `skipped` and nothing is landing:

1. A free slot is switched to `plan/<name>` and its `plan_slots` row deleted: it is now an ordinary worktree, the plan's, which the person lands as any worktree (docs/TIDE_WORKTREES.md), by PR or rebase, and removes once landed. Its base is the default branch, which is why a plan always starts from it.
2. Every other free slot is removed with `git worktree remove`, which refuses a slot with changes; such a slot is kept and logged.
3. A skipped task's slot is kept as an ordinary worktree on its branch, for the person to land or remove: wdx never deletes work.
4. The run is `done`.

Slots are never kept for another plan: one may not come for a while.

### A daemon restarted

Agents outlive the daemon, as terminals do (docs/TERMINALS.md); a landing does not, as the lander forgets one. On start, the runner wakes every run not `done`, and `Next` sees:

- a task `landing`: landed again, which finds nothing to do if the plan's branch already holds its commit, a crash after the move;
- a task `running` whose agent is not running: `failed` with `agent_ended`, as the supervisor's reconcile has marked it;
- a task `pending` holding a slot: started again from step 2;
- a slot whose directory is gone: its row deleted, and a task holding it `failed` with `slot_missing`.

### Around the plan

- **The plan's branch** is `plan/<name>`, from the default branch's tip, refused with `branch_exists` if there; tasks' branches `task/<plan>/<task>`. A plan starts from the default branch, or, stacked as a parent's child, from the branch of the plan before it (Agreed); its run's `base` says which.
- **Closing the project** stops its agents, as now. The plan is paused first, so that agents stopped by wdx do not fail their tasks: their tasks stay `running` and are resumed when the project opens and the plan is resumed.
- **Removing a task's agent** by hand: its task is `failed` with `agent_ended`, and `retry` starts a new agent, as there is none to resume.
- **The worktree tracker** learns which paths are slots, so that the worktrees in a project's stream carry their plan and task, and a slot's commits are measured against the plan's branch rather than the default branch.
- **Settings**: `plan.parallel`, 3 by default, the most tasks each plan runs at once unless its run says otherwise (`plan run --parallel`, `plan set --parallel`), read by the runner and taking effect at once on `wdx setting set`. Not in the plan's YAML: the right number depends on the machine's memory, and with the hub a teammate's machine is not this one. No limit across plans for now, as agents a person starts have none; a machine-wide one can come if two plans at once prove too heavy.

### The API

| Request | Does | Returns |
|---|---|---|
| `POST /v1/plans` | creates one, checked | 201 `created`, `plan` |
| `GET /v1/plans?project=` | a project's plans | `plans` |
| `GET /v1/plans/{plan}?project=&revision=` | a plan, its content, its run and tasks | `plan`, `revision`, `content`, `run`, `tasks` |
| `PUT /v1/plans/{plan}/content` | a new revision, with `base` | `updated` or `unchanged` |
| `GET /v1/plans/{plan}/revisions` | its revisions | `revisions` |
| `DELETE /v1/plans/{plan}?project=` | removes it | `removed` |
| `POST /v1/plans/{plan}/copies` | a new draft of its latest revision, with `name` | 201 `created`, `plan` |
| `POST /v1/plans/{plan}/run` | starts it, with `parallel` | `started`, `plan` |
| `PATCH /v1/plans/{plan}` | `name` of a draft, `parallel` of its run, or `archived` | `updated` or `unchanged` |
| `POST /v1/plans/{plan}/pause`, `/resume`, `/cancel` | | `paused`, `resumed`, `cancelled`, or `unchanged` |
| `POST /v1/plans/{plan}/tasks/{task}/done` | with `summary`; the CLI fills plan and task from the agent | `landing`, `task` |
| `POST /v1/plans/{plan}/tasks/{task}/land`, `/retry` (with `fresh`), `/skip` | the person's answers | `landing`, `retried`, `skipped`, `task` |

- A plan in a response: `id`, `project_id`, `project_name`, `name`, `plans`, `parent`, `revision`, `size`, `created_at`, `updated_at`, and its run's `state` and `tasks_done`/`tasks` (`null` before it runs). A task: `id`, `after`, `state`, `failure`, `conflicts`, `agent_id`, `slot_path`, `summary`, `started_at`, `finished_at`; the prompt only from `GET` of the plan, in its content.
- New codes: `invalid_plan` (422), `plan_changed` (409), `plan_in_use` (409, step 1), `plan_started` (409, editing, renaming or removing a plan that is not a draft, or running it again), `plan_not_running` (409, pause, resume, cancel and a task's answers on a plan not running or paused, archiving one not done or cancelled), `task_state` (409, a task not in a state its request needs). Reused: `not_found`, `name_taken`, `branch_exists`, `not_committed`, `agent_working`.
- The project's stream carries its plans, with their runs and tasks, so that tide follows them live.

### The CLI

| Command | Does |
|---|---|
| `plan create --name <name> --path <file>\|-` | a plan, checked |
| `plan get (--id \| --name) [--revision <n>] [--out <file> [--overwrite]]` | its YAML, as `doc get` |
| `plan update (--id \| --name) --path <file>\|- --base <n>` | a new revision, checked; a draft only |
| `plan list [--all]` | `NAME`, `STATE`, `TASKS` (`3/7`), `UPDATED`; `--all` adds the archived |
| `plan history (--id \| --name)` | its revisions |
| `plan rename (--id \| --name) --new-name <name>` | a draft only |
| `plan remove (--id \| --name) [--yes]` | a draft only |
| `plan copy (--id \| --name) --new-name <name>` | a new draft of its latest revision |
| `plan status (--id \| --name)` | its tasks in DAG order: `TASK`, `STATE`, `AFTER`, `AGENT`, `SLOT` |
| `plan run (--id \| --name) [--parallel <n>]` | starts it |
| `plan set (--id \| --name) --parallel <n>` | changes a run's parallel |
| `plan pause`, `resume`, `cancel (--id \| --name)` | |
| `plan archive`, `unarchive (--id \| --name)` | a `done` or `cancelled` plan |
| `plan task done [--summary <text>]` | from a task's agent; a person names `--plan` and `--task` |
| `plan task land`, `retry [--fresh]`, `skip (--plan) --task <id>` | the person's answers |
| `plan task get --plan <name> --task <id>` | a task: its prompt, state, summary, conflicts |

`wdx help agents` gains how to finish a task, and the launch note's list of what wdx replaces gains plans.

### tide

- A Plans section in the left pane: a row per plan with its state and `3/7`, and `needs you` when a task is `land_failed` or `failed`, or its agent waits for input.
- `Enter` opens the plan's tab: a row per task in DAG order, its state, what it waits for, and its agent. `Enter` on a done task shows its summary, on a running one goes to its agent, on a pending one shows its prompt; on a task in `land_failed` or `failed`, a box with the choices above. Keys to run, pause and resume it, and `e` to edit it in the editor, as a doc.
- In the Agents section, a plan's agents are under a row for the plan that `Enter` folds, as child agents under their parent. Folded, it still shows `needs you`.
- In the Worktrees section, the same: a row for the plan with its branch, and its slots under it, each with the task and agent in it, or `free`, dimmed. A slot cannot be landed or removed while the plan runs; `Enter` opens its tab as any worktree's, with its diff against the plan's branch.

```
auth-refactor              plan/auth-refactor
  auth-refactor-1          D · auth-refactor-d running
  auth-refactor-2          free
```

docs/CLI_TIDE.md gets the plan rows.

### Logging and tests

- Logged at info, with `plan_id`, `task_id` and `slot`: every change of a run's or task's state and why, every slot taken, made, switched, freed and removed with the git command, every landing's steps. Never a prompt, the first message or a summary.
- `Next` and the definition's check are table tests. The runner is tested in a daemon with the test ACA (aca/acatest) and real git: an agent that commits and runs `wdx plan task done`, one that ends without it, and one that conflicts, each checked by the states it reaches and in what order.

### Steps

Each is designed again in detail before it is built.

1. Plans as docs: the definition and its check, `create`, `get`, `update`, `list`, `history`, `remove`, `status` with no run; tide's Plans section and a read-only tab.
2. Running: the run's tables, `Next`, the runner, slots, starting agents, `task done`, landing on the plan's branch with no check, finishing, restarts.
3. The person's part: `land_failed` and `failed`, the check, `land`, `retry`, `skip`, `pause`, `resume`, `cancel`, `set --parallel`, in the CLI and the tab.
4. The rest of the lifecycle: `copy`, `archive`, `unarchive`.
5. tide's grouping of a plan's agents and slots.
6. Running plans of plans: a parent starting its children, waiting for the person at each PR, stacking, and rebasing a stacked branch onto the default one (`git rebase --onto`, from the old base's tip the run keeps) once the PR under it is squash-merged.

## Step 1 in detail (proposed)

Plans that can be written, read and checked, with nothing running: what step 2 runs. Every plan is a `draft`.

### Storage

- Migration `0020_plans.sql` adds two columns to `docs`, and so only adds: a rolled-back binary keeps working, and would list plans among its docs.
  - `kind TEXT NOT NULL DEFAULT 'doc' CHECK (kind IN ('doc', 'plan'))`.
  - `parent_id TEXT REFERENCES docs (id) ON DELETE SET NULL`, indexed: a child plan's parent, NULL for a doc, a parent and a plan no parent lists. Written when a parent is saved, for every child it lists, and cleared for those it stops listing, so that `parent` and `plan_in_use` are one lookup, not a parse of every parent. SET NULL rather than CASCADE: removing a parent frees its children, it does not remove them.
- A plan's `name` column holds `plan:<name>`. The table's `UNIQUE (project_id, name)` then keeps plans and docs apart with nothing rebuilt: a doc's name cannot hold `:`, so no doc can have a plan's. Making names unique per kind otherwise would rebuild `docs`, and a migration runs with foreign keys on, so dropping the old table would delete every revision through its cascade unless `doc_revisions` were rebuilt with it. Docs keep their names as they are: a `doc:` prefix would rename every doc row and every doc synced to git, for nothing.
- `internal/doc` takes the kind: a `Store` keeps one kind, `doc.NewStore` docs as now and `doc.NewKindStore(db, dir, doc.KindPlan)` plans. Every query it makes has `d.kind = ?`, so that no `doc` request reaches a plan or the reverse, and no method can forget it. `sync-to-git` writes docs only, refusing on a plan store.
- The prefix is the store's storage detail: a plan store adds it on the way in and strips it on the way out, so `internal/plan`, the API, CLI, tide and logs never see it. A plan's name has letters, numbers and `-` (Agreed), at most 95, so that the stored one fits docs' 100. tide's doc tabs check the doc they name is a `doc`.
- A plan's row: `format` `yaml`, `labels` `[]`, `status` `wip`, none of which a plan shows. Plans have no labels or statuses.
- Hidden copies: `<home>/plans/<project>/<name>.yaml`, written, caught up and removed as docs' are, by the same code given the kind's directory.
- Content: what docs allow (UTF-8, no NUL, at most 2 MiB), and the plan's check on every create and update, so that every stored revision is a valid plan.

### The definition: `internal/plan`

A plan of tasks:

```yaml
goal: Keep sessions in the database instead of cookies.
constraints:
  - The public API does not change.
  - Every new table gets a migration test.
tasks:
  - id: schema
    prompt: |
      Add a sessions table with id, user_id and expires_at.
  - id: store
    after: [schema]
    prompt: |
      Write the session store on top of the table.
  - id: api
    after: [schema]
    prompt: |
      Change the login and logout handlers to use sessions.
  - id: docs
    after: [store, api]
    prompt: |
      Document the session lifetime in docs/AUTH.md.
```

A plan of plans, each entry an existing plan's name:

```yaml
goal: Rewrite authentication.
plans:
  - id: auth-sessions
  - id: auth-tokens
    after: [auth-sessions]
  - id: auth-cleanup
    after: [auth-tokens]
```

- `definition.go`: `Parse(content []byte) (Definition, []Problem)`. `Definition` holds `Goal`, `Constraints`, and either `Tasks` (`ID`, `After`, `Prompt`) or `Plans` (`ID`, `After`), in DAG order. A `Problem` is a line and a message.
- It decodes through yaml.v3's node tree, already a dependency, so that every problem has its line, and reports all it finds rather than the first:
  - not YAML, or not a mapping at the top;
  - a key other than `goal`, `constraints`, `tasks`, `plans`, in a task other than `id`, `after`, `prompt`, or in a child plan other than `id`, `after`;
  - `goal` missing or blank; `constraints` not a list of strings;
  - both `tasks` and `plans`, or neither, or the one given empty; a task with no `id` or a blank `prompt`;
  - a task `id` that is not `A-Z a-z 0-9 -`, at most 50, as it goes into agents' names, which cannot hold `_`, and branches'; one used twice, whatever its case;
  - an `after` naming no entry, the entry itself, or one twice;
  - a cycle, naming its entries in order (`schema → api → schema`).
- The store checks what the YAML alone cannot, as problems with their lines too: a child plan that does not exist in the project, that is itself a plan of plans (one level only), that another parent already lists (its `parent_id`), or that is the plan itself. It writes the children's `parent_id` in the same transaction as the revision.
- **Reduction**: after the check, an `after` entry that another path already reaches is dropped (the transitive reduction). When anything is dropped, wdx stores the YAML with those entries removed, through the node tree, which keeps comments and block text but may change indentation; with nothing dropped it stores the content byte for byte. `Reduce(content) (Reduced, []Problem)` does the check and this, returning what to store, its definition and what went. The response lists it in `dropped`, `[{entry, after, via}]`, `via` being the other `after` that already waits for it, so that the AI or person who wrote it knows the stored plan differs from what they sent. Every stored revision is then a valid, reduced plan.
- DAG order: Kahn's algorithm, always taking the ready entry that comes first in the file, so that a plan written in order reads as written, and the order is the same every time.
- `wdx help plans`, a help topic, is the format's reference: every key, the rules above, and an example plan, which a test parses so that it cannot go stale.

### The API

| Request | Does | Returns |
|---|---|---|
| `POST /v1/plans` | creates one, checked | 201 `created`, `plan` |
| `GET /v1/plans?project=` | a project's plans, by name | `plans` |
| `GET /v1/plans/{plan}?project=&revision=` | a plan with a revision, its content and what it defines | `plan`, `revision`, `content`, `definition` |
| `PUT /v1/plans/{plan}/content` | a new revision, checked, with `base` | `updated` or `unchanged` |
| `PATCH /v1/plans/{plan}` | `name` | `updated` or `unchanged` |
| `GET /v1/plans/{plan}/revisions` | its revisions: number, size, time | `revisions` |
| `DELETE /v1/plans/{plan}?project=` | removes it, with its revisions and copy | `removed` |

- A plan: `id`, `project_id`, `project_name`, `name`, `state` (`draft` until step 2), `tasks` (how many, or null for a parent), `plans` (its children's names, or null), `parent` (the parent's name, from `parent_id`, or null), `revision`, `size`, `created_at`, `updated_at`.
- `definition`: `goal`, `constraints`, and `tasks` in DAG order, each `id`, `after`, `prompt`, or `plans` in DAG order, each `id`, `after`; from step 2 each with its run's state. tide and `plan status` read it, so that no client parses the YAML.
- `create` and `update` return `dropped`, the `after` entries reduction took out (`entry`, `after`, `via`), empty when none.
- New codes: `invalid_plan` (422), its `details.problems` a list of `{line, message}`; `plan_changed` (409), as `doc_changed` is for docs, its details holding the plan and `base`; `plan_in_use` (409), removing or renaming a plan a parent lists, its details naming the parent. wdx never edits a parent's content for you. Reused: `not_found`, `name_taken`, `invalid_name`, `invalid_argument`.
- `Project.plans` in `getProject` and the project's stream, without content or definition, as `Project.docs`.
- Logs name the plan, project, revision and size of every change, never the content: the daemon measures `content` in requests and responses, and logs `getPlan`'s definition with its ids and the sizes of its goal and prompts, as they are what agents are told.

### The CLI

Each command takes `--project-id <id> | --project-name <name>`, or the project of the working directory, and a plan is named by `--id` or `--name`, as `doc` does.

| Command | Text output |
|---|---|
| `plan create --name <name> --path <file>\|-` | `plan sso-login created, with 4 tasks` |
| `plan get (--id \| --name) [--revision <n>] [--out <file> [--overwrite]]` | the YAML, byte for byte; with `--out`, `revision 2 of sso-login written to <file>` |
| `plan update (--id \| --name) --path <file>\|- --base <n>` | `sso-login is at revision 3, with 5 tasks`; `sso-login is unchanged at revision 3` |
| `plan list` | a table: `NAME`, `STATE`, `TASKS`, `REVISION`, `UPDATED` |
| `plan history (--id \| --name)` | a table: `REVISION`, `SIZE`, `CREATED` |
| `plan rename (--id \| --name) --new-name <name>` | `plan sso-login renamed to sso` |
| `plan remove (--id \| --name) [--yes]` | `plan sso-login removed` |
| `plan status (--id \| --name)` | `sso-login: draft, 4 tasks` and its goal, then a table in DAG order: `TASK`, `AFTER`, `STATE` (empty until step 2); for a parent `auth: draft, 3 plans` and `PLAN`, `AFTER`, `STATE` |

- `create` needs `--path`, a file or `-` for stdin: an empty plan is not a valid one, and a template would be a plan that runs. An AI sends the YAML on stdin (`wdx plan create --name auth --path - <<'EOF'`), with no file to write; a `--content` flag would put multi-line YAML in shell quoting. `wdx help plans` is what it writes from.
- Text output adds a line per dropped `after`: `dropped docs after schema: api already waits for it`.
- `invalid_plan` in text says `the plan in <file> has N problems`, then lists each as `<file>:<line>: <message>`, the file being `--path` or `stdin`, so that editors and AIs can jump to it; `--json` keeps the API's problem, with `details.problems`.
- The CLI measures a plan's YAML, and the goal, constraints and prompts of `plan status`, in what it logs, as the daemon does (`api.PlanResponseLogged`, shared by both).
- `plan_changed`, `get --out`, `--overwrite`, `remove`'s question and `--yes`, and the completions follow `doc`'s, word for word with `plan`.
- No `set`: a plan has no status or labels. `copy`, `run` and the rest come with their steps.

### tide

- The Plans section, `p` in `tide.section_order`, already taken and placed by the default `awdpc`. `p` alone stops being refused, as the section is no longer empty.
- A row per plan, from the project's stream: name, state (`draft` muted) and `4 tasks` (`3 plans` for a parent), the state dropped first on a narrow row. A parent's children are listed under it, indented, in its DAG order; `←` folds a parent's plans and `→` unfolds them, as for an agent's children and a worktree's files, and a click on its fold mark does either. The heading counts them; with none, `no plans: see wdx help plans`. The `/` filter matches a name, and keeps a matching child's parent.
- A click, or `Enter`, opens a `plan: sso-login` tab (`plan:<id>`, so a rename keeps it), a parent's too. (The design had `Enter` fold a parent and `o` open its tab, but `o` is the menu's open project, which acts without `C-q` in the left pane; changed while building sub-step 5, 2026-11-03.) The tab is read with `GetPlan` when opened and again when the stream brings a new revision. It shows the goal and the constraints, then a row per entry in DAG order: its id and `after`. `Enter` on a task shows its prompt under its row, and again hides it; on a child plan it opens that plan's tab.
- Nothing in tide changes a plan, as with docs: the CLI makes every change.
- A plan's tab is kept in the daemon with tide's other tabs (docs/TIDE_STATE.md), as a doc's is: a tab of kind `plan`, its plan's id in `doc_id`, so that it comes back after a restart and removing the plan closes it through the cascade. `tide_tabs`' CHECK lists the kinds, so migration `0021_plan_tabs.sql` builds the table again with `plan` added, the trigger closing a tombstoned agent's tabs dropped and made again around it, and the triggers counting the tabs' changes (0019) made again after it, as they go with the table. It keeps the columns as they are, and an older tide passes over a kind it does not know, so it is not marked `breaks-older`: a rollback keeps the tabs, less the plans'. The API's `TabKind` gains `plan`. (Found while building sub-step 5, approved 2026-11-03.)

### docs/CLI_TIDE.md, help and README

- A Plans table: reading a plan and its tasks, Same; create, update, rename, remove, history, By design, CLI only, as for docs.
- `wdx help agents` gains plans: write one as `wdx help plans` says, create it with `wdx plan create`, and leave running it to the person until asked. Its command lines are checked, as the rest.
- The README's feature list gains plans.

### Steps of step 1

Each builds and passes `make test` on its own.

1. The migration and `internal/doc` taking the kind, with the docs' tests unchanged and new ones for the kinds kept apart.
2. `internal/plan`'s definition: the check and DAG order, table tests.
3. The API and daemon: spec, `make generate`, handlers, the codes, `Project.plans`, the copies.
4. The CLI and `wdx help plans`.
5. tide's Plans section and tab.
6. docs/CLI_TIDE.md, `wdx help agents`, the README.

## Step 2 in detail (approved 2026-11-03)

Running a plan from `plan run` to `done` when nothing goes wrong: the run's tables, `Next`, the runner, slots, task agents, `task done`, landing on the plan's branch with no check, finishing, and a daemon restarted in the middle. What the person does when something goes wrong is step 3.

### What step 2 leaves to step 3

A task can still reach `failed` (its agent ended, or did not start) or `land_failed` (`rebase`), and a run can be closed with its project; step 2 records and shows them, but `retry`, `land`, `skip` and `cancel` come with step 3. Until then such a plan stays `running` with that task stuck, and what waits for it never starts; a person can still work in the slot by hand. So step 3 follows before a release that has plans running.

### Storage

`0022_plan_runs.sql` adds the three tables of The tables above, whole: every state step 3 and 4 use is in the CHECKs now, with `parallel` and `archived_at`, as a CHECK cannot change without building the table again. Only added tables, so not `breaks-older`; a rollback leaves runs nobody reads, and a plan that has run editable again by the older binary.

- `plan_slots` keeps the slot's path, which is also a row of `project_worktrees`: a slot is a worktree wdx made, recorded as `WorktreeNamed`'s are, so that agents in it belong to the project and the worktree tracker lists it (step 5 groups it under its plan).
- A slot's path is `<root>/.worktrees/<plan>-<n>`, `n` the lowest number neither another slot of the plan has nor a path on disk: the person's worktree of that name is passed over, not refused (the Open question, answered).

### `internal/plan`

- `run.go`: `Run`, `Task` and `Slot` as the tables hold them, and the store's writes: `Start` writes the run and a `pending` row per task from the plan's revision, in one transaction, refusing a plan that has a run (`plan_started`); every change of a task's state is `UPDATE … WHERE state = <from>`, and one that matched nothing is read again.
- `next.go`: `Next(Facts) []Action`, pure. `Facts` is the run read whole, its definition, the `parallel` in force, whether the project is open, and per task whether its agent is live or stopped with its project, and whether its slot is on disk: the runner gathers them, so that `Next` is table tests with no process. The actions, in this order:
  1. `Fail` a `running` task whose agent ended by itself, failed or was lost (`agent_ended`), and one whose slot is gone (`slot_missing`). An agent `stopped` with its project is waited for: `StartProject` starts it again when the project opens, and its task goes on.
  2. `Land` the first `landing` task.
  3. If the project is open and the run `running`: `Start` each `pending` task in DAG order whose `after` are all `done` or `skipped`, while fewer than `parallel` tasks are at work (`running`, `landing`, or `pending` holding a slot), in the lowest free slot or a new one; a `pending` task already holding a slot, a start a crash cut short, first. A `failed` or `land_failed` task keeps its slot, for the person, but is not at work, so the tasks that can go on do. (Counting tasks at work rather than slots found while building sub-step 1.)
  4. `Finish` when every task is `done` or `skipped`.
- `runner.go`: one goroutine, a set of plan ids woken as The runner says, each read, given to `Next`, its first action carried out and its outcome written, until `Next` says nothing. In step 2 a landing runs in the runner, as with no check it is one rebase and a ref moved; step 3 moves it to a goroutine of its own with the check.
- `RunnerOptions`, which the daemon fills, so that `plan` never imports `agent`, `terminal` or `project`:
  - `Project(ctx, id)`: its root, clone path, default branch and whether it is open;
  - `AddWorktree(ctx, projectID, path, branch, from)` and `RemoveWorktree(ctx, projectID, path)`: `git worktree add` and `remove`, with the `project_worktrees` row;
  - `CreateAgent(ctx, projectID, name, dir, first, linked)`: the agent, its first message, and `linked(tx, agentID)` writing the task `running`, in one transaction;
  - `StopAgent(ctx, agentID)`: ends it and waits until it is gone;
  - `AgentStates(ctx, ids)`: live, stopped with its project, or ended;
  - `Changed()`: the projects' stream.
- Landing is `worktree.LandOnto(ctx, path, base, branch)`, the rebase and the move taken out of `Lander.run`, which keeps the check and the push between them: rebase `branch` onto `base` if `base` moved on, then `git.MoveBranch(base, commit)`, which already moves the ref only from the value it read. It returns the commit, or a `*git.RebaseError` with the files in conflict after the rebase is aborted.

### Starting a task, as built

As Starting a task above, with these changes found against the code:

- `agent.Manager.Create` already starts the agent, so there is no `StartAgent`. It gains its first message and a `linked` function, both written in the transaction that adds the agent's row, as `BeginSession` writes a restart's: the message waits in `messages` for the holder, as any sent before the agent is ready (docs/SEND.md). So a crash never leaves a task's agent without its task, nor a task `running` with no agent. `createAgent`'s `--prompt` keeps sending after the create, as now.
- Its name is `<plan>-<task>`; one taken by another agent fails the start (`start_failed`), as wdx does not rename what a person named.
- The new agent runs the project's default ACA.

### A task's end, as built

- An agent runs `wdx plan task done --agent-id $WDX_AGENT_ID --summary <text>`, its shell filling the id, as `agent create --parent-id $WDX_AGENT_ID` does: the CLI reads no environment, as only `main` may, and only the home (AGENTS.md). It asks the daemon for the agent, whose view gains `task`, `{plan, task}` or null, and sends `POST /v1/plans/{plan}/tasks/{task}/done`. A person names `--plan` and `--task` instead, for an agent that finished but did not say so. The first message spells the whole command. `--summary` is required and cannot be blank: the tasks after it are told it.
- Refused when the task is not `running` (`task_state`, with its state), when the slot has changes not committed or files not tracked (`not_committed`), and when the slot is not on `task/<plan>/<task>` (`wrong_branch`, new, 409), as commits on another branch would never land.
- It writes the summary and `landing`, and wakes the runner, which then stops the agent and lands the task: stopped first, so that nothing writes in the slot during the rebase.
- Stopping one agent is new: `Supervisor.StopAgent` hangs up on it without marking it `stopped`, so that its row ends `exited`, as one that ended by itself. A `stopped` agent is started again when its project opens; a finished task's must not be, so one already stopped with its project is made `exited` too (found while building sub-step 2). Its session is kept, for step 3 to resume.
- Landed: the slot detached at the commit, `task/<plan>/<task>` deleted with `git branch -d`, the task `done` and its `slot` cleared. A conflict: the rebase aborted, the task `land_failed` with `rebase` and its files, waiting for step 3.

### The run's start and end

- `plan run` checks, in the request: the plan has no run (`plan_started`), the project is open (`project_closed`), no parent lists it (`plan_in_use`, until step 6 says whether a child runs alone), it is not a parent itself (`invalid_argument`, until step 6 runs parents), and `plan/<name>` can be made (`branch_exists`, also when a branch such as `plan` is in its way). It then makes `plan/<name>` at the default branch's tip, writes the run, and wakes the runner. Starting the agents is the runner's, so the response says `started` before any agent is up.
- Once it has run, `update`, `rename` and `remove` are refused with `plan_started`, and so is a parent listing it as a child, until step 6.
- Finishing is as Finishing above, with no skipped tasks yet: the free slot kept becomes an ordinary worktree on `plan/<name>`, the others removed, the run `done` with `finished_at`.

### The API

| Request | Does | Returns |
|---|---|---|
| `POST /v1/plans/{plan}/run?project=` | starts it, with `parallel` (1 to 20) or the setting's | `started`, `plan` |
| `POST /v1/plans/{plan}/tasks/{task}/done?project=` | with `summary` | `landing`, `task` |

- `Plan.state` becomes `draft`, `running`, `paused`, `done` or `cancelled`, every state of a run now, so that step 3 adds none to the enum, and `Plan` gains `tasks_done`, null for a draft.
- `getPlan` gains `run`, null for a draft: `state`, `revision`, `base`, `branch`, `parallel` (null for the setting's), `started_at`, `finished_at`. Each task of `definition` gains `state`, `waiting_for` (its `after` not yet `done` or `skipped`, so that no client works it out), `failure`, `conflicts`, `agent` (`id` and `name`, or null), `slot_path`, `summary`, `started_at`, `finished_at`; all null for a draft but `state`, `pending`, and `waiting_for`.
- `Agent.task`: `{plan, task}`, the plan's name and the task's id, or null.
- Every change to a run moves its plan's `updated_at` on, as the field says it is when anything about the plan last changed: a project's stream sends only what changed, and a task going from `running` to `landing`, or failing, changes nothing else it carries. So tide reads the plan's tab again on every `updated_at`, not only on a new revision. (Found while building sub-step 3.)
- New codes: `plan_started` (409), `task_state` (409, its details the task), `wrong_branch` (409). Reused: `not_found`, `project_closed`, `plan_in_use`, `branch_exists`, `not_committed`, `invalid_argument`.
- Summaries are agents' output: the daemon and the CLI measure them in what they log, in `task done`'s request and in every plan and task they log, as they do the content.

### Settings

`plan.parallel`, 3 by default, 1 to 20: the most tasks each plan runs at once unless its run says otherwise. The runner reads it at each action, so `wdx setting set` takes effect at once through `daemon.Settings`.

### The CLI

| Command | Text output |
|---|---|
| `plan run (--id \| --name) [--parallel <n>]` | `plan sso-login running on plan/sso-login, up to 3 tasks at once` |
| `plan task done (--agent-id <id> \| --plan <name> --task <id>) --summary <text>` | `task schema of sso-login done: landing on plan/sso-login` |
| `plan task get --plan <name> --task <id>` | its state, agent, slot, `after`, then its prompt and its summary |
| `plan status` | `sso-login: running, 2/4 tasks done, on plan/sso-login`, and the table gains `AGENT` and `SLOT`; `STATE` says `waiting for api, cli`, `ready`, `running`, `landing`, `done`, `failed: agent ended`, `land failed: rebase` |
| `plan list` | `STATE` the run's, `TASKS` as `2/4` once it runs |

- `plan task done` takes `--agent-id`, or `--plan` and `--task`, not both: a `usage` error otherwise. An agent that is no task's is `not_found`.
- No prompt: `plan run` is asked for by name.
- `wdx help plans` gains running and finishing a task; `wdx help agents` gains `plan task done`, for an agent told it is doing a task; the launch note's list of what to do through wdx gains plans.

### tide

- The Plans section: a running plan's state and `2/4`.
- The plan's tab: each task's row gains its state, as `plan status` says it, and its agent's name. `Enter` on a running or landing task opens its agent's tab; on any other, shows its prompt under the row, and its summary once done, and hides them again.
- `r` in a draft's tab runs it, after `run sso-login? up to 3 agents start  y/n`, as removing a worktree asks; refusals show as other actions' do. The rule of step 1, nothing in tide changes a plan, keeps holding for its YAML.
- docs/CLI_TIDE.md: rows for running a plan and finishing a task (the CLI only: an agent's or, in step 3, the person's answers).

### Logging and tests

- As Logging and tests above. The first message is never logged: `Create` logs it as `Send` does, by its size.
- `Next` is table tests over `Facts`: each action, its order, the slots chosen, `parallel`, a closed project, a stopped agent waited for, an ended one failed, a slot gone.
- The runner in a daemon with the test ACA, whose script, committed in the test repository, reads its first message, commits and runs `wdx plan task done`: a plan of A, then B and C at once, then D, run to `done`, checked by the order of the states each task reached and the slots used (two), with `plan/<name>` holding every commit and one worktree left on it; a task whose agent exits without `task done`, `failed`; two tasks changing the same line, the second `land_failed` with its file; the daemon stopped while tasks run and started again, the run going on.
- The CLI: an integration test per outcome of `run` and `task done`, by `--agent-id` and by `--plan` and `--task`.

### Steps of step 2

Each builds and passes `make test` on its own.

1. The migration, `internal/plan`'s run store and `Next`, table tests.
2. `worktree.LandOnto` taken out of the lander, `Supervisor.StopAgent`, `Manager.Create`'s first message and `linked`.
3. The runner and its wiring in the daemon, `plan run`, `task done`, `plan_started` on edits, the setting, the API.
4. The CLI: `run`, `task done`, `task get`, `status` and `list`, the help and launch note.
5. tide: states in the section and tab, `Enter` to an agent, `r`.
6. docs/CLI_TIDE.md and the README.

## Step 3 in detail (approved 2026-11-04)

What the person does when a task cannot go on, and with a run as a whole: the landing check, `task retry`, `land`, `skip` and a person's `task done` on a failed task, `pause`, `resume`, `cancel` and `set --parallel`, in the API, the CLI and the plan's tab. No migration: 0022's CHECKs already hold every state and column.

### What changes from the architecture above

Found against step 2's code, each simpler than what it replaces:

- **A retry goes through the runner's start**, not a path of its own: `task retry` moves the task back to `pending`, keeping its slot, and `Next` starts it as a start a crash cut short, first. So a retry waits for room under `parallel`, and a paused run starts it only on `resume`, as any start.
- **A `land_failed` task is retried too**: `retry` resumes its agent with what went wrong (the conflicts, or the check), and its own `task done` lands it again, as any task's. `task land` is for a person who dealt with it themselves. The architecture had the agent resumed and the person landing after it, which is two steps for one answer.
- **Closing the project pauses the plan, and opening it resumes it**, as the human expects (2026-11-04), shown rather than stored: while the project is closed, every view shows the run `paused` with `paused_reason: project_closed`, whatever its stored state. Underneath it is step 2's: nothing starts while the project is closed, the agents it stopped are waited for, and `StartProject` starts them again in their sessions when it opens. So opening gives back the state the person left: a plan they paused stays paused, one running runs again, with nothing written on close or open and no column to say who paused it.
- **A plan's branch moved during the check lands again** (rebase, check, move), rather than failing with a new failure: only a person moves it, and once.
- **A skipped task's slot leaves the plan at once**, rather than at the end: its work, if it has any, becomes an ordinary worktree on its branch, and a slot with none is freed for the next task.

### The landing check

- Landing runs the project's landing check (`wdx project landing --check`), read at each landing, between the rebase and the move: none set, none run. It runs in the terminal `land-<plan>-<n>`, as `runLandCheck` does for a worktree, and a failed one keeps its terminal: the task is `land_failed` with `check`, and its view names the terminal (`check_terminal`).
- A landing runs in a goroutine of its own, one per plan at a time, as a check can take many minutes; `Facts` gains `Landing`, and `Next` lands nothing while one is going but goes on starting tasks. The rebase and the move hold the daemon's `changes` lock; the check does not.
- A landing needs the project open, for the check's terminal. One cut short (the project closed, the daemon stopped, the run cancelled) leaves the task `landing`, landed again when it can be. A check terminal of the plan's still running from a landing cut short is stopped and removed first: slots are the plan's, and so are their checks.

### `Next`, as step 3 changes it

In this order, for a run with no `finished_at`:

1. `Fail`, as step 2.
2. A `cancelled` run: `Stop` each live agent of its tasks, then `Release` each slot, then `Close` (writes `finished_at`). Tasks keep the states they had. Nothing else.
3. `Release` the slot of a `skipped` task holding one: its agent stopped if live; with work (commits on its branch the plan's branch does not hold, or changes in the slot) the slot's row goes and the worktree stays, an ordinary one on `task/<plan>/<task>`; with none, detached and its branch deleted, free again.
4. `Land` the first `landing` task, if the project is open and no landing is going.
5. `Start`, as step 2, while `running`.
6. `Finish` when every task is `done` or `skipped` and no landing is going, `running` or `paused` alike: a paused run has nothing left to hold back.

### Starting a task that started before

A task with a `started_at` was started before: its branch holds its earlier work, which wdx never resets.

- Its branch is used as it is: `git switch task/<plan>/<task>` in its slot, or `git worktree add <path> task/<plan>/<task>` in a new one; never `switch -C`, and the ancestor check of step 2 is skipped.
- Its `agent_id` still set: the agent is resumed (`Manager.Start`, same session), then the message below and `running` written in one transaction. One already live, after a crash, only gets them.
- No `agent_id` (removed, `--fresh`, or `slot_missing`): a new agent, with the first message and a line that a previous attempt left its commits on this branch. Its name is `<plan>-<task>-<n>`, the lowest `n` from 2 no agent has, as the old one keeps its name; the old one is left `exited`, belonging to no task, for the person to read or remove.

What the agent is told, by the task's `failure`, never logged:

| `failure` | Resumed agent told |
|---|---|
| `agent_ended` | it ended before `task done`: go on with the task, then commit and run it |
| `rebase` | rebase `task/<plan>/<task>` onto `plan/<name>`, resolve the conflicts in `<files>`, commit, and run `task done` again |
| `check` | after rebasing onto `plan/<name>`, the check `<command>` failed, its output in terminal `<name>`: run it, fix it, commit, and run `task done` again |

`start_failed` has no agent, so it is started as new; its branch, made by the failed start, holds nothing.

### The person's answers

| Answer | From | To | Refused |
|---|---|---|---|
| `task retry [--fresh]` | `failed`, `land_failed` | `pending`, keeping its slot; `--fresh` and `slot_missing` clear `agent_id`, `slot_missing` the slot | `task_state` |
| `task land` | `land_failed` | `landing` | `task_state`; `not_committed`, `wrong_branch` as `task done`; `agent_working` while a live agent is in the slot |
| `task done --plan --task --summary` | `running`, and now `failed` | `landing` | as step 2, and `agent_working` for a failed one |
| `task skip` | `pending`, `failed`, `land_failed` | `skipped`, with `finished_at` | `task_state` |
| `pause` | `running` | `paused`: nothing new starts; agents at work go on and land | `plan_not_running`; `unchanged` when paused; on a closed project it is stored, so the plan stays paused when the project opens |
| `resume` | `paused` | `running` | `plan_not_running`; `unchanged` when running; `project_closed` while the project is closed, as nothing could run |
| `cancel` | `running`, `paused` | `cancelled` | `plan_not_running` |
| `set --parallel <n>` | `running`, `paused` | its `parallel`, 1 to 20 | `plan_not_running`; `unchanged` when the same |

- Every answer needs a run `running` or `paused` (`plan_not_running`, new, 409, otherwise, also for a draft), wakes the runner, and is one `UPDATE … WHERE state IN (<from>)`.
- A retry's slot, `failure` and `conflicts` stay until it is `running` again, so that its start knows what to say; views show `failure` only for `failed` and `land_failed`.
- A run's `parallel` cannot go back to following the setting: nothing needs it yet.
- Cancelling keeps every slot holding work as an ordinary worktree, removes the rest, and keeps `plan/<name>`: wdx never deletes work. A landing going is cut short (its check's terminal stopped), and its task stays `landing` in the cancelled run.

### The API

| Request | Returns |
|---|---|
| `POST /v1/plans/{plan}/pause`, `/resume`, `/cancel` | `paused`, `resumed`, `cancelled` or `unchanged`, `plan` |
| `PATCH /v1/plans/{plan}` gains `parallel` | `updated` or `unchanged`, `plan` |
| `POST /v1/plans/{plan}/tasks/{task}/retry` (`fresh`), `/land`, `/skip` | `retried`, `landing`, `skipped`, `task` |

- `PlanRun` gains `paused_reason`: `person` when its stored state is `paused`, `project_closed` while its project is closed (which shows a running run as `paused`), else null. `Plan.state` follows the same rule, so that a list shows it.
- `PlanTask` gains `check_terminal`, the name of the check's terminal when `land_failed` with `check`, else null; `failure` gains `check` in the enum.
- `Plan` gains `tasks_failed`: its tasks `failed` or `land_failed`, null for a draft, so that a list shows a plan needing the person without reading each one.
- New code `plan_not_running` (409). Reused: `task_state`, `not_committed`, `wrong_branch`, `agent_working`, `invalid_argument`, `project_closed`.

### The CLI

| Command | Text output |
|---|---|
| `plan pause (--id \| --name)` | `plan sso-login paused: nothing new starts, 2 tasks go on` |
| `plan resume (--id \| --name)` | `plan sso-login running again` |

`plan status` says `sso-login: paused (project closed), 2/4 tasks done, on plan/sso-login` while the project is closed.
| `plan cancel (--id \| --name) [--yes]` | `plan sso-login cancelled: 2 agents stopped, 1 worktree kept` |
| `plan set (--id \| --name) --parallel <n>` | `plan sso-login runs up to 4 tasks at once` |
| `plan task retry --plan <name> --task <id> [--fresh]` | `task api of sso-login retried: its agent resumes` (or `a new agent starts`) |
| `plan task land --plan <name> --task <id>` | `task api of sso-login landing on plan/sso-login` |
| `plan task skip --plan <name> --task <id>` | `task api of sso-login skipped: what waits for it goes on without it` |

- `cancel` asks `cancel sso-login? its running agents stop and it cannot run again [y/N]`; `--yes` answers it, and `--json` needs it. The others ask nothing: each is asked for by name, and none loses work.
- `plan list`'s `TASKS` becomes `2/4, 1 failed`; `plan status` and `task get` show `failed: check (terminal land-sso-login-1)`, and `task get` the conflicts.
- `wdx help plans` gains what to do when a task fails or does not land.

### tide

- The Plans section: `needs you` after `2/4` when `tasks_failed` is above 0, or an agent of its tasks is `waiting`, which tide already knows from the agents it has.
- A plan of a closed project shows `paused (project closed)`, in the section and the tab.
- The plan's tab: `p` pauses a running plan and resumes a paused one; `c` cancels it after a confirmation, as `r` runs one.
- `Enter` on a `failed` or `land_failed` task opens a box of its answers: `r` retry, `f` fresh, `l` land (`land_failed` only), `s` skip, `t` its check's terminal (`check` only), `esc`. Each but `t` asks the daemon at once; a refusal shows as other actions' do.
- Not in tide, said in docs/CLI_TIDE.md: `set --parallel` (tuning, rare; the setting is in the Settings tab), and a person's `task done` (a summary is written, best in a shell or by an agent).

### Logging and tests

- Each answer is logged with `plan_id`, `task_id` and why, as step 2's changes; the resumed agent's message by its size.
- `Next`: table tests for a cancelled run's order, a skipped task's slot with work and without, no landing while one is going or the project is closed, finishing while paused.
- In a daemon with the test ACA: a check that fails, then a retry whose agent fixes it and lands; a conflict landed by `task land` after the test resolves it in the slot; an agent that ends, retried and resumed, then `--fresh` with `-2`; a skip letting what waits go on; pause holding a start until resume; a project closed showing its running plan `paused (project closed)` and opened running it again, and one paused by the person staying paused across a close and open; cancel stopping agents and keeping the slot with work.
- The CLI: an integration test per outcome of each new command.

### Found while building step 3

- `worktree.LandOnto` became `RebaseOnto`, the rebase alone: the runner moves the branch itself after the check, again from the rebase if the branch moved during it.
- A task whose agent was removed is retried with a new agent even without `--fresh`: a removed agent is a tombstone, which cannot be resumed. The runner tells it from one that ended (`AgentRemoved`).
- getPlan takes the plan's state and counts from the run it returns (`Plan.AsOf`): read apart, a task that moved between the two reads showed in one and not the other.
- `task done` on a draft is `plan_not_running`, as every request on a task of a plan with no run, rather than `not_found`.
- `plan cancel` and `plan pause` say what happens rather than counting the agents stopped, the worktrees kept or the tasks going on: the runner does that after the request has answered.
- tide's box has `t`, which shows the failed check's terminal in the terminal pane.

### Steps of step 3

Each builds and passes `make test` on its own.

1. The store's answers and `Next`'s new actions (`Stop`, `Release`, `Close`, finishing while paused, landing only when open and alone), table tests.
2. The runner: landing in a goroutine with the check, `Release` and cancelling, a task that started before (its branch, a resumed or `-<n>` agent, the messages).
3. The API and the daemon: pause, resume, cancel, `parallel`, retry, land, skip, a person's `task done` on a failed task, `plan_not_running`, `tasks_failed`, `check_terminal`.
4. The CLI: the new commands, `list`, `status`, `task get`, help.
5. tide: `needs you`, `p`, `c`, the answers' box.
6. docs/CLI_TIDE.md and the README.

## Open

For the steps that need them, not step 1:

- Step 6: can a plan a parent lists be run on its own, and can a plan that has run be listed as a child?
