# CLI and tide: what each can do

Status: decided with the human on 2026-11-01, unless a row says Open. Nothing under "Planned" is built yet; it is approved to be built.

The CLI and tide stay in sync, but not one to one. The CLI serves scripts and AIs as well as people; tide serves a person looking at a screen. So a feature is on both unless there is a reason, and that reason is written here. A new feature that reaches only one of them adds a row saying why.

- **Same:** both can do it, perhaps in different ways.
- **By design:** the difference is meant to stay.
- **Planned:** to be built; see [Planned](#planned).
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
| Reopen a closed project | only through `--local` or `--git` | `C-q o`, then `Enter` on it | Planned (CLI) | The CLI cannot name a closed project today |
| Close a project | `project close` | `C-q w` | Same | |
| Remove a project | `project remove` | only when no checkout is left | Later (tide) | Comes after close |
| Project details and checkouts | `project get` | no screen | By design | The agent and terminal lists already show a project; checkouts will show with the worktrees work |
| Switch between projects | not needed | `C-q p`, `Alt+Shift+←/→` | By design | A command names its project with a flag, or takes it from the directory |

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

## Planned

### Reopen a project by id or name

`wdx project open` becomes the CLI's version of tide's `Open Project`.

```
wdx project open              # interactive: choose from the list
wdx project open --name app   # open a known project by name
wdx project open --id <id>    # open a known project by id
wdx project open --git <url>  # clone a repository and open it
wdx project open --local .    # open the repository at a path
```

- **With no flags, on a terminal:** an interactive list of create, local, git and the closed projects.
- **With no flags and no terminal, or with `--json`:** a `usage` error naming the flags.
- **`--id <id>` or `--name <name>` alone:** opens that known project, whichever state it is in. Completion offers the closed ones.
- **`--name` with `--local` or `--git`:** names the new project, as it does today.
- **`--id` with `--local` or `--git`:** a `usage` error.
- **No checkout left:** the flags above still ask, as `--git` does today: clone it again, when it has an origin, or remove it. `--reclone` and `--remove` answer in advance and, as with `--git`, one of them is required with `--json`.

No use that works today changes: `project open` without `--local` or `--git` is a `usage` error now.

## An agent's history

Terminal history holds only what scrolls off the main screen (docs/ATTACH.md). With Claude Code's `"tui": "fullscreen"` setting the program stays on the alternate screen, which never scrolls into history, and it does its own scrolling. On 2026-11-01 two running agents with that setting had 0 lines of history, against transcripts of 876 and 1,568 lines.

So the conversation is in the ACA's transcript (`~/.claude/projects/*/<session>.jsonl`), not in wdx. A command giving scripts the conversation would read the transcript through `AiCodingAgent`, one implementation per ACA. Not decided.
