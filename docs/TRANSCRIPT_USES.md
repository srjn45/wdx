# What an agent's history could be used for

Status: possible future enhancements, written down on 2026-11-01. Only restart (use 1) is built, as docs/AGENT_RESUME.md says, which also describes the history itself. None of the others is decided. Showing the working directory or worktree in the transcript is being added separately.

The order is a recommendation: restart first because it is nearly free, then the features only wdx can offer because it holds both a diff and the conversation behind it.

| # | Use | What it gives | What it needs |
|---|---|---|---|
| 1 | **Restart from the transcript** | An agent goes on in a fresh session after its ACA session is lost or too large, and could later go on in another ACA, as the transcript is plain text | `wdx agent restart` and `wdx agent transcript --brief` |
| 2 | **Why did this change** | From a worktree's change in tide's diff review, to the conversation that made it | Which worktree each message was in, and linking file edits to transcript lines |
| 3 | **A worktree's story** | Everything that happened on a worktree, across agents and sessions, as a timeline, a pull request description or a summary before landing | Which worktree each message was in; a summary also needs an LLM, a timeline does not |
| 4 | **Search across agents** | "Which agent touched the holder?", "why did we choose this?", removed agents included while they are in the trash | A command and a tide view; grep is enough to start. No command reads the trash yet |
| 5 | **Catch-up** | What an agent did while you were away: files edited, commands run, tests failed, questions asked | Reading the tool calls from the raw files; no LLM |
| 6 | **Agents reading each other's history** | A child reads its parent's reasoning, or a reviewer reads why the code was written that way, without being briefed | A command for agents, and a decision on who may read what |
| 7 | **Lessons for `AGENTS.md`** | Corrections repeated and failures that come back, as suggested rules or rows in `docs/FLAKES.md` | An LLM pass over the history, run by hand now and then |
| 8 | **Usage and cost** | Tokens per agent, worktree or feature | The raw files, as `transcript.md` leaves out request records. Per-agent numbers stay out of the metrics, whose labels never hold ids |
| 9 | **Audit** | Which commands ran and which files were read, above all by agents nobody was watching | The same reading of tool calls as catch-up |

## What to design around

- **Secrets.** A transcript holds whatever tools read. Its files are private, but sharing it, a pull request summary and an agent reading another's history all move that text somewhere else, so each needs a decision of its own.
- **What the backup misses.** What subagents said is only in the raw files, and a fork of a session, such as a Claude background session, is not followed at all. A worktree's story has holes until forks are followed.
- **An edit is not where the agent was.** The working directory says where an agent was, not which files it changed, and an agent can edit outside it. Uses 2 and 3 take the worktree from an edit's own file path, and fall back on the working directory.
- **One ACA.** Only Claude Code's history is backed up. For these uses not to depend on its format, `BackUp` could give a neutral list of events (an edit, a command, a message) that each ACA supplies in its own way.
