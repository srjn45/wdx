package aca

import (
	"fmt"
	"path/filepath"
)

// ClaudeCode is Anthropic's Claude Code CLI.
type ClaudeCode struct{}

// ID implements AiCodingAgent.
func (ClaudeCode) ID() string { return "claude_code" }

// Command implements AiCodingAgent. A session is resumed only once it has a
// transcript: Claude Code writes none until something is typed, and resuming
// such a session fails with "No conversation found".
func (ClaudeCode) Command(l Launch) ([]string, error) {
	transcripts, err := filepath.Glob(filepath.Join(l.UserHome, ".claude", "projects", "*", l.SessionID+".jsonl"))
	if err != nil {
		return nil, fmt.Errorf("look for the transcript of session %s: %w", l.SessionID, err)
	}
	if len(transcripts) > 0 {
		return []string{"claude", "--resume", l.SessionID}, nil
	}
	return []string{"claude", "--session-id", l.SessionID}, nil
}
