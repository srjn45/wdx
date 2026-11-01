# Keeping an agent's ACA running

Status: an agent's history is built for Claude Code. Resuming an ACA that ended is decided and not built.

## Resuming an ACA that ended

Decided by the human on 2026-11-01.

- **An ACA that ends is resumed by the daemon,** however it ended, `/exit` and `kill` included. The holder runs the ACA through `exec`, so no shell stays behind: the ACA's end is the terminal's, and the kernel tells the holder. `ScreenParser` does not change.
- **A resume is a relaunch:** a new holder, in the same session. Keeping a shell alive and typing the command into it was considered and rejected: a wrong reading would send the command to a live ACA as a prompt, and a message could run as a shell command.
- **`Launch` gets `Resume bool`.** Create passes false, which always starts the session. Every relaunch passes true, which resumes when the ACA has something to resume and otherwise starts the session, as `--resume` of a session with no transcript fails.
- **A failing resume ends in `failed`,** with `error` saying why.

Proposed, not yet confirmed:

- Three attempts, 1 s, 5 s and 30 s apart; a run that stays up 60 s resets the count, as does a human's `agent start`. The count is in the daemon's memory.
- `stopped`, a closed project and a removal are never resumed; `agent start` during a wait cancels it. Shell terminals do not change.
- `feat!`, since it reverses "never relaunched automatically" in docs/AGENT.md.

Open:

- Whether a `lost` agent is resumed. Recommended: no, or a reboot restarts every agent at once with nobody watching.
- Whether queued messages survive. They live in the holder, so a relaunch probably loses them. Recommended: document it.

Consequences to document:

- With no `agent stop`, only `agent remove` or closing the project ends an agent. `/exit` brings it back.
- `/exit` inside `wdx agent attach` ends that attach.
- A resume does not continue the turn: the ACA waits at its input box.
- `--resume` goes back to before a `/clear` (docs/AGENT_STATE.md).

Parked on 2026-11-01 until an agent's history is built.

## No `wdx agent restart`

Decided by the human on 2026-11-01: `agent start` already relaunches an agent in its session. Rotating, handing off and switching ACA are not being built now.

## Keeping an agent's history

Asked for by the human on 2026-11-01: wdx keeps a backup of an agent's conversation, readable and greppable at any time, until the agent is removed, and for 7 days after. Each ACA keeps its history in its own way, so the backup is ACA-specific, and only Claude Code's is built. `transcript.md` only ever grows, across compactions and clears, so that a later restart can have an agent read its last lines, and older ones if it needs them. Decided by the human: an agent started again goes on with its latest conversation.

```
~/.wdx/agents/<agent id>/
  agent.json                         whose it is, kept current, as the row goes before it does
  sessions.jsonl                     every session of the agent, told by Claude's hook
  claude-settings.json               that hook, given with --settings
  rendered.json                      how far transcript.md has got
  transcripts/<session id>.jsonl     Claude's own bytes
  transcripts/<session id>/          its subagents/ and tool-results/
  transcript.md                      all of it as text, oldest first: what wdx reads and grep finds

~/.wdx/trash/agents/<agent id>/      the same, with removed.json, for 7 days
```

- **ACA-specific.** `AiCodingAgent` has `BackUp`, which brings an agent's directory up to date and returns the session it is in now, and `Launch` has that directory as `BackupDir`. How is the ACA's business. A format change costs a fix to `transcript.md`'s rendering, which can be redone from the raw files, and never the history.
- **Following sessions.** `Command` adds `--settings` with a `SessionStart` hook that appends what Claude sends to `sessions.jsonl`. It needs no daemon, and gives the exact path, so a `CLAUDE_CONFIG_DIR` or a long directory name stops mattering. An agent started before this, or with `disableAllHooks`, has only its first session.
- **When.** The daemon brings the backup up to date, copying only new bytes, when an agent goes `idle` or `waiting`, when it exits, before a start, when its transcript is read, once its row is gone, and at startup. One at a time, across agents, and never in the way of the holder's connection.
- **The latest conversation.** `BackUp` returns the session of the last `SessionStart`, a start goes on with it, and `agents.session_id` follows it.
- **A file that shrank or was replaced** is kept and copied again beside it, as Claude documents setting transcripts aside.
- **`transcript.md`.** Appended to, with a heading per session and per message: messages, thinking, tool calls and their results, in full, with escape codes stripped. Request records, other metadata and what subagents said are left out, and stay in the raw files. A line or content block it cannot read is skipped, counted and logged. A replaced transcript is rendered again from its start, so its text appears twice rather than go missing. `<home>/agents/` is 0700 and its files 0600, as transcripts hold whatever tools read.
- **Reading.** `wdx agent transcript (--id | --name) [project flags] [--tail <n> | [--from <n>] [--count <n>] | --path]` and `GET /v1/agents/{agent}/transcript?tail=` or `?from=&count=`, lines as with a terminal's history, whose name is taken, plus the file's `path`. The text stays out of the logs. An agent's scrollback was being logged, unlike a terminal's, and no longer is.
- **Staying resumable.** With Claude's file gone and a copy kept, `Command` puts it back at the recorded path and resumes, in place of silently starting an empty session.
- **Removal.** When the agent's row goes (`agent remove`, a tombstone's last child, `project remove`), its directory is brought up to date and moved to the trash with `removed.json`: id, name, project, dir, ACA, sessions, removal time. The row is gone, so that is all that says what it was. Claude's own files stay. No command reads the trash yet.
- **The trash** is deleted 7 days after removal, at daemon startup, on each removal and by one timer armed for the next expiry. Every removal ends the same way: a sweep moves any directory with no row, so a crash between the two is finished at startup.

### Checked on the real thing

Claude Code 2.1.316 on 2026-11-01, driven under tmux with Haiku:

- **The hook works through `--settings`,** from a file: `SessionStart` `startup`, `compact`, then `clear` with the new session's id and path. `--safe-mode` fires no hooks, so recordings that test it cannot use it. A project's own `SessionStart` hook ran beside it, so `--settings` adds to hooks rather than replacing them; a user-level hook was not tried. `--settings` as inline JSON was not reached, so wdx gives a file. `disableAllHooks` is not tried, and an agent under it would keep only its first session.
- **`/clear` leaves the old file and starts another;** the old one stops growing.
- **Claude appends in place:** the same inode through turns, `/compact`, `/clear` and a resume.
- **Putting a file back resumes it.** With the file and session directory deleted and a copy restored at the path, `--resume` answered from the conversation, and went on appending to that file.
- **Transcripts are mostly metadata:** the first turn's file is 366 KB, a later turn adds 6 KB, a compact 87 KB and a resume's first turn 298 KB, which is why `transcript.md` is what is read.

## What each ACA keeps

Researched on 2026-11-01 on this machine's real files, looking at their structure and not their text, and from `--help`. Versions: Claude Code 2.1.316, Codex 0.163.0, Cursor 2026.10.19, Antigravity 1.4.7. One recent session of each was sampled for its record types, so a type not listed may still exist.

| | Claude Code | Codex | Cursor | Antigravity |
|---|---|---|---|---|
| Resume by id | `--resume <id>` | `codex resume <id>` | `--resume <id>` | `--conversation <id>` |
| Fork | `--resume <id> --fork-session` | `codex fork` | none in `--help` | none in `--help` |
| wdx can choose the id | yes, `--session-id` | none in `--help` | `create-chat` returns one | none in `--help` |
| Non-TUI mode | `-p`, stream-json in and out | `codex exec --json` | `-p`, stream-json out | `-p`, stream-json in and out |
| What it resumes from | `~/.claude/projects/<dir>/<id>.jsonl` | `~/.codex/sessions/YYYY/MM/DD/rollout-*-<id>.jsonl` | `~/.cursor/chats/<hash>/<id>/store.db`, binary and JSON blobs, with a `blobEncryptionKey` in its `meta` | `~/.gemini/antigravity-cli/conversations/<id>.db`, blobs in a `steps` table |
| Readable record | the same file: messages, `tool_use`, `tool_result`, `thinking` | the same file: messages, reasoning, tool calls and output, with `cwd` in its first line | `~/.cursor/projects/<dir>/agent-transcripts/<id>/<id>.jsonl`: text and `tool_use`, no results in the sample; 616 of 622 chats have one | `brain/<id>/.system_generated/logs/transcript_full.jsonl`: typed steps; 496 of 502 conversations have one |
| Format promised | no: Claude's docs call it internal, changing between versions | none found; `migrate-rollouts` shows it is moving | none found | none found |

Claude Code deletes transcripts after 30 days by default (`cleanupPeriodDays`), and then `Command` would silently start the session afresh. Its docs also describe a `Resume this conversation?` dialog on long conversations after a long break, not yet recorded, which would stop an unattended resume at a prompt.

### The ways to reach history

| Way | Verdict |
|---|---|
| The ACA's own resume | enough for a restart on the same ACA, on all four, with nothing to read |
| Reading the ACA's files | possible on all four, read-only, for showing history or handing it to another ACA. Each format can break on any release, so it needs one reader per ACA, tests on recordings and a failure that harms nothing |
| Non-TUI mode | rejected for now: attach, `ScreenParser`, delivering and permission prompts all rely on the TUI, and wdx would have to rebuild it. Also captures nothing of existing sessions |
| The holder's own history | already kept, but it is what scrolled off the main screen as rendered text, nothing in fullscreen mode. For a human's scrollback, not for resuming |
| Asking the ACA, as `claude -p --resume <id> "summarize"` | the documented way on Claude Code, and costs tokens; a handoff summary, not a record |
