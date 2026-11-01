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

## Worktrees

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| List the worktrees and their uncommitted files | `worktree list` | the left pane | Same | tide's stays up to date, and the CLI reads once |
| A file's uncommitted changes | `worktree diff --path` | a tab, split in two | By design | A script needs the patch, a person the two versions side by side |
| What a worktree has committed | `worktree diff` | its tab | Same | |
| Everything a worktree has not committed | `worktree diff --uncommitted` | its tab | Same | |
| Ask an agent to commit | `agent send` | `C` in a worktree's tab | Same | |
| Read the worktrees again | not needed | `C-q f`, `f` in the list | By design | Each command reads anew |

## Agents

| Feature | CLI | tide | Status | Why they differ |
|---|---|---|---|---|
| Create an agent | `agent create` | `C-q a` | Same | |
| Create a child | `agent create --parent-*` | `C-q c` | Same | |
| Choose the name when creating | `--name` | a generated name, editable in the info tab | Later (tide) | Comes with an agent form, once there are more fields (ACA, model, role, name) |
| Choose a worktree for an agent with no parent | `--worktree` | only for a child | Later (tide) | Comes with the agent form or the worktrees work |
| List agents | `agent list` | the left pane | Same | |
| Show an agent | `agent get` | the info tab | Same | The fields differ and change as needed: `agent get` also has session, pid and times |
| Rename an agent | `agent rename` | the info tab | Same | |
| Talk to an agent | `agent attach` | its tab | Same | |
| Scroll back through its output | `agent attach` | its tab | Same | |
| Print its history for a script | none | not needed | Open (CLI) | `terminal history` serves shells only. An `agent history` would not give a script the conversation; see [An agent's history](#an-agents-history) |
| Read or grep its conversation | `agent transcript` | none | Open (tide) | Not needed in tide yet: docs/AGENT_RESUME.md is about keeping it. |
| Send a message from a script | `agent send` | not needed | By design | A person types into the ACA, which queues messages itself. `send` is how scripts and other agents reach an agent |
| Start one that ended | `agent start` | `C-q g` | Same | |
| Remove an agent | `agent remove` | `C-q x` | Same | |

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
