# Where wdx is specific to an ACA

Every place where wdx depends on which AI CLI an agent runs. Adding an ACA means going through this list. A change that adds, moves or removes such a place updates this file in the same commit.

## Behind `AiCodingAgent`

An ACA cannot compile without these.

| What | Where | Claude Code |
|---|---|---|
| Its id in the database and the API | `ID()`, `internal/aca/claudecode.go` | `claude_code` |
| The command that starts or resumes a session | `Command()`, `internal/aca/claudecode.go` | `claude --session-id <id>`, or `claude --resume <id>` once a transcript exists |
| Where it keeps transcripts | `claudeCodeSessions`, `internal/aca/claudecode_backup.go` | the path its `SessionStart` hook tells, else `~/.claude/projects/*/<id>.jsonl` |
| Where it last worked | `Backup.Dir` from `BackUp()`, `internal/aca/claudecode_backup.go`; docs/TIDE_WORKTREES.md | the last `cwd` of a transcript's lines, not a subagent's |
| Keeping its conversation | `BackUp()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | copies the raw transcripts, renders `transcript.md`, follows `/clear` through a hook given with `--settings`, and puts back a transcript Claude Code deleted |
| Beginning a session of wdx's choosing | `BeginSession()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | a line in `sessions.jsonl` with `source` `wdx_restart`, which makes it the session `BackUp` says the agent is in, and which tells the brief that a restart began it |
| The conversation in brief | `Brief()`, `internal/aca/claudecode_brief.go`; docs/AGENT_RESUME.md | reads `transcript.md`, so it knows `renderLine`'s format, from the latest compaction summary or the first request, no further back than a `/clear`. An ACA with no summary starts from the first request |
| What runs outside its terminal | `Detached()`, `StopDetached()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | sessions linked by `continued-in` in a transcript, as `claude agents --json` lists them, stopped by `claude stop <id>` through the login shell, by restarting, starting or removing the agent and closing or removing its project. Nothing runs when no transcript has a link |
| Giving it wdx's launch note | `Command()` with `Launch.Note`, `internal/aca/claudecode.go`; the text is `LaunchNote`, `internal/agent/manager.go`; docs/IDE.md | `--append-system-prompt <note>`, at every start, new or resumed; a resumed conversation keeps the prompt it recorded until it is compacted (`--system-prompt-snapshot`, checked on 2.1.317) |
| Its model and permissions, kept for every start | `Command()` with `Launch.Model` and `Launch.Permissions`, `internal/aca/claudecode.go`, and the settings file, `claudeCodeWriteSettings`, `internal/aca/claudecode_backup.go`; docs/AGENT.md | `--model <model>`; `full` is `--permission-mode bypassPermissions`, with `skipDangerousModePermissionPrompt` and `sandbox.enabled: false` in the `--settings` file; `ask` passes nothing |
| Giving it a message | `Deliver()`, `internal/aca/claudecode.go`; docs/SEND.md | bracketed paste, about 150 ms, then Enter as a separate key; a paste-end marker in the message is taken out |
| Stopping its turn | `Interrupt()`, `internal/aca/claudecode.go`; docs/SEND.md | Esc |
| Reading its activity, draft and model from its screen | `ScreenParser()`, `ClaudeCodeScreenParser`, `internal/aca/claudecode.go`; docs/AGENT_STATE.md | the footer rules, the banner and `/model` lines; its fixes reach a running agent when it is relaunched |

## Outside `AiCodingAgent`

Code that names one ACA, or assumes what they all do.

| What | Where | Needed for another ACA |
|---|---|---|
| The ACA new agents run, and the only one known | `Options.ACA`, `internal/daemon/daemon.go`, by default `aca.Default()`, `internal/aca/aca.go` | a way to choose one, and a list of all of them |
| Help names Claude Code's model aliases | `agent create`'s `Long`, `internal/cli/agent.go` | the words, until the ACA lists its models |
| wdx chooses the session id before the first start, as a UUID | `newSessionID`, `internal/agent/manager.go` | an ACA that names its own sessions cannot be given one |
| The test ACA reports Claude Code's id | `ID()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| Help says "Claude Code for now" | `newAgentCmd`'s `Long`, `internal/cli/agent.go` | the words |
| The `aca` field's example | `Agent.aca`, `api/openapi.yaml` | nothing; only an example |
| Every ACA a holder knows, by id, and the one place one is made | `ByID`, `internal/aca/aca.go`, called by the hidden `wdx holder` command in `internal/cli/holder.go`: a holder is its own process, told `--aca <id>` | add it to `ByID` as well as to the daemon's list; `agent.Launches` keeps its own, through which tests hand in theirs |
| The test ACA's parser and delivering are Claude Code's, as a holder goes by the id | `ScreenParser()`, `Deliver()`, `Interrupt()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| An ACA queues what is typed during a turn, so the `inboxReader` delivers on `working`; checked on Claude Code 2.1.316 | `nextAction`, `internal/holder/inbox.go`; docs/SEND.md | an ACA that does not queue needs a way to say to hold while working |
| A message to an ACA its holder does not know is typed as into a shell | `newHolderCmd`, `internal/cli/holder.go`; docs/SEND.md | settle this before a holder can be older than an ACA |
| The waits of the motion fallback, chosen on recordings of Claude Code, though the code knows no ACA | `StillTime`, `EchoTime`, `ReadMoment`, `internal/screen/motion.go`; docs/AGENT_STATE.md | check them on recordings of it, and make them per ACA if they differ |
| The test ACA's backup copies a file it is given | `BackUp()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| The test ACA's brief is Claude Code's, on what a test writes | `Brief()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| A session can be resumed from a directory other than the one it began in, so a relaunch runs where the agent last worked; checked on Claude Code 2.1.316 | `runDir`, `internal/agent/manager.go`; docs/AGENT_RESUME.md | an ACA that looks by directory needs to be started where the session began |
| The first message names `agent transcript --brief` and a restart's session is a UUID wdx chooses | `firstMessage`, `Restart`, `internal/agent/manager.go` | an ACA that names its own sessions, as above |
| Recordings of its screens, and the scripts that make them | `internal/aca/testdata/claude_code/` | its own recordings |

## Not landed yet

Move a row up when it reaches `main`.

| What | Where | Status |
|---|---|---|
