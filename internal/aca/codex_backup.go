package aca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// codexRawDir holds Codex's own bytes of each of the agent's sessions, with its
// subagents' under <session id>/subagents. The wdx doc CODEX_BACKUP.
const codexRawDir = "transcripts"

// codexSubagentStarted is in the line of a session file that says the session started
// a subagent, so only such lines are parsed.
var codexSubagentStarted = []byte(`"type":"SubAgentActivity"`)

// codexPathsFile records where Codex kept each of the user's sessions copied, so that
// Command can put a copy back there once Codex deletes its file: checked by hand on
// 0.163.0, `codex resume` fails with the file gone and resumes once it is back, though
// Codex's sqlite still lists it either way. The wdx doc CODEX_BACKUP.
const codexPathsFile = "codex-sessions.json"

// codexCopySessions copies what is new of each of the user's sessions in threads, and
// of their subagents, to the backup, and says which sessions' files were replaced. A
// file that shrank or was replaced is kept, and copied again beside it; one Codex
// deleted leaves its copy as it is.
func codexCopySessions(ctx context.Context, l Launch, threads []string) (replaced map[string]bool, _ error) {
	replaced = map[string]bool{}
	var failed []error
	found := map[string]string{}
	for _, id := range threads {
		file, err := codexSessionFile(l.UserHome, id)
		if err == nil && file != "" {
			var source string
			if source, err = codexThreadSource(file); err == nil && source == "user" {
				found[id] = file
				replaced[id], err = codexCopySession(ctx, l, id, file)
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, fmt.Errorf("session %s: %w", id, err))
		}
	}
	if err := codexKeepPaths(ctx, l.BackupDir, found); err != nil {
		failed = append(failed, fmt.Errorf("record where the sessions are: %w", err))
	}
	for _, err := range failed {
		logging.FromContext(ctx).Error("agent session not copied", "err", err)
	}
	return replaced, errors.Join(failed...)
}

// codexCopySession copies session's file, then each subagent its copy says it started,
// and theirs in turn, all under the session. A subagent with no file yet is looked for
// again at the next backup. It says whether session's own file was replaced.
func codexCopySession(ctx context.Context, l Launch, session, file string) (replaced bool, err error) {
	raw := filepath.Join(l.BackupDir, codexRawDir)
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return false, err
	}
	dest := filepath.Join(raw, session+".jsonl")
	if replaced, err = copyNewBytes(ctx, file, dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	queue := []string{dest}
	seen := map[string]bool{session: true}
	for len(queue) > 0 {
		started, err := codexSubagentsStarted(queue[0])
		queue = queue[1:]
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return replaced, err
		}
		for _, id := range started {
			if seen[id] {
				continue
			}
			seen[id] = true
			file, err := codexSessionFile(l.UserHome, id)
			if err != nil {
				return replaced, err
			}
			to := filepath.Join(raw, session, "subagents", id+".jsonl")
			if file != "" {
				if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
					return replaced, err
				}
				if _, err := copyNewBytes(ctx, file, to); err != nil && !errors.Is(err, os.ErrNotExist) {
					return replaced, err
				}
			}
			queue = append(queue, to)
		}
	}
	return replaced, nil
}

// codexSubagentsStarted is the threads of the subagents the session file says it
// started, in the order it started them, read a line at a time.
func codexSubagentsStarted(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var started []string
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, codexSubagentStarted) {
			var entry struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Item struct {
						Type   string `json:"type"`
						Kind   string `json:"kind"`
						Thread string `json:"agent_thread_id"`
					} `json:"item"`
				} `json:"payload"`
			}
			item := &entry.Payload.Item
			if json.Unmarshal(line, &entry) == nil && entry.Type == "event_msg" && entry.Payload.Type == "item_completed" &&
				item.Type == "SubAgentActivity" && item.Kind == "started" && sessionUUID.MatchString(item.Thread) &&
				!slices.Contains(started, item.Thread) {
				started = append(started, item.Thread)
			}
		}
		if errors.Is(err, io.EOF) {
			return started, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// codexThreadSource is the thread_source of the session file's session_meta, its first
// line: user for the user's conversation.
func codexThreadSource(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			ThreadSource string `json:"thread_source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "session_meta" {
		return "", nil
	}
	return entry.Payload.ThreadSource, nil
}

// codexKeepPaths adds the paths found to the paths file, writing it only when one is
// new or moved.
func codexKeepPaths(ctx context.Context, backupDir string, found map[string]string) error {
	paths, err := codexReadPaths(backupDir)
	if err != nil {
		return err
	}
	changed := false
	for id, path := range found {
		if paths[id] != path {
			paths[id], changed = path, true
		}
	}
	if !changed {
		return nil
	}
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	file := filepath.Join(backupDir, codexPathsFile)
	if err := atomicfile.Write(file, append(data, '\n'), 0o600); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("agent session paths recorded", "path", file, "sessions", len(paths))
	return nil
}

// codexReadPaths is the paths file's sessions, each with where Codex kept it; none
// before the first backup.
func codexReadPaths(backupDir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(backupDir, codexPathsFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil, fmt.Errorf("read %s: %w", codexPathsFile, err)
	}
	return paths, nil
}

// codexKept is the backup's copy of session id and the path Codex kept it at, or ""
// for both when the backup lacks either, and so cannot put it back.
func codexKept(backupDir, id string) (copied, at string, err error) {
	if backupDir == "" || !sessionUUID.MatchString(id) {
		return "", "", nil
	}
	paths, err := codexReadPaths(backupDir)
	if err != nil || paths[id] == "" {
		return "", "", err
	}
	copied = filepath.Join(backupDir, codexRawDir, id+".jsonl")
	if _, err := os.Stat(copied); errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	} else if err != nil {
		return "", "", err
	}
	return copied, paths[id], nil
}

// codexRestore puts the backup's copy of l.SessionID back where Codex kept it, and
// says whether it did: Codex resumes from it there.
func codexRestore(l Launch) (bool, error) {
	copied, at, err := codexKept(l.BackupDir, l.SessionID)
	if err != nil || copied == "" {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return false, err
	}
	if err := atomicfile.Copy(at, copied, 0o600); err != nil {
		return false, err
	}
	return true, nil
}
