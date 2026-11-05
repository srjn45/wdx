package aca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
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
	line, err := json.Marshal(codexRestart{Session: session, Threads: len(first)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.BackupDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.BackupDir, codexRestartsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// codexReadRestarts is the restarts file's lines, oldest first; none for an agent never
// restarted, or from before the file. A line it cannot read is left out.
func codexReadRestarts(backupDir string) ([]codexRestart, error) {
	f, err := os.Open(filepath.Join(backupDir, codexRestartsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var restarts []codexRestart
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var restart codexRestart
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &restart) == nil && restart.Session != "" {
			restarts = append(restarts, restart)
		}
		if errors.Is(err, io.EOF) {
			return restarts, nil
		}
		if err != nil {
			return nil, err
		}
	}
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
	restarted := map[string]bool{}
	for _, r := range restarts {
		for _, id := range first[min(r.Threads, len(first)):] {
			if user(id) {
				restarted[id] = true
				break
			}
		}
	}
	fromNew := map[string]bool{}
	seenFirst := false
	for _, id := range first {
		if !user(id) {
			continue
		}
		if seenFirst && !restarted[id] {
			fromNew[id] = true
		}
		seenFirst = true
	}
	return fromNew, nil
}
