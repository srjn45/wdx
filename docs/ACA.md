# Where wdx is specific to an ACA

Every place where wdx depends on which AI CLI an agent runs. Adding an ACA means going through this list. A change that adds, moves or removes such a place updates this file in the same commit.

## Behind `AiCodingAgent`

An ACA cannot compile without these. The places named are Claude Code's; Antigravity's are in `internal/aca/antigravity.go`.

| What | Where | Claude Code | Antigravity |
|---|---|---|---|
| Its id in the database and the API | `ID()`, `internal/aca/claudecode.go` | `claude_code` | `antigravity` |
| Its program, which says it is installed | `Program()`, `internal/aca/claudecode.go`; looked for through the login shell by `agent.ACAs` and `wdx deps list` | `claude` | `agy` |
| The command that starts or resumes a session | `Command()`, `internal/aca/claudecode.go` | `claude --session-id <id>`, or `claude --resume <id>` once a transcript exists | `agy`: it names its own conversations and cannot be given one, so `Launch.SessionID` is not used and every start begins a new conversation; `--conversation <id>` resumes, not used yet (the wdx doc ACA_ANTIGRAVITY) |
| Where it keeps transcripts | `claudeCodeSessions`, `internal/aca/claudecode_backup.go` | the path its `SessionStart` hook tells, else `~/.claude/projects/*/<id>.jsonl` | none yet (`NoBackUp`) |
| Where it last worked | `Backup.Dir` from `BackUp()`, `internal/aca/claudecode_backup.go`; docs/TIDE_WORKTREES.md | the last `cwd` of a transcript's lines, not a subagent's | none yet (`NoBackUp`) |
| Keeping its conversation | `BackUp()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | copies the raw transcripts, renders `transcript.md`, follows `/clear` through a hook given with `--settings`, and puts back a transcript Claude Code deleted | none yet (`NoBackUp`) |
| Beginning a session of wdx's choosing | `BeginSession()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | a line in `sessions.jsonl` with `source` `wdx_restart`, which makes it the session `BackUp` says the agent is in, and which tells the brief that a restart began it | none yet (`NoBeginSession`) |
| The conversation in brief | `Brief()`, `internal/aca/claudecode_brief.go`; docs/AGENT_RESUME.md | reads `transcript.md`, so it knows `renderLine`'s format, from the latest compaction summary or the first request, no further back than a `/clear`. An ACA with no summary starts from the first request | none yet (`NoBrief`) |
| What runs outside its terminal | `Detached()`, `StopDetached()`, `internal/aca/claudecode_backup.go`; docs/AGENT_RESUME.md | sessions linked by `continued-in` in a transcript, as `claude agents --json` lists them, stopped by `claude stop <id>` through the login shell, by restarting, starting or removing the agent and closing or removing its project. Nothing runs when no transcript has a link | nothing (`NoDetached`) |
| Giving it wdx's launch note | `Command()` with `Launch.Note`, `internal/aca/claudecode.go`; the text is `LaunchNote`, `internal/agent/manager.go`; docs/IDE.md | `--append-system-prompt <note>`, at every start, new or resumed; a resumed conversation keeps the prompt it recorded until it is compacted (`--system-prompt-snapshot`, checked on 2.1.317) | `--add-dir <agent dir>/antigravity-note`, a folder holding only the note as `AGENTS.md`, which it reads as the rules of a workspace folder, written at every start; no note without an agent dir. It has no flag for the system prompt (checked on 1.5.0) |
| Its model and permissions, kept for every start | `Command()` with `Launch.Model` and `Launch.Permissions`, `internal/aca/claudecode.go`, and the settings file, `claudeCodeWriteSettings`, `internal/aca/claudecode_backup.go`; docs/AGENT.md | `--model <model>`; `full` is `--permission-mode bypassPermissions`, with `skipDangerousModePermissionPrompt` and `sandbox.enabled: false` in the `--settings` file; `ask` passes nothing | `--model <model>`; `full` is `--dangerously-skip-permissions`, no `--sandbox`, and `enableTerminalSandbox: false` in `--settings`; `ask` passes nothing. `--settings`, inline JSON for that session only, also holds `queuedMessages: queue` and `trustedWorkspaces: [<dir>]`, so its question whether to trust a new folder does not stop the agent |
| The models it offers | `Models()`, `internal/aca/claudecode.go`; served as `GET /v1/acas/{aca}/models`, `wdx agent models` and `--model`'s completion | fixed in wdx: its aliases `fable`, `opus`, `sonnet`, `haiku`, none its default, as Claude Code has no command that lists them | what `agy models` lists, an id and a name between a tab per line, run through the login shell; none its default |
| Giving it a message | `Deliver()`, `internal/aca/claudecode.go`; docs/SEND.md | bracketed paste, about 150 ms, then Enter as a separate key; a paste-end marker in the message is taken out | as Claude Code, a copy until the ACAs share it; it needed no wait on 1.5.0 |
| Stopping its turn | `Interrupt()`, `internal/aca/claudecode.go`; docs/SEND.md | Esc | Esc; Ctrl+C would end it |
| Reading its activity, draft and model from its screen | `ScreenParser()`, `ClaudeCodeScreenParser`, `internal/aca/claudecode.go`; docs/AGENT_STATE.md | the footer rules, the banner and `/model` lines; its fixes reach a running agent when it is relaunched | none yet (`NoScreenParser`) |

An ACA that has not written a method yet uses a stand-in from `internal/aca`, embedded in its type and dropped once it writes the method (the wdx doc ACA_SELECT):

| Stand-in | For | Answers | What its callers then do |
|---|---|---|---|
| `NoScreenParser` | `ScreenParser()` | `unknown` with an input box | the motion fallback decides the activity, and messages are delivered on `idle`; no draft, turn or model |
| `NoBackUp` | `BackUp()` | `ErrNotSupported` | starts go on and the agent's record is still written, logged at debug; `agent transcript` fails with `aca_unsupported` |
| `NoBeginSession` | `BeginSession()` | `ErrNotSupported`; `CanBeginSession` is false | `agent restart` fails with `aca_unsupported` before anything stops |
| `NoBrief` | `Brief()` | `ErrNotSupported` | `agent transcript --brief` fails with `aca_unsupported` |
| `NoDetached` | `Detached()`, `StopDetached()` | nothing | start, restart, remove and closing a project stop nothing outside the terminal |

## Outside `AiCodingAgent`

Code that names one ACA, or assumes what they all do.

| What | Where | Needed for another ACA |
|---|---|---|
| Every ACA wdx knows, in the order they are offered, and the one place one is made | `known`, read through `All`, `IDs`, `ByID` and `Default`, `internal/aca/aca.go` | one line in `known`: the daemon, the holder, the settings and the deps rows are built from it |
| Which ACAs new agents can run: installed and enabled, the first the default | `agent.ACAs`, `internal/agent/acas.go`, given `daemon.Options.ACAs` (`aca.All()` by default) and the `aca.<id>.enabled` settings; served as `GET /v1/acas`, `wdx agent acas`, `--aca`'s completion and tide's form | nothing |
| Whether one is enabled | `aca.<id>.enabled`, made per ACA by `acaSettings`, `internal/config/config.go`; docs/SETTINGS.md | nothing |
| Whether one is installed, in the CLI | `acaDependencies`, `internal/cli/deps.go`; docs/DEPS.md | nothing |
| wdx chooses the session id before the first start, as a UUID | `newSessionID`, `internal/agent/manager.go` | an ACA that names its own sessions cannot be given one: Antigravity's `Command` does not use it |
| The test ACA offers Claude Code's models | `Models()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| The test ACA reports Claude Code's id | `ID()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| The `aca` field's example | `Agent.aca`, `api/openapi.yaml` | nothing; only an example |
| Every ACA a holder knows, by id | `ByID`, `internal/aca/aca.go`, called by the hidden `wdx holder` command in `internal/cli/holder.go`: a holder is its own process, told `--aca <id>` | nothing: `ByID` reads `known`; tests hand the daemon theirs through `Options.ACAs` |
| The test ACA's parser and delivering are Claude Code's, as a holder goes by the id | `ScreenParser()`, `Deliver()`, `Interrupt()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| An ACA queues what is typed during a turn, so the `inboxReader` delivers on `working`; checked on Claude Code 2.1.316, and on Antigravity 1.5.0, whose `queuedMessages: queue` wdx pins | `nextAction`, `internal/holder/inbox.go`; docs/SEND.md | an ACA that does not queue needs a way to say to hold while working |
| A message to an ACA its holder does not know is typed as into a shell | `newHolderCmd`, `internal/cli/holder.go`; docs/SEND.md | nothing: the daemon starts a holder from its own binary with its agent's ACA, so a holder is never older than its ACA |
| The waits of the motion fallback, chosen on recordings of Claude Code, though the code knows no ACA | `StillTime`, `EchoTime`, `ReadMoment`, `internal/screen/motion.go`; docs/AGENT_STATE.md | check them on recordings of it, and make them per ACA if they differ |
| The test ACA's backup copies a file it is given | `BackUp()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| The test ACA's brief is Claude Code's, on what a test writes | `Brief()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| A second test ACA with only stand-ins, and a look that finds every program but one | `acatest.Bare`, `acatest.LookProgram`, `internal/aca/acatest/acatest.go` | nothing |
| A session can be resumed from a directory other than the one it began in, so a relaunch runs where the agent last worked; checked on Claude Code 2.1.316 | `runDir`, `internal/agent/manager.go`; docs/AGENT_RESUME.md | an ACA that looks by directory needs to be started where the session began |
| The first message names `agent transcript --brief` and a restart's session is a UUID wdx chooses | `firstMessage`, `Restart`, `internal/agent/manager.go` | an ACA that names its own sessions, as above |
| Recordings of its screens, and the scripts that make them | `internal/aca/testdata/claude_code/` | its own recordings |
| Tests that list every setting and every dependency, so name each ACA's | `TestFileSettings`, `TestChoicesAreAccepted`, `internal/config/config_test.go`; `TestDepsList`, `internal/cli/deps_test.go` | its `aca.<id>.enabled` and its program's row |
| What `agy models` printed on 1.5.0, which the tests parse instead of running it | `internal/aca/testdata/antigravity/models.txt` | its own sample |

## Not landed yet

Move a row up when it reaches `main`.

| What | Where | Status |
|---|---|---|
