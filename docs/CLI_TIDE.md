# CLI and tide: what each can do

Status: decided with the human on 2026-11-01, unless a row says Open.

The CLI and tide stay in sync, but not one to one. The CLI serves scripts and AIs as well as people; tide serves a person looking at a screen. So a feature is on both unless there is a reason, and that reason is written here. A new feature that reaches only one of them adds a row saying why.

- **Same:** both can do it, perhaps in different ways.
- **By design:** the difference is meant to stay.
- **Later:** waits for something else, or for a real need.
- **Open:** not decided.

## Projects

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List projects | `project list` | `C-q p s`, `C-q p o` | Same | |
| Create a project | `project create` | `C-q p o`, create | Same | |
| Open a local repository | `project open --local` | `C-q p o`, local | Same | |
| Clone a repository | `project open --git` | `C-q p o`, clone | Same | |
| Answer a missing checkout | `--reclone`, `--remove` or a prompt | a choice screen | Same | |
| Reopen a closed project | `project open --name`, `--id`, or the list | `C-q p o`, then `Enter` on it | Same | |
| Close a project | `project close` | `C-q p c` | Same | |
| Remove a project | `project remove` | `C-q p x`, the project shown, after a confirmation; a closed one only when no checkout is left | Same | A closed project is removed with the CLI: tide removes the one it shows |
| Project details | `project get` | no screen | By design | The agent, terminal and worktree lists already show a project |
| Switch between projects | not needed | `C-q p s`, `Alt+Shift+←/→` | By design | A command names its project with a flag, or takes it from the directory |
| Keep what tide showed across runs | none, only the API | the tabs, the project shown, focus, zoom, cursor, folds and collapsed sections kept by the daemon come back, and a removal through wdx closes its tab | By design | It is tide's view; the CLI shows none (docs/TIDE_STATE.md) |

## Messages

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| See a message waiting | `message list` | `1 held` on the agent's row and its info tab | Later (tide) | A held one is rare, so tide says it exists and the CLI deals with it for now |
| Send or discard a held one | `message resend`, `message discard` | none | Later (tide) | As above |

## Worktrees

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List the worktrees and their uncommitted files | `worktree list` | the left pane | Same | tide's stays up to date, and the CLI reads once |
| A file's uncommitted changes | `worktree diff --path` | a tab, split in two | By design | A script needs the patch, a person the two versions side by side |
| What a worktree has committed | `worktree diff` | its tab | Same | |
| Everything a worktree has not committed | `worktree diff --uncommitted` | its tab | Same | |
| Ask an agent to commit | `agent send` | `C` in a worktree's tab | Same | |
| See which files are verified | `worktree list --json` | a `✓` in the list and the diffs | Same | |
| Mark a file verified | none, only the API | `v` | By design | It says a person looked at the file, so an agent or a script should not be saying it |
| See a worktree's comments | `worktree comments` | under their lines | Same | |
| Write a comment and send a review | none, only the API | `c`, `e`, `d`, `s` | Later (CLI) | For now it is a person's review. An agent reviewing another would need a command |
| Land a worktree | `worktree land` | `L` in its tab | By design | tide shows what lands first and offers to remove it after. The CLI waits for the end and answers with a code. Only tide offers to ask an agent after a conflict: a script has `agent send`. Only the CLI has `--method` for one landing: in tide it is the project's, and `m` in the box changes it. Only the CLI has `--agent-id`, the agent asking, not counted as working there: tide's person is no agent |
| Rebase a worktree onto its base | `worktree rebase`: `rebased` or `up_to_date`; a conflict, undone, `rebase_failed` with the files, uncommitted changes `not_committed`, the hint naming `agent send` | `R` on its row, in its tab and in the row's menu, the bar saying `R rebase` once its base has moved on; a conflict opens a box to send the rebase to an agent, uncommitted changes `C`'s box to have one commit first | By design | The CLI sends nothing to an agent: a script has `agent send`, and the question is tide's, where the person is. Both refuse the main checkout and a slot of a plan going on; a branch on origin is pushed again with `--force-with-lease`, the one place wdx forces (docs/PLANS.md, Step 7 in detail). Only the CLI has `--agent-id`, the agent asking, not counted as working there: tide's person is no agent |
| See a worktree's PR | `worktree list --json` | on its row | By design | tide asks `gh` when a tab opens or on `f`. The CLI shows what was last said, as a list must not reach the network; `worktree remove` asks |
| Say how a project lands | `project landing` | the box of `L` | Same | |
| Remove a worktree that landed | `worktree remove` | `C-q x` on its row | By design | tide confirms, listing who is affected. The CLI asks nothing, as only what landed can go and a script must not stop at a question |
| Read the worktrees again | not needed | `f` in the left pane | By design | Each command reads anew |

## Agents

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Create an agent | `agent create` | `C-q a`, a form | Same | tide asks every field in one box; the CLI takes flags and asks nothing, as each has a default |
| Create a child | `agent create --parent-*` | `C-q c`, the same form with the parent filled in | Same | |
| Start it on a task | `--prompt` | the form's Prompt, one line | Same | |
| Choose the name when creating | `--name` | the form's Name | Same | |
| Choose a worktree for an agent with no parent | `--worktree` | the form's Worktree | Same | |
| Choose its model and permissions when creating | `--model`, `--permissions` | the form's Model and Permissions | Same | |
| See its model and permissions | `agent get` | the info tab | Same | |
| List the models to choose from | `agent models [--aca]`, `--model`'s completion | the form's model list, filtered by what is typed | Same | |
| See how much of an ACA's account's limits is used, and whether it can be used | `usage` | the stats tab, `C-q S` (docs/TIDE_STATS.md) | Same | the CLI names the fact, `usage`; tide's tab is generic, `stats`, so that other machine-wide facts join it over time |
| Choose its AI coding agent when creating | `--aca` | the form's ACA field, shown with two or more | Same | |
| List the AI coding agents to choose from | `agent acas`, `--aca`'s completion | the field's ‹ › | Same | |
| See why an ACA is not offered | `deps list` (installed), `setting list` (enabled) | the settings tab (enabled); installed is not shown | Later (tide) | tide has no deps screen yet |
| Turn an ACA off for new agents | `setting set --name aca.<id>.enabled` | the settings tab | Same | |
| No ACA can be used | `agent create` and `agent models` fail with `aca_unavailable` | `C-q a`, `C-q c` say so on the status line | Same | |
| List agents | `agent list` | the left pane | Same | |
| Show an agent | `agent get` | the info tab | Same | The fields differ and change as needed: `agent get` also has session, pid and times |
| See what a waiting agent asks | `agent get`'s `prompt`, `question`, `detail` and `options`; `agent list`'s `ACTIVITY` reads `yes/no` or `decision` in place of `waiting` | the row reads `yes/no` or `decision` in place of `waiting`; the info tab's `prompt`, `question`, `detail` and `options` rows | Same | Only while a prompt is read (Claude Code for now, docs/AGENT_STATE.md); a folded parent's `N waiting` stays. Both show `❯` before the selected option and, in a multi-select, `[✔]` before a ticked one and `[ ]` before the others |
| See how full its context is | `agent get` (`70k of 200k (35%)`), `agent list`'s `CONTEXT` column | the info tab, the size at its row's end | Same | Both color it by `context.threshold.warning` and `context.threshold.critical`: green below warning, yellow from it, red from critical (the wdx doc CONTEXT_SIZE) |
| Rename an agent | `agent rename` | `r` on it, in the list or its tab, or the info tab | Same | |
| Talk to an agent | `agent attach` | its tab | Same | |
| Scroll back through its output | `agent attach` | its tab | Same | |
| Keys only the kitty keyboard protocol tells apart (`Shift+Enter`) | `agent attach`, `terminal attach` | its tab | Same | Both are termview, whose vt encodes them (docs/KITTY_KEYS.md) |
| Print its history for a script | none | not needed | Open (CLI) | `terminal history` serves shells only. An `agent history` would not give a script the conversation; see [An agent's history](#an-agents-history) |
| Read or grep its conversation | `agent transcript` | none | Open (tide) | Not needed in tide yet: docs/AGENT_RESUME.md is about keeping it. |
| Read its conversation in brief | `agent transcript --brief` | not needed | By design | The agent reads it, in a new session, not a person |
| Send a message from a script | `agent send` | not needed | By design | A person types into the ACA, which queues messages itself. `send` is how scripts and other agents reach an agent |
| Start one that ended | `agent start` | `C-q g` | Same | |
| Start it again in a new session | `agent restart`, with `--force` while it is busy | `C-q R`, after a confirmation | Same | The CLI refuses a busy agent, as a script cannot see it; tide says what it is doing and `y` goes on |
| Remove an agent | `agent remove` | `C-q x` | Same | |
| Say which background session of an agent was stopped, by restarting, starting or removing it or closing or removing its project | `stopped_sessions`, and a line of text | The status line | Same | |

## Terminals

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Create a terminal | `terminal create` | `C-q t` | Same | |
| Choose the name when creating | `--name` | a generated name | Later (tide) | Terminals have no rename yet, on either side |
| Choose the checkout | the directory it runs from | the first checkout on disk | Later (tide) | Comes with the worktrees work |
| List terminals | `terminal list` | the tabs | Same | |
| Show a terminal | `terminal get` | none | Later (tide) | No real need for it in tide yet |
| Talk to a terminal | `terminal attach` | its tab | Same | |
| Scroll back through its output | `terminal attach` | its tab | Same | |
| Print its history for a script | `terminal history` | not needed | By design | tide scrolls back; the command exists for scripts and AIs |
| Send a message from a script | `terminal send` | not needed | By design | A person types directly |
| Start one that ended | `terminal start` | `C-q g` | Same | |
| Remove a terminal | `terminal remove` | `C-q x` | Same | |

## Code

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Browse a worktree's files | none, only the API | the Code section, `< worktree >` and a tree | By design | A script or an AI reads files itself (docs/TIDE_CODE.md) |
| See which files a worktree changed | `worktree list` for what is not committed, `worktree diff` for what is | the Code tree's letters and colours, and a count on each directory | By design | The CLI has no tree to mark; its lists say the same (docs/TIDE_CODE.md) |
| Read a file, highlighted | none, only the API | `Enter` on it: a read-only tab | By design | As above |
| Edit a file | none, only the API | `e`: the `editor` setting's editor in its tab; closing it asks to save or discard | By design | A script or an AI writes files itself; a person edits in an editor |
| Create, rename and delete files and directories | none, only the API | `n`, `N`, `r` and `x` in the Code section, in popups | By design | A script or an AI changes files itself |
| Choose the editor | `setting set editor` | the settings tab | Same | |
| Order the left pane's sections | `setting set tide.section_order` | the settings tab, which applies it at once | Same | |
| Choose tide's colors | `setting set tide.theme` | the settings tab, which applies it at once | Same | Only `slate` so far (the wdx doc TIDE_COLORS) |
| Filter the left pane's list | none | `/`: agents, worktrees, their changed files, the chosen worktree's paths, docs and plans | By design | A script filters the `--json` output itself |
| Install the editor | `deps install --name vi` | none | By design | As every dependency: `deps` manages wdx itself |

## Docs

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List a project's docs | `doc list` | the Docs section | Same | tide's stays up to date (docs/DOCS.md) |
| Filter docs | `--labels`, `--status` | `/`: a name or labels | Same | |
| Read a doc | `doc get` | `Enter`: a read-only tab | Same | |
| Read Markdown rendered | none: `doc get` prints the source | a doc of format `md` or a `.md` file shows rendered, `m` its source | By design | Scripts and AIs want the source; a person in a shell can pipe it to a renderer of their choice (the wdx doc TIDE_MARKDOWN) |
| Read an older revision, history | `doc get --revision`, `doc history` | none | By design | AIs write docs, and tide only shows them for now |
| Create, update, status and labels, rename, remove | `doc create`, `update`, `set`, `rename`, `remove` | none | By design | As above |
| Write the docs into the repository | `doc sync-to-git`, `docs.git_path` | the settings tab, for the setting | By design | Writing them once is for a script; the setting is in both |
| A doc's or plan's folder | `--folder` on `doc create`, `plan create`; `doc set --folder`, `plan set --folder`; a `FOLDER` column in `doc list` and `plan list` | the Docs and Plans sections as a tree of folders, folded with `←` `→`, `Enter` or a click, a folded one saying `needs you` for a plan in it; `m` moves the doc or plan under the cursor | By design | tide creates no doc, so it sets a folder only by moving one; the CLI's lists are flat, sorted by folder. Moving a whole folder at once is on neither (docs/FOLDERS.md) |

## Plans

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List a project's plans | `plan list`, a run's `2/4` in `TASKS`, `2/4, 1 failed` when tasks wait for the person; the archived only with `--all` | the Plans section, a plan of plans with its plans under it, folded with `←` `→`, a run's state and `2/4`, and `needs you` when a task failed or did not land, or an agent of it waits for input; the archived only found with `/` | Same | tide's stays up to date (docs/PLANS.md); the CLI's list is flat, with `3 plans` for a plan of plans. An agent waiting is tide's, which follows agents' activity; the CLI says it in `agent list` |
| Choose which plans to see: all, draft and in progress, or completed | none: `plan list` says each plan's state | the Plans section's first row, `< Draft & in progress >`: `←` `→` or `h` `l` go round All, Draft & in progress and Completed, `<` `>` can be clicked; each project keeps its choice | By design | A script picks the states it wants from the JSON; a person reads what is going on apart from what is over (docs/PLANS.md, Step 9 in detail) |
| See what a run runs: its agents and its worktrees | `plan status`, with `AGENT` and `SLOT` per task; `agent list --json` gives an agent's `task`, `plan list --json` a plan's `slots` | the Agents section: a plan's agents under a row for the plan, with its state and `2/4`; the Worktrees section: its slots under one with its branch, each with its task and agent, or `free`. Each folded with `←` `→` on its own, saying `needs you` in the Agents section; `Enter` opens the plan's tab | By design | A run of five tasks is one row to fold in tide; the CLI's lists stay flat, as a script groups the JSON itself (docs/PLANS.md, Step 5 in detail) |
| Land or remove a slot of a running plan | refused: `worktree land` and `worktree remove` fail with `worktree_in_plan`, naming the plan | not offered: no `x` on a slot, and `L` in its tab says the plan lands it | Same | Either would break the run; once the plan is done or cancelled its worktrees are landed and removed as any |
| Read what a plan defines: its goal, constraints and tasks or plans in DAG order | `plan status` | `Enter` or a click: the plan's tab, a header with its state, revision and a progress bar, the goal, the constraints folded with `→` `←`, the tasks or plans listed with a panel for the one under the cursor | Same | |
| Read a plan's DAG: what each task or plan waits for and unblocks | `plan status`'s `AFTER` column | the tab's rail, drawn as `git log --graph` draws commits; the row under the cursor marks those it needs `▲ needs` and those waiting for it `▼ unblocks`, its edges in the accent style, and its panel lists both. Past 6 lanes the rail is left out | By design | A person reads a graph at a glance; a script reads the `after` lists in the JSON (docs/PLANS.md, Step 8 in detail) |
| See what a plan's state takes | the errors of the commands it refuses, with a hint | the tab's keys line, only the keys its state takes (`r run  y copy` for a draft, `p pause  c cancel  y copy` running...), then what `Enter` does on the row under the cursor | By design | tide shows what a key does before it is pressed; the CLI's help lists every command, and a refused one says why |
| Read a task's prompt | `plan status --json`, `plan get`, `plan task get` | the panel of the task under the cursor, cut to fit; `Enter` on a task neither running, landing nor waiting for the person shows it whole, with its summary, `Esc` back | Same | |
| Run a draft plan | `plan run`, `--parallel` | `r` in its tab, after a y/n question | Same | tide asks first, as it starts agents; it runs with `plan.parallel`, as a run's own number is for a script that knows the machine |
| Follow a run: each task's state, agent and worktree | `plan status`, `plan task get` | the plan's tab, read again on every step | Same | tide's stays up to date |
| Read a done task's summary | `plan task get` | the task's panel in the plan's tab, cut to fit; `Enter` shows it whole | Same | |
| Go to a running task's agent | `plan status` names it, then `agent attach` | `Enter` on the task in the plan's tab | Same | |
| Say a task is done | `plan task done`, by `--agent-id` or `--plan` and `--task`, also for a failed task the person finished | none | By design | The task's agent says it, through the CLI; a person who finished a task by hand does too, as it takes a summary, written best in a shell or by an agent |
| Pause or resume a run | `plan pause`, `plan resume` | `p` in its tab | Same | A closed project shows its running plans paused, and resumes them when it opens, on both |
| Cancel a run | `plan cancel`, after a y/n question, `--yes` | `c` in its tab, after a y/n question | Same | |
| Change how many tasks a run runs at once | `plan set --parallel` | none | By design | Rare, and tuned to the machine; the setting `plan.parallel` is in tide's Settings tab |
| Answer a task that failed or did not land: retry it, with a new agent, land it again, skip it | `plan task retry`, `--fresh`, `plan task land`, `plan task skip` | `Enter` on the task in its plan's tab: a box with `r`, `f`, `l`, `s` | Same | |
| Read a failed check's output | `plan task get` names its terminal, then `terminal attach` | `t` in the task's box | Same | |
| Go from a plan of plans to one of its plans | `plan status --name <child>` | `Enter` on the plan in the tab | Same | |
| Run a plan of plans: each of its plans once those before it have landed | `plan run` on it; `plan status` lists its plans with `STATE` (`waiting for sso`, `waiting for you`) and `TASKS`; `plan list` says `1/3 plans` | `r` in its tab, after a y/n question; the tab lists its plans with their state, and the Plans section says `needs you` on it and on the plan waiting | Same | `plans_waiting` on its row tells tide which wait, as the stream sends rows, not definitions; neither takes `--parallel`, each plan's own |
| Answer a plan that waits for you: start it once the work before it is landed, or merged on GitHub and pulled | `plan run` on it, refused with `plan_not_landed` or `plan_not_finished` and a hint | `r` on its row in its parent's tab, saying in the bar why not | Same | tide asks nothing first, as you picked the row; both ask gh only here, as it uses the network |
| Have wdx open a plan's PR once it is done | `pr: true` in the plan, or in its plan of plans for each it lists; the PR's state in `worktree list --json`, and in the log when it could not start | the same key; the plan's worktree row says `PR #12 open`, or why the landing failed, and `L` opens it once dealt with | Same | A landing that fails leaves the plan done on both, its work on its branch (docs/PLANS.md, Step 7 in detail) |
| Stack a plan of plans' PRs: start a plan on the branch of the one before it, its PR against that branch | `stacked: true` with `pr: true`; `plan status --json` gives each plan's `stacked_on`; `worktree rebase` on its worktree once the PR below moved on or merged, `base_not_pulled` with the hint `pull main` when merged and not pulled | the same keys; the parent's tab says `stacked on auth-a` on the plan, and `rebase: R` when the plan below has moved on; `R` there or on the worktree rebases it | Same | Each rebase is the person's, from the bottom of the stack up, and asks gh about the PR below, as it uses the network; the conflicts and uncommitted changes of a stacked worktree go as for any rebase |
| Copy a plan, to run it again or start from it | `plan copy --new-name`, `--folder` | `y` in its tab: a popup with the first name free, then the copy's tab | Same | tide's copy keeps the plan's folder, which `m` changes. A plan of plans is copied with its plans not done, to run again what is left, on both |
| Remove a plan's agents once it is over | done by the daemon once the plan is done or cancelled: `plan status` and `plan task get` then give each task's agent as null, and `agent list` no longer lists them | done by the daemon: the agents leave the Agents section, and the tab's AGENT says `—` | Same | The daemon does it; each task keeps its state and summary. An agent still running, as one restarted, or with children of its own stays, for `agent remove` |
| Archive a plan that is over, or bring it back | `plan archive`, `plan unarchive` | `a` in its tab, both ways | Same | Nothing is asked on either side, as nothing is lost |
| Read the YAML, an older revision, history | `plan get`, `--revision`, `plan history` | none | By design | AIs write plans, and tide shows what they define |
| Create, update, rename, remove | `plan create`, `update`, `rename`, `remove` | none | By design | As above |
| The format's reference | `wdx help plans` | none | By design | Help topics are the CLI's; an AI writing a plan reads it there |

## Settings

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List, read and change settings | `setting list`, `get`, `set` | the settings tab, `C-q s` | Same | |

## Only on one side

| Feature | Only on | Status | Why |
|---|---|---|---|
| Live updates of a project | tide | By design | tide needs them to keep its screen current; a script can ask when it needs to know |
| Sending raw keys from a script (Esc, arrows) | `attach` and tide | By design | Both are for a person at a keyboard. The `send` commands can grow this when a need appears |
| Panes, tabs, focus, zoom, folding, selection, the menu's parts | tide | By design | They are tide's own interface |
| `daemon`, `deps`, `observe`, `update`, `setting write`, `completion`, `version` | CLI | By design | They manage wdx itself, not the work in it |
| Help topics (`wdx help json`, `settings`, `logs`, `agents`) | CLI | By design | They are for scripts and AI agents, which use the CLI; tide's keys are in its bar and `?` |
| Pushing a project's docs to the hub, and what waits or was refused (`project sync`, `--on`, `--off`) | CLI | Later (tide) | Not in tide in the MVP (the wdx doc HUB_DOCS_PUSH): it is set once per project, and its state, a count and the docs refused, can come to tide with the hub's |
| Joining a hub and its nodes (`hub login`, `logout`, `status`, `node list`, `get`, `remove`) | CLI | Later (tide) | Login needs a browser, or a code typed on another device, which a terminal command hands over best; tide's window is where a remote session will show, with step 9 of the hub (docs/HUB_ARCH.md), and the hub's state can come to it then |
| The host's authenticator code (`hub code setup`, `--replace`, `recover`) and its audit chain (`hub audit verify`) | CLI | By design | The secret and the recovery key are shown once, and must never reach a screen an AI reads: tide's panes show agents' terminals. Setting up a code is done once per node, which a terminal command does best; both commands are interactive only (the wdx doc HUB_TUNNEL) |
| Sessions between one's own nodes (`hub session open`, `list`, `close`) | CLI | Later (tide) | `--node` runs commands through them since step 8 of the hub; tide shows that a session is open, and opens one for a remote tide, with step 9 (docs/HUB_ARCH.md) |
| Running a command on another of one's nodes (`--node <name>` on `agent`, `terminal`, `project`, `doc`, `plan`, `worktree`, `message` and `version`) | CLI | Later (tide) | Step 8 of the hub (the wdx doc HUB_NODE_COMMANDS) gives the CLI's commands `--node`, asking the host's code when no session is open; `terminal attach` and `agent attach` refuse it with `usage` until step 9, which relays the holder's protocol and brings `wdx tide --node`, a whole tide on the host, rather than a flag per action |

## `wdx project open`

The CLI's version of tide's `Open Project`.

```
wdx project open              # choose from a list: create, local, git, the closed projects
wdx project open --name app   # open a known project by name
wdx project open --id <id>    # open a known project by id
wdx project open --git <url>  # clone a repository and open it
wdx project open --local .    # open the repository at a path
```

- **With no flags and no terminal, or with `--json`:** a `usage` error naming the flags.
- **`--id` or `--name` alone:** opens that known project, whichever state it is in. Completion offers the closed ones.
- **`--name` with `--local` or `--git`:** names the new project.
- **No checkout left:** `--id`, `--name` and `--git` ask whether to clone it again, offered only when it has an origin, or to remove it. `--reclone` and `--remove` answer in advance; with `--json` one of them is required.

## An agent's history

Terminal history holds only what scrolls off the main screen (docs/ATTACH.md). With Claude Code's `"tui": "fullscreen"` setting the program stays on the alternate screen, which never scrolls into history, and it does its own scrolling. On 2026-11-01 two running agents with that setting had 0 lines of history, against transcripts of 876 and 1,568 lines.

So the conversation is in the ACA's transcript (`~/.claude/projects/*/<session>.jsonl`), not in wdx. A command giving scripts the conversation would read the transcript through `AiCodingAgent`, one implementation per ACA. Not decided.
