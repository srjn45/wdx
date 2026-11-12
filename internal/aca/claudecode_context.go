package aca

// claudeCodeStatusFile is what Claude Code last gave its status line, which the
// settings claudeCodeWriteSettings writes have it keep in the backup.
const claudeCodeStatusFile = "claude-status.json"

// ContextSize implements AiCodingAgent, from what Claude Code last gave its status
// line: the tokens in the context, cache reads and writes among them, and the
// model's window. Before the first message and after /clear the tokens are 0. It
// gives the model's display name too, which a resumed session's screen does not
// show (the wdx doc STATE_LEFTOVERS). The status file is in the backup, so there is
// nothing else to follow. Checked on 2.1.319 (the wdx doc CONTEXT_SIZE).
func (ClaudeCode) ContextSize(l Launch) (ContextSize, []string, error) {
	if l.BackupDir == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	size, err := readStatusLine(l.BackupDir, claudeCodeStatusFile)
	return size, nil, err
}
