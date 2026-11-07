package aca

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// claudeCodeStatusFile is what Claude Code last gave its status line, which the
// settings claudeCodeWriteSettings writes have it keep in the backup.
const claudeCodeStatusFile = "claude-status.json"

// claudeCodeStatus is what ContextSize reads of claudeCodeStatusFile.
type claudeCodeStatus struct {
	ContextWindow *struct {
		TotalInputTokens  int64 `json:"total_input_tokens"`
		ContextWindowSize int64 `json:"context_window_size"`
	} `json:"context_window"`
}

// ContextSize implements AiCodingAgent, from what Claude Code last gave its status
// line: the tokens in the context, cache reads and writes among them, and the
// model's window. Before the first message and after /clear the tokens are 0. The
// status file is in the backup, so there is nothing else to follow. Checked on
// 2.1.319 (the wdx doc CONTEXT_SIZE).
func (ClaudeCode) ContextSize(l Launch) (ContextSize, []string, error) {
	if l.BackupDir == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	data, err := os.ReadFile(filepath.Join(l.BackupDir, claudeCodeStatusFile))
	if errors.Is(err, os.ErrNotExist) {
		return ContextSize{}, nil, ErrNoContextSize
	}
	if err != nil {
		return ContextSize{}, nil, err
	}
	var status claudeCodeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return ContextSize{}, nil, fmt.Errorf("read %s: %w", claudeCodeStatusFile, err)
	}
	if status.ContextWindow == nil {
		return ContextSize{}, nil, ErrNoContextSize
	}
	return ContextSize{Tokens: status.ContextWindow.TotalInputTokens, Window: status.ContextWindow.ContextWindowSize}, nil, nil
}
