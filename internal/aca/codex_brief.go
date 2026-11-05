package aca

import (
	"context"
	"os"
	"path/filepath"
)

// Brief implements AiCodingAgent, reading no further back than the latest /new, and
// from the first request since: Codex's compactions keep their summary encrypted, so
// the reader takes none of them as a starting point. A restart's thread does not stop
// it.
func (Codex) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	since, err := codexSinceNew(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	return readTranscriptBrief(l.BackupDir, since, limit)
}

// codexSinceNew is the threads from the latest /new on, in the turns file's first-came
// order, nil when there was none. The user's threads are those the backup copied, as
// it copies no other.
func codexSinceNew(backupDir string) (map[string]bool, error) {
	user := func(thread string) bool {
		_, err := os.Stat(filepath.Join(backupDir, codexRawDir, thread+".jsonl"))
		return err == nil
	}
	fromNew, err := codexNewThreads(backupDir, user)
	if err != nil || len(fromNew) == 0 {
		return nil, err
	}
	first, _, err := codexThreads(filepath.Join(backupDir, codexTurnsFile))
	if err != nil {
		return nil, err
	}
	latest := len(first) - 1
	for !fromNew[first[latest]] {
		latest--
	}
	since := map[string]bool{}
	for _, id := range first[latest:] {
		since[id] = true
	}
	return since, nil
}
