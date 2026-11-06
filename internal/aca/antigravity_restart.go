package aca

import (
	"context"
	"path/filepath"
)

// antigravityRestartsFile records each restart wdx began, as codexRestartsFile does:
// Antigravity's files do not tell a conversation a restart began from one /clear
// began. A line per restart, with the session wdx gave it and how many conversations
// the hooks file named by then, subagents' among them. The first of the user's
// conversations at or after that position is the restart's. The wdx doc
// ANTIGRAVITY_PARITY.
const antigravityRestartsFile = "antigravity-restarts.jsonl"

type antigravityRestart struct {
	Session       string `json:"session"`
	Conversations int    `json:"conversations"`
}

// BeginSession implements AiCodingAgent. Its line makes session the one BackUp says
// the agent is in until the new conversation's first message, so that the next start
// begins one rather than resume the conversation before, and tells the brief that
// the conversation which comes next is a restart's, not a /clear.
func (Antigravity) BeginSession(_ context.Context, l Launch, session string) error {
	starts, err := antigravityStarts(filepath.Join(l.BackupDir, antigravityHooksFile))
	if err != nil {
		return err
	}
	return appendJSONLine(l.BackupDir, antigravityRestartsFile, antigravityRestart{Session: session, Conversations: len(antigravityFirstCame(starts))})
}

// antigravityReadRestarts is the restarts file's lines, oldest first; none for an agent
// never restarted.
func antigravityReadRestarts(backupDir string) ([]antigravityRestart, error) {
	return readJSONLines(filepath.Join(backupDir, antigravityRestartsFile), func(r antigravityRestart) bool { return r.Session != "" })
}

// antigravityFirstCame is the conversations of starts, each once, in the order they
// first came: a resumed one starts again.
func antigravityFirstCame(starts []antigravityStart) []string {
	var first []string
	seen := map[string]bool{}
	for _, s := range starts {
		if !seen[s.ConversationID] {
			seen[s.ConversationID] = true
			first = append(first, s.ConversationID)
		}
	}
	return first
}
