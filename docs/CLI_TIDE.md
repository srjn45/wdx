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
| List projects | `project list` | `C-q p`, `C-q o` | Same | |
| Create a project | `project create` | `C-q o`, create | Same | |
| Open a local repository | `project open --local` | `C-q o`, local | Same | |
| Clone a repository | `project open --git` | `C-q o`, clone | Same | |
| Answer a missing checkout | `--reclone`, `--remove` or a prompt | a choice screen | Same | |
| Reopen a closed project | `project open --name`, `--id`, or the list | `C-q o`, then `Enter` on it | Same | |
| Close a project | `project close` | `C-q w` | Same | |
| Remove a project | `project remove` | only when no checkout is left | Later (tide) | Comes after close |
| Project details | `project get` | no screen | By design | The agent, terminal and worktree lists already show a project |
| Switch between projects | not needed | `C-q p`, `Alt+Shift+←/→` | By design | A command names its project with a flag, or takes it from the directory |

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
| Read the worktrees again | not needed | `C-q f`, `f` in the list | By design | Each command reads anew |

## Agents

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Create an agent | `agent create` | `C-q a` | Same | |
| Create a child | `agent create --parent-*` | `C-q c` | Same | |
| Start it on a task | `--prompt` | type it into the agent | Later (tide) | Comes with the agent form, with the name and the rest |
| Choose the name when creating | `--name` | a generated name, editable in the info tab | Later (tide) | Comes with an agent form, once there are more fields (ACA, model, role, name) |
| Choose a worktree for an agent with no parent | `--worktree` | only for a child | Later (tide) | Comes with the agent form or the worktrees work |
| List agents | `agent list` | the left pane | Same | |
| Show an agent | `agent get` | the info tab | Same | The fields differ and change as needed: `agent get` also has session, pid and times |
| Rename an agent | `agent rename` | the info tab | Same | |
| Talk to an agent | `agent attach` | its tab | Same | |
| Scroll back through its output | `agent attach` | its tab | Same | |
| Print its history for a script | none | not needed | Open (CLI) | `terminal history` serves shells only. An `agent history` would not give a script the conversation; see [An agent's history](#an-agents-history) |
| Read or grep its conversation | `agent transcript` | none | Open (tide) | Not needed in tide yet: docs/AGENT_RESUME.md is about keeping it. |
| Read its conversation in brief | `agent transcript --brief` | not needed | By design | The agent reads it, in a new session, not a person |
| Send a message from a script | `agent send` | not needed | By design | A person types into the ACA, which queues messages itself. `send` is how scripts and other agents reach an agent |
| Start one that ended | `agent start` | `C-q g` | Same | |
| Start it again in a new session | `agent restart`, with `--force` while it is busy | `C-q n`, after a confirmation | Same | The CLI refuses a busy agent, as a script cannot see it; tide says what it is doing and `y` goes on |
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
| Create, rename and delete files and directories | none, only the API | `n`, `N`, `r` and `d` in the Code section, in popups | By design | A script or an AI changes files itself |
| Choose the editor | `setting set editor` | the settings tab | Same | |
| Order the left pane's sections | `setting set tide.section_order` | the settings tab, which applies it at once | Same | |
| Filter the left pane's list | none | `/`: agents, worktrees, their changed files and the chosen worktree's paths | By design | A script filters the `--json` output itself |
| Install the editor | `deps install --name vi` | none | By design | As every dependency: `deps` manages wdx itself |

## Settings

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List, read and change settings | `setting list`, `get`, `set` | the settings tab, `C-q s` | Same | |

## Only on one side

| Feature | Only on | Status | Why |
|---|---|---|---|
| Live updates of a project | tide | By design | tide needs them to keep its screen current; a script can ask when it needs to know |
| Sending raw keys from a script (Esc, arrows) | `attach` and tide | By design | Both are for a person at a keyboard. The `send` commands can grow this when a need appears |
| Panes, tabs, focus, zoom, folding, selection, `C-q r` | tide | By design | They are tide's own interface |
| `daemon`, `deps`, `observe`, `update`, `setting write`, `completion`, `version` | CLI | By design | They manage wdx itself, not the work in it |

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
