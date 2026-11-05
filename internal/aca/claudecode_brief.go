package aca

import (
	"context"
)

// Brief implements AiCodingAgent, reading no further back than the latest /clear.
func (ClaudeCode) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	return readTranscriptBrief(l.BackupDir, claudeCodeSinceClear(starts), limit)
}

// claudeCodeSinceClear is the sessions from the latest /clear on, nil when there was
// none: the brief looks no further back. Those a restart began do not stop it. Which one
// was written first decides, as a /clear's session may have written nothing.
func claudeCodeSinceClear(starts []claudeCodeStart) map[string]bool {
	var since map[string]bool
	for _, s := range starts {
		if s.Source == claudeCodeSourceClear {
			since = map[string]bool{}
		}
		if since != nil {
			since[s.SessionID] = true
		}
	}
	return since
}
