# Where wdx is specific to an ACA

Every place where wdx depends on which AI CLI an agent runs. Adding an ACA means going through this list. A change that adds, moves or removes such a place updates this file in the same commit.

## Behind `AiCodingAgent`

An ACA cannot compile without these.

| What | Where | Claude Code |
|---|---|---|
| Its id in the database and the API | `ID()`, `internal/aca/claudecode.go` | `claude_code` |
| The command that starts or resumes a session | `Command()`, `internal/aca/claudecode.go` | `claude --session-id <id>`, or `claude --resume <id>` once a transcript exists |
| Where it keeps transcripts | `Command()`, `internal/aca/claudecode.go` | `~/.claude/projects/*/<id>.jsonl`; a `CLAUDE_CONFIG_DIR` set in the user's rc file is not seen |
| Giving it a message | `Deliver()`, `internal/aca/claudecode.go`; docs/SEND.md | bracketed paste, about 150 ms, then Enter as a separate key; a paste-end marker in the message is taken out |
| Stopping its turn | `Interrupt()`, `internal/aca/claudecode.go`; docs/SEND.md | Esc |
| Reading its activity and draft from its screen | `ScreenParser()`, `ClaudeCodeScreenParser`, `internal/aca/claudecode.go`; docs/AGENT_STATE.md | the footer rules; its fixes reach a running agent when it is relaunched |

## Outside `AiCodingAgent`

Code that names one ACA, or assumes what they all do.

| What | Where | Needed for another ACA |
|---|---|---|
| The ACA new agents run, and the only one known | `Options.ACA` and its default, `internal/daemon/daemon.go` | a way to choose one, and a list of all of them |
| wdx chooses the session id before the first start, as a UUID | `newSessionID`, `internal/agent/manager.go` | an ACA that names its own sessions cannot be given one |
| The test ACA reports Claude Code's id | `ID()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| Help says "Claude Code for now" | `newAgentCmd`'s `Long`, `internal/cli/agent.go` | the words |
| The `aca` field's example | `Agent.aca`, `api/openapi.yaml` | nothing; only an example |
| Every ACA a holder knows, by id | `ByID`, `internal/aca/aca.go`, called by the hidden `wdx holder` command in `internal/cli/holder.go`: a holder is its own process, told `--aca <id>` | add it to `ByID` as well as to the daemon's list; `agent.Launches` keeps its own, through which tests hand in theirs |
| The test ACA's parser and delivering are Claude Code's, as a holder goes by the id | `ScreenParser()`, `Deliver()`, `Interrupt()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| An ACA queues what is typed during a turn, so the `inboxReader` delivers on `working`; checked on Claude Code 2.1.316 | `nextAction`, `internal/holder/inbox.go`; docs/SEND.md | an ACA that does not queue needs a way to say to hold while working |
| A message to an ACA its holder does not know is typed as into a shell | `newHolderCmd`, `internal/cli/holder.go`; docs/SEND.md | settle this before a holder can be older than an ACA |
| The waits of the motion fallback, chosen on recordings of Claude Code, though the code knows no ACA | `StillTime`, `EchoTime`, `ReadMoment`, `internal/screen/motion.go`; docs/AGENT_STATE.md | check them on recordings of it, and make them per ACA if they differ |
| Recordings of its screens, and the scripts that make them | `internal/aca/testdata/claude_code/` | its own recordings |

## Not landed yet

Move a row up when it reaches `main`.

| What | Where | Status |
|---|---|---|
