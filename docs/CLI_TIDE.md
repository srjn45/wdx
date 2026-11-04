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
| Land a worktree | `worktree land` | `L` in its tab | By design | tide shows what lands first and offers to remove it after. The CLI waits for the end and answers with a code. Only tide offers to ask an agent after a conflict: a script has `agent send`. Only the CLI has `--method` for one landing: in tide it is the project's, and `m` in the box changes it |
| See a worktree's PR | `worktree list --json` | on its row | By design | tide asks `gh` when a tab opens or on `f`. The CLI shows what was last said, as a list must not reach the network; `worktree remove` asks |
| Say how a project lands | `project landing` | the box of `L` | Same | |
| Remove a worktree that landed | `worktree remove` | `C-q x` on its row | By design | tide confirms, listing who is affected. The CLI asks nothing, as only what landed can go and a script must not stop at a question |
| Read the worktrees again | not needed | `f` in the left pane | By design | Each command reads anew |

## Agents

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Create an agent | `agent create` | `C-q a` | Same | |
| Create a child | `agent create --parent-*` | `C-q c` | Same | |
| Start it on a task | `--prompt` | type it into the agent | Later (tide) | Comes with the agent form, with the name and the rest |
| Choose the name when creating | `--name` | a generated name, editable in the info tab | Later (tide) | Comes with an agent form, once there are more fields (ACA, model, role, name) |
| Choose a worktree for an agent with no parent | `--worktree` | only for a child | Later (tide) | Comes with the agent form or the worktrees work |
| Choose its model and permissions when creating | `--model`, `--permissions` | the defaults: the ACA's model, `full` | Later (tide) | Comes with the agent form (the wdx doc AGENT_CREATE, step 3) |
| See its model and permissions | `agent get` | the info tab | Same | |
| List the models to choose from | `agent models`, `--model`'s completion | the agent form's list | Later (tide) | Comes with the agent form (the wdx doc AGENT_CREATE, step 3) |
| List agents | `agent list` | the left pane | Same | |
| Show an agent | `agent get` | the info tab | Same | The fields differ and change as needed: `agent get` also has session, pid and times |
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
| Filter the left pane's list | none | `/`: agents, worktrees, their changed files, the chosen worktree's paths, docs and plans | By design | A script filters the `--json` output itself |
| Install the editor | `deps install --name vi` | none | By design | As every dependency: `deps` manages wdx itself |

## Docs

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List a project's docs | `doc list` | the Docs section | Same | tide's stays up to date (docs/DOCS.md) |
| Filter docs | `--labels`, `--status` | `/`: a name or labels | Same | |
| Read a doc | `doc get` | `Enter`: a read-only tab | Same | |
| Read an older revision, history | `doc get --revision`, `doc history` | none | By design | AIs write docs, and tide only shows them for now |
| Create, update, status and labels, rename, remove | `doc create`, `update`, `set`, `rename`, `remove` | none | By design | As above |
| Write the docs into the repository | `doc sync-to-git`, `docs.git_path` | the settings tab, for the setting | By design | Writing them once is for a script; the setting is in both |
| A doc's or plan's folder | `--folder` on `doc create`, `plan create`; `doc set --folder`, `plan set --folder`; a `FOLDER` column in `doc list` and `plan list` | the Docs and Plans sections as a tree of folders, folded with `←` `→`, `Enter` or a click, a folded one saying `needs you` for a plan in it; `m` moves the doc or plan under the cursor | By design | tide creates no doc, so it sets a folder only by moving one; the CLI's lists are flat, sorted by folder. Moving a whole folder at once is on neither (docs/FOLDERS.md) |

## Plans

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List a project's plans | `plan list`, a run's `2/4` in `TASKS`, `2/4, 1 failed` when tasks wait for the person | the Plans section, a plan of plans with its plans under it, folded with `←` `→`, a run's state and `2/4`, and `needs you` when a task failed or did not land, or an agent of it waits for input | Same | tide's stays up to date (docs/PLANS.md); the CLI's list is flat, with `3 plans` for a plan of plans. An agent waiting is tide's, which follows agents' activity; the CLI says it in `agent list` |
| Read what a plan defines: its goal, constraints and tasks or plans in DAG order | `plan status` | `Enter` or a click: a read-only tab | Same | |
| Read a task's prompt | `plan status --json`, `plan get`, `plan task get` | `Enter` on the task in the plan's tab | Same | |
| Run a draft plan | `plan run`, `--parallel` | `r` in its tab, after a y/n question | Same | tide asks first, as it starts agents; it runs with `plan.parallel`, as a run's own number is for a script that knows the machine |
| Follow a run: each task's state, agent and worktree | `plan status`, `plan task get` | the plan's tab, read again on every step | Same | tide's stays up to date |
| Read a done task's summary | `plan task get` | `Enter` on the task in the plan's tab | Same | |
| Go to a running task's agent | `plan status` names it, then `agent attach` | `Enter` on the task in the plan's tab | Same | |
| Say a task is done | `plan task done`, by `--agent-id` or `--plan` and `--task`, also for a failed task the person finished | none | By design | The task's agent says it, through the CLI; a person who finished a task by hand does too, as it takes a summary, written best in a shell or by an agent |
| Pause or resume a run | `plan pause`, `plan resume` | `p` in its tab | Same | A closed project shows its running plans paused, and resumes them when it opens, on both |
| Cancel a run | `plan cancel`, after a y/n question, `--yes` | `c` in its tab, after a y/n question | Same | |
| Change how many tasks a run runs at once | `plan set --parallel` | none | By design | Rare, and tuned to the machine; the setting `plan.parallel` is in tide's Settings tab |
| Answer a task that failed or did not land: retry it, with a new agent, land it again, skip it | `plan task retry`, `--fresh`, `plan task land`, `plan task skip` | `Enter` on the task in its plan's tab: a box with `r`, `f`, `l`, `s` | Same | |
| Read a failed check's output | `plan task get` names its terminal, then `terminal attach` | `t` in the task's box | Same | |
| Go from a plan of plans to one of its plans | `plan status --name <child>` | `Enter` on the plan in the tab | Same | |
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
