package aca

import (
	"context"
	"os"
	"path/filepath"
)

// Brief implements AiCodingAgent, reading no further back than the latest /clear, and
// from the latest compaction since, whose summary Antigravity keeps readable, or else
// the first request. A restart's conversation does not stop it.
func (Antigravity) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	since, err := antigravitySinceClear(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	return readTranscriptBrief(l.BackupDir, since, limit)
}

// antigravitySinceClear is the conversations from the latest /clear on, in the order
// they first came, nil when there was none. The user's conversations are those the
// backup copied, as it copies a subagent's only under its parent's.
func antigravitySinceClear(backupDir string) (map[string]bool, error) {
	starts, err := antigravityStarts(filepath.Join(backupDir, antigravityHooksFile))
	if err != nil {
		return nil, err
	}
	restarts, err := antigravityReadRestarts(backupDir)
	if err != nil {
		return nil, err
	}
	var at []int
	for _, r := range restarts {
		at = append(at, r.Conversations)
	}
	user := func(id string) bool {
		_, err := os.Stat(filepath.Join(backupDir, antigravityRawDir, id+".jsonl"))
		return err == nil
	}
	first := antigravityFirstCame(starts)
	return sinceTheLatest(first, sessionsTheUserBegan(first, at, user)), nil
}
