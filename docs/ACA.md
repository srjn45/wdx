# Where wdx is specific to an ACA

Every place where wdx depends on which AI CLI an agent runs. Adding an ACA means going through this list. A change that adds, moves or removes such a place updates this file in the same commit.

## Behind `AiCodingAgent`

An ACA cannot compile without these.

| What | Where | Claude Code |
|---|---|---|
| Its id in the database and the API | `ID()`, `internal/aca/claudecode.go` | `claude_code` |
| The command that starts or resumes a session | `Command()`, `internal/aca/claudecode.go` | `claude --session-id <id>`, or `claude --resume <id>` once a transcript exists |
| Where it keeps transcripts | `Command()`, `internal/aca/claudecode.go` | `~/.claude/projects/*/<id>.jsonl`; a `CLAUDE_CONFIG_DIR` set in the user's rc file is not seen |

## Outside `AiCodingAgent`

Code that names one ACA, or assumes what they all do.

| What | Where | Needed for another ACA |
|---|---|---|
| The ACA new agents run, and the only one known | `Options.ACA` and its default, `internal/daemon/daemon.go` | a way to choose one, and a list of all of them |
| wdx chooses the session id before the first start, as a UUID | `newSessionID`, `internal/agent/manager.go` | an ACA that names its own sessions cannot be given one |
| The test ACA reports Claude Code's id | `ID()`, `internal/aca/acatest/acatest.go` | nothing while one ACA exists |
| Help says "Claude Code for now" | `newAgentCmd`'s `Long`, `internal/cli/agent.go` | the words |
| The `aca` field's example | `Agent.aca`, `api/openapi.yaml` | nothing; only an example |
| Reading its activity and draft from its screen, not yet called | `ClaudeCodeScreenParser`, `internal/aca/claudecode.go`; docs/AGENT_STATE.md | its own `ScreenParser`; `AiCodingAgent` gets a method for it with the holder's `watchScreen` |
| Recordings of its screens, and the scripts that make them | `internal/aca/testdata/claude_code/` | its own recordings |

## Not landed yet

Move a row up when it reaches `main`.

| What | Where | Status |
|---|---|---|
