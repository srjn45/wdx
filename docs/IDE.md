# The IDE direction

Status: notes from an open discussion on 2026-10-31 and 2026-11-01, a shared picture of where wdx is going rather than a plan to build. Most of it is far off. Nothing here is final, what the human said included: each part's decisions are worked out when that part is worked on, with its own design before any code. "Proposed" marks what came from the AI and was not agreed.

The aim: nobody needs to leave tide. Agents, terminals, the PR-like diff, docs, plans and the whole codebase are in one place.

## The shape

The right pane stops being "the agent pane" and hosts tabs. Each feature is then a section on the left, a tab type on the right, and an API and CLI behind them, so that agents and the web IDE reach the same data.

```
┌ [Project: wdx] ───────┬ swift-falcon │ info: swift-falcon │ diff: auth-fix │ settings ┐
│ Docs                2 │
│ Plans               1 │
│ Agents              4 │
│ Worktrees           3 │
│ ▾ auth-fix    +120 −8 │
│    M tide/tabs.go     │
│ Code: auth-fix main   │
```

## Right pane tabs

- `Alt+←` and `Alt+→` switch tabs. `Alt+←` on the leftmost tab moves the focus to the left pane.
- `C-q -` closes the tab.
- Tab types: an agent, an agent's info, settings, a doc, a plan, a diff, a file.

Proposed:

- `Enter` on a left-pane row opens a tab, or goes to it if it is open.
- Closing a tab stops nothing, and `C-q x` stays the way to remove. An editor still running asks first, since closing could lose unsaved work.
- Only the visible tab is attached, as with terminal tabs. `C-q r` keeps the open tabs.
- Diffs, docs, plans and read-only files share one scrolling text view built into tide.

## Agent info

`wdx agent info` returns everything about an agent, and tide shows it in a tab of the right pane. Which fields are in it is decided later. `wdx agent get` exists already and returns what is stored; proposed: extend it rather than have both.

## Settings

A tab of the right pane. Proposed: through a daemon API, since tide may not import `config` and the web IDE needs one too.

## Worktrees

Built on 2026-11-01: docs/TIDE_WORKTREES.md, which is what holds now.

Shows every active worktree, for reviewing changes before there is a PR.

- Under a worktree: its uncommitted changes, file by file. A file opens its diff.
- On the worktree itself: its complete diff against main by default, with an option to change the base branch.

Proposed: the complete diff is what a PR would show, measured from the merge-base so that later commits on the base do not appear as removals, plus what is not committed yet. The base chosen lasts for the tide run.

## Code

The whole project's files and directories, opened read-only or for writing in a terminal editor. The heading names what it shows, `Code: <worktree> <branch>`, and the user can switch both.

Built on 2026-11-02 as docs/TIDE_CODE.md describes: a Code section with `< worktree >`, a read-only highlighted tab, and `e` for the `editor` setting's editor. What follows is the picture it started from.

Proposed:

- `Enter` opens a file read-only in the text view; `e` opens it in the editor, which runs as a terminal under a holder.
- Switching never runs `git checkout`, since an agent may be working in that checkout. A branch checked out in a worktree shows that worktree. Any other branch is read from git, read-only: to edit it, make a worktree.
- The tree follows `.gitignore`, and has a fuzzy file finder.

## Docs and plans

Docs are being designed in docs/DOCS.md, which holds where it differs from this section.

Docs are tech docs such as RFCs, tech specs and one-pagers. Plans are DAG implementation plans, as in warden. Both must be shared across developers, so that everyone sees parallel plans and changes.

Running a plan needs pipelines and the orchestrator and worker model first, so those come before plans.

### Where they live

Agreed on 2026-11-01:

- Docs and plans are kept completely in wdx, not in git. A commit or push that gets missed would mean an extra PR or push, so sharing must not depend on one.
- `wdx-hub`, a central server, is how users share docs and plans with each other (docs/HUB.md). An ecosystem is to be built around wdx.
- `sync-to-git` is a command for those who also want them in git.

Proposed:

- They are tables in the one SQLite database, with a row per revision, so that they work offline and with no hub.
- Doc and plan ids are random and global, and each revision has a content hash, from the first migration, so that the hub needs no change to them.
- Editing works like `git commit`: `wdx doc edit` opens the editor on a temporary file and stores a new revision when it exits. If the doc changed meanwhile, it fails with a conflict and keeps the edit. `--file` brings in existing markdown.
- With a hub set, each new revision is pushed by itself, and revisions made offline wait for the connection. There is nothing for a user to forget.
- Only content and a plan's coarse status and owner are shared. Executions, task progress, worktree paths and transcripts stay on the machine.
- A project is matched across machines by `git.RepoKey` of its remote, because `proj-` ids differ on every machine.
- `sync-to-git` is one way: it writes files to a branch of the repository, and they are never read back, so that wdx stays the only authority. Warden's repository export works this way.

Open: who may see what on the hub (accounts, teams, a project's members); how two revisions made at once on different machines are shown and resolved; what the ecosystem holds; whether plan definitions are one row or one per task.

## MCP and skills

To be looked into (the human, 2026-11-01). Agents are expected to spawn most children themselves, so how an agent uses wdx belongs with the local work, not the hub.

Proposed:

Every agent wdx starts knows wdx, in two layers, both from the binary running so that they cannot disagree with it:

1. **A short note at each launch**, added to the system prompt: who the agent is, when to use wdx rather than its own means, and where to read more. It lists no commands. An ACA hands it over in its own way (`Launch` gets the text; Claude Code takes `--append-system-prompt`, which applies on `--resume` too and is not lost when a conversation is compacted). docs/ACA.md records each. Built on 2026-11-03, approved by the human: `agent.LaunchNote`, the same for every agent and every start, names `WDX_AGENT_ID` rather than the id, and `wdx help agents` as its one command. Claude Code gets it with `--append-system-prompt` at every start. Since 2.1.317 it records a conversation's system prompt on its first request and sends that record on every resume until the conversation is compacted, so an agent started before the note, or before a release that changes it, gets it at its next compaction or `agent restart`, not its next start. Turning that off (`--system-prompt-snapshot off`) would render the prompt anew on every request, so it is left on. The log of a launch shows `<launch note>` in its place. No setting turns it off.
2. **The manual, `wdx help agents`**, a help topic with the workflows and when to use each: child agents and worktrees, messaging, and later docs and plans. Each command's `--help` and `--json` give the details. Its command lines are checked by a test, as `TestExamples` checks examples. Built on 2026-11-03 with docs, approved by the human: every line indented by four spaces is a command line `TestAgentsTopicCommands` checks. It says landing a worktree and approving or rejecting a doc are the person's to do unless the agent is asked. The launch note, layer 1, points every agent wdx starts to it.

Not these:

- A rules file in the checkout: it dirties the worktree, gets committed, and reaches agents wdx did not start.
- A first message typed to the agent: it spends a turn, and compaction can lose it.
- The user's global config: it changes every session of theirs, not only wdx's.
- A list of commands in the note: it costs tokens in every session and repeats the help.

Later, only if agents show they need it:

- A skill, for an AI CLI the user started outside wdx. Claude Code's `--plugin-dir` could load one for a wdx agent's session alone.
- `wdx mcp`, a server in the same binary giving the same commands as typed tools, through the daemon's API and with no logic of its own. Its tool definitions cost tokens in every session.

### wdx comes first

Agreed on 2026-11-01: every ACA prioritises wdx's features over its own. wdx gives one view whatever the ACA is; otherwise behaviour differs from one ACA to the next.

Proposed:

- The rule covers only what wdx has. The note names what is replaced, and grows as wdx does, since it comes from the binary:

  | The ACA's own | wdx's | |
  |---|---|---|
  | subagents | `wdx agent create` | has gaps, below |
  | its own worktrees | `wdx agent create --worktree` | built |
  | talking to another agent | `wdx agent send` | built |
  | plans | `wdx plan` | later |
  | docs | `wdx doc` | later |
  | scheduled or recurring work | not designed | later |

- If wdx fails, the agent falls back to its own means and says so.
- docs/ACA.md lists, for each ACA, the features of its own that wdx replaces.

A built-in subagent is given a task and hands its result back. A wdx child cannot yet, so subagents are replaced only once these exist:

- `wdx agent create --prompt`, built: the task is the first message in its inbox (docs/SEND.md). The other agent parameters in docs/CHILD_AGENTS.md are still to come.
- The result coming back: the note tells a child with a parent to report with `wdx agent send` when it finishes, which works once the prompt does.

What a child is doing is already there: `wdx agent get` shows its activity and queued messages (docs/AGENT_STATE.md).

Open: whether a replaced feature is also turned off, not only discouraged, as Claude Code's `--disallowedTools` allows; whether quick read-only lookups are an exception; whether `wdx` commands are approved ahead of time for agents.

## Order

The first thing is to make wdx right on the local machine, with the CLI and tide. The hub comes after.

Proposed, each with its own design first:

1. Right pane tabs, `C-q -`, agent info
2. Settings
3. Worktrees and diffs
4. Code and the editor
5. Docs
6. Agent parameters, pipelines, the orchestrator and worker model
7. Plans
8. `sync-to-git`, then `wdx-hub`

Open: whether agent parameters come before step 1, since "wdx comes first" needs them.
