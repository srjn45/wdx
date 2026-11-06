package aca

import (
	"context"
	"path/filepath"
)

// codexRestartsFile records each restart wdx began, since Codex's own files do not
// tell a thread a restart began from one /new began: a line per restart, with the
// session wdx gave it and how many threads the turns file named by then. The first
// user thread at or after that position is the restart's. The wdx doc CODEX_BRIEF.
const codexRestartsFile = "codex-restarts.jsonl"

type codexRestart struct {
	Session string `json:"session"`
	Threads int    `json:"threads"`
}

// BeginSession implements AiCodingAgent. Its line is what makes session the one BackUp
// says the agent is in until the new conversation's first turn, so that the next start
// begins it rather than resume the thread before, and what tells the brief that the
// thread which comes next is a restart's, not a /new.
func (Codex) BeginSession(_ context.Context, l Launch, session string) error {
	first, _, err := codexThreads(filepath.Join(l.BackupDir, codexTurnsFile))
	if err != nil {
		return err
	}
	return appendJSONLine(l.BackupDir, codexRestartsFile, codexRestart{Session: session, Threads: len(first)})
}

// codexReadRestarts is the restarts file's lines, oldest first; none for an agent never
// restarted, or from before the file.
func codexReadRestarts(backupDir string) ([]codexRestart, error) {
	return readJSONLines(filepath.Join(backupDir, codexRestartsFile), func(r codexRestart) bool { return r.Session != "" })
}

// codexNewThreads is the user threads, of the turns file's, that /new began: every one
// after the first that is not a restart's. user says which threads are the user's
// conversations, as the turns file also names the thread Codex names a conversation in
// and subagents'.
func codexNewThreads(backupDir string, user func(thread string) bool) (map[string]bool, error) {
	first, _, err := codexThreads(filepath.Join(backupDir, codexTurnsFile))
	if err != nil {
		return nil, err
	}
	restarts, err := codexReadRestarts(backupDir)
	if err != nil {
		return nil, err
	}
	var at []int
	for _, r := range restarts {
		at = append(at, r.Threads)
	}
	return sessionsTheUserBegan(first, at, user), nil
}
