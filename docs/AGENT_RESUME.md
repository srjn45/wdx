# Keeping an agent's ACA running

Status: an agent's history and restarting an agent in a new session are built for Claude Code. Resuming an ACA that ended is decided and not built.

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
- Whether queued messages survive. They live in the holder today, and the queue moving to the daemon's database will keep them across a relaunch; see "Messages across a restart".

Consequences to document:

- With no `agent stop`, only `agent remove` or closing the project ends an agent. `/exit` brings it back.
- `/exit` inside `wdx agent attach` ends that attach.
- A resume does not continue the turn: the ACA waits at its input box.
- `--resume` goes back to before a `/clear` (docs/AGENT_STATE.md).

Parked on 2026-11-01 until an agent's history is built.

## Restarting an agent in a new session

Decided by the human on 2026-11-01; built. `agent start` goes on with the same conversation, whatever its size. `wdx agent restart` keeps the agent and begins a new session, in which the agent reads only what it needs of its history. This replaces the earlier decision to have no restart command. Rotating, handing off and switching ACA are still not being built.

- **The steps.** The ACA is stopped, the backup is brought up to date, a new session starts under the same agent id, and its first message tells the agent to read `wdx agent transcript --brief`, and older lines if it needs them. `transcript.md` goes on growing, with a heading for the new session.
- **Who asks.** A person or a script: `wdx agent restart (--id | --name) [project flags] [--force]`, and a key in tide with a confirmation. Nothing restarts an agent by itself, though auto-resume could later fall back on it when a resume keeps failing.
- **While the agent is working** it is refused with a conflict, unless `--force` is given, as a stop in the middle of a turn can leave work half done.
- **Where.** In the worktree the agent was last in if it still exists, or else its own directory, which the first message then says.

Decided by the human when building it, on 2026-11-01:

- **What counts as working.** Without `--force`, anything but `idle` with an empty input box is refused with `agent_busy`, a conflict whose hint names `--force`: `working`, `waiting`, `unknown` and a draft. There is no prompt. An agent that is not running just restarts.
- **Every relaunch runs in the last directory.** `agent start` and opening a project too: the directory the agent last worked in if it still exists, else its own. A conversation that thinks it is in a worktree must not wake up in another checkout.
- **Carried `--instant` messages become general,** so that they cannot interrupt the agent reading its brief. The first message is instant, so that a later `--instant` cannot overtake it during startup. One sent after that can still interrupt the brief: the sender asked for it.
- **A second restart replaces the first message** of a restart whose launch failed. Its id starts `msg-restart-`, and a restart deletes any still undelivered.
- **Names.** `POST /v1/agents/{agent}/restart` with `{force}`, the code `agent_busy`, the result `restarted`, and `BeginSession` and `Brief` on `AiCodingAgent`.
- **In tide,** `C-q R`, for a new session (`C-q n` until 2026-11-03). The confirmation says what the agent is doing, and `y` restarts it with force.
- **`--brief`** answers in the shape of `agent transcript`: `from` is the line of `transcript.md` it starts at, and `lines` are the brief's own. It excludes `--tail`, `--from`, `--count` and `--path`.

### The brief

Tool calls and results are 73% and 90% of the two transcripts measured, so the last lines would be mostly file dumps and test output, and would miss the original request. `--brief` gives the conversation without them:

- **It starts at** the latest compaction summary, or the first request when there is none.
- **It goes on with** every message since, without thinking, and each tool call as one line: its name, a short form of its input, whether it failed, and its line in `transcript.md`, which `--from` reads in full.
- **`/clear` is respected:** the brief looks no further back than the session a `/clear` began.
- **A restart is not a starting point.** It looks back through sessions a restart began, which therefore have to be marked, or a second restart would start from the first one's message and lose the original request, since the brief the agent read was a tool result.
- **It has a size limit,** 64 KB to begin with. Over it, the starting point stays, the oldest messages after it go, and a line says which lines of `transcript.md` hold them.

Measured on 2026-11-01, from the latest compaction summary on:

| Agent | `transcript.md` | Everything since | The brief |
|---|---|---|---|
| `frosty-seal` | 892 KB | 55 KB | 16 KB, about 4,000 tokens |
| `worktree-feat` | 1,295 KB | 464 KB | 39 KB, about 10,000 tokens |

A compaction summary is written by the ACA and leaves things out, which is why the rest of the file stays one command away. An ACA with no such summary starts from the first request.

### Messages across a restart

A restart builds on the daemon's queue (docs/SEND.md). In one transaction with the new session on the agent's row:

- **Pending messages go on to the new session,** with no holder and as general. Elsewhere a change of session holds them, so that an old "commit" cannot arrive out of nowhere; here a human asked for it and it is the same agent going on. `held` ones stay held, and a review's comments stay sent.
- **The first message goes ahead of them,** instant and created just before the oldest, so that it leads in both the holder's queues and in the order they are sent in. It waits at a trust prompt like any other. If the launch fails it stays, and the agent's next start delivers it; a second restart deletes it and writes its own.
- **A message typed but not yet reported** when the ACA stopped arrives again in the new session, which is the queue's at-least-once delivery. A message Claude Code had queued inside itself counts as delivered and goes with the old session.

The order in which it happens matters. The backup is brought up to date once the ACA is gone, so that nothing is missed. The transaction comes before `BeginSession`, whose line in `sessions.jsonl` is what makes the session stick: a crash between leaves the agent in its old session with a message that says otherwise, which is better than a new session with nothing to tell it. All of it is under the backups' lock, or a backup between would put the row back.

### Known limits

- What the old session held only in memory goes with it: background processes, a mode or approval given for the session and a `/model` choice for the session; see below.
- A fork of the session is not followed by the backup, so work done in one is not in the brief. A conversation Claude Code moved to a background session is followed; see below.
- An agent started before the backup existed gets its session hook from the restart, or from any later start.
- **Reading `transcript.md`.** Message text is not fenced, so the brief goes by strict headings. A quoted heading can split a message, and a compaction heading is only believed if it is not older than the one before it, which a quotation is. A message quoting `### Result` over an open fence while a tool call is unanswered could hide text from the brief up to the next such fence, though not from the file. Results are matched to calls in order, so parallel calls could show the wrong one as failed.
- **Thinking.** It has no end but the next heading in lines rendered before `### Text` was added, so text after thinking in the same message is not in the brief of those lines. None of the 63 text blocks of the two backups measured on 2026-11-01 was such text.

### A conversation Claude Code moves to the background

Found on 2026-11-01, when `worktree-feat`'s brief ended seven hours before its work did. Claude Code 2.1.316 can move a running conversation to a background session under its own daemon (`claude daemon run`), outside the holder: a new process with `--session-id <new> --fork-session --resume <old transcript>`. What triggers it was not established; in the case examined it came at an interrupted tool call. In the old transcript it writes `{"type":"continued-in","continuedInSessionId":"<new>"}`, the line Claude Code itself goes by, and the new transcript starts with copies of the old one's last messages, from the latest compaction, under the same `uuid`s.

Decided by the human on 2026-11-01:

- **The backup follows the link.** It is read from the transcript, whether or not the hook saw anything. The new session gets a line in `sessions.jsonl` with `source` `wdx_continued` and `continued_from`, and becomes the session the agent is in only if the one it came from still was: a link found after a restart does not take the agent back. A backup from before this finds its links on its next run, as how far it has looked is kept apart from how far it has rendered. Such late work comes after what was rendered since, as `transcript.md` only grows, so until the agent has written more it can crowd out what came after it in the brief; and it does not move where the agent last worked.
- **Copied messages are skipped** by `uuid`, up to the first line of the new session's own, or they would be in `transcript.md` twice.
- **What ends an agent stops the background session,** with `claude stop`, and says so: `stopped_sessions` in the answer, a line in the CLI's text and tide's status line. So do `agent restart`, which leaves the old conversation, `agent remove`, and closing or removing the project. `agent start` does too, before it starts, as `claude --resume` is said by its help to work on such a session only once it is stopped. It stops only sessions linked from this agent's transcripts, never another job `claude agents` lists. One at work is refused first, since stopping it can lose the work: `agent_busy` from `agent restart` without `--force` and from `agent start`, which has no such flag; `agent_running` from `agent remove` and `project_in_use` from closing or removing the project, without `--stop-running`, the question each already asks. One that will not stop is logged and the restart goes on. Looking for them runs nothing when no transcript has a link.

Checked on Claude Code 2.1.316, on a real job and through a login shell: `Detached` found it by `claude agents --json`, which lists `kind` `background`, a `pid` while it runs, and `status`, and `StopDetached` ended it. A job that has only just been asked for may have no `pid` for a moment, and is then not seen. `worktree-feat`'s real transcripts, in a scratch copy, gave a brief that reaches its last work at 19:55 and kept its session and directory. Moving a conversation itself could not be tried, as what triggers it is not known, so whether the hook fires in such a session is still open; following the link does not depend on it.

## An ACA without these yet

An ACA that has not written `BackUp`, `BeginSession` or `Brief` embeds `aca.NoBackUp`, `aca.NoBeginSession` and `aca.NoBrief`, which answer `aca.ErrNotSupported`, and `aca.NoDetached`, which finds nothing. Its agents start and are removed as any other, with no backup kept, logged at debug. `agent transcript` and `--brief` fail with `aca_unsupported` rather than return nothing, and `agent restart` with it before anything stops, as `aca.CanBeginSession` says (the wdx doc ACA_SELECT).

## Keeping an agent's history

Asked for by the human on 2026-11-01: wdx keeps a backup of an agent's conversation, readable and greppable at any time, until the agent is removed, and for 7 days after. Each ACA keeps its history in its own way, so the backup is ACA-specific, and only Claude Code's is built. `transcript.md` only ever grows, across compactions and clears, so that a later restart can have an agent read its last lines, and older ones if it needs them. Decided by the human: an agent started again goes on with its latest conversation. docs/TRANSCRIPT_USES.md lists what the history could be used for.

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
- **`transcript.md`.** Appended to, with a heading per session and per message: messages, thinking, tool calls and their results, in full, with escape codes stripped. Text after thinking in the same message has a `### Text` heading, as the brief needs to know where the thinking ends. Request records, other metadata and what subagents said are left out, and stay in the raw files. A line or content block it cannot read is skipped, counted and logged. A replaced transcript is rendered again from its start, so its text appears twice rather than go missing. `<home>/agents/` is 0700 and its files 0600, as transcripts hold whatever tools read.
- **Reading.** `wdx agent transcript (--id | --name) [project flags] [--tail <n> | [--from <n>] [--count <n>] | --path]` and `GET /v1/agents/{agent}/transcript?tail=` or `?from=&count=`, lines as with a terminal's history, whose name is taken, plus the file's `path`. `--brief`, `?brief=true`, gives the brief instead. The text stays out of the logs. An agent's scrollback was being logged, unlike a terminal's, and no longer is.
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

Restart, on Claude Code 2.1.316 on 2026-11-01, driven with `wdx agent attach` under tmux, on a home and daemon of its own, with Haiku, with the user's settings:

| Check | What happened |
|---|---|
| Background process across a restart | Gone: a `sleep` run by Claude's background Bash ended with the stop. The new session has no sign of it but the one line in its brief |
| A mode approved for the session | Gone. `Yes, and switch to accept edits … for this session` made the next Write ask nothing; after the restart it asks again, and the footer is back to manual mode. The Bash prompt's offer, `always allow access to <dir> from this project`, was no such approval: the next `touch` asked again in the same session |
| `/model` for the session (`s` in its menu) | Gone: `agent get` said Sonnet 5.5, and Haiku 5.5, what it was launched with, after the restart. A choice saved with Enter is in Claude's settings, so it stays (docs/AGENT_STATE.md); not tried here, as it changes them for everyone |
| A restarted agent reads its brief and can say what it was doing | Yes: told only the first message, it ran the command, and gave the codeword and the task from its first turn when asked |
| Pending messages arrive after the first message | Yes: a general and an instant one held behind a draft, restart with `--force`, then the first message, then both in the order sent, in one turn after the brief was read |
| Restart in a worktree, `/exit`, `agent start` | Resumes the new session, in the worktree |
| `agent start` in a worktree, of a session begun in the checkout | Resumes it. **`--resume` finds a session from any directory,** in `-p` and in the terminal UI, and goes on appending to its first file with `cwd` the new one. So where the session began need not be recorded |
| Restart, `/clear`, restart | The brief starts at the `/clear`: nothing from before it, and the agent knew only what was said since |
| `/compact` | The brief starts at the summary |
| Restart of an agent from before the hook, made by leaving out `--settings` | Sessions followed afterwards: the old session listed, then the restart's, then `startup`, and a later `/clear` moved `session_id` |
| Worktree removed | Starts in the agent's own dir, and the first message says so |
| Trust prompt | In a worktree under a trusted checkout, none. In a directory never trusted, a restart is refused as `waiting` without `--force`, and with it the first message waits (`queued` 1) until a human answers the prompt with `wdx agent attach`, then goes in. Nothing else can answer it |
| `cd` out of the project | Not followed: Claude resets the shell, and `cwd` stays. So the last directory is inside what it may work in |

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
