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

	"github.com/srjn45/wdx/internal/logging"
)

// codexRawDir holds Codex's own bytes of each of the agent's sessions, with its
// subagents' under <session id>/subagents. The wdx doc CODEX_BACKUP.
const codexRawDir = "transcripts"

// codexSubagentStarted is in the line of a session file that says the session started
// a subagent, so only such lines are parsed.
var codexSubagentStarted = []byte(`"type":"SubAgentActivity"`)

// codexCopySessions copies what is new of each of the user's sessions in threads, and
// of their subagents, to the backup. A file that shrank or was replaced is kept, and
// copied again beside it; one Codex deleted leaves its copy as it is.
func codexCopySessions(ctx context.Context, l Launch, threads []string) error {
	var failed []error
	for _, id := range threads {
		file, err := codexSessionFile(l.UserHome, id)
		if err == nil && file != "" {
			var source string
			if source, err = codexThreadSource(file); err == nil && source == "user" {
				err = codexCopySession(ctx, l, id, file)
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, fmt.Errorf("session %s: %w", id, err))
		}
	}
	for _, err := range failed {
		logging.FromContext(ctx).Error("agent session not copied", "err", err)
	}
	return errors.Join(failed...)
}

// codexCopySession copies session's file, then each subagent its copy says it started,
// and theirs in turn, all under the session. A subagent with no file yet is looked for
// again at the next backup.
func codexCopySession(ctx context.Context, l Launch, session, file string) error {
	raw := filepath.Join(l.BackupDir, codexRawDir)
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(raw, session+".jsonl")
	if _, err := copyNewBytes(ctx, file, dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	queue := []string{dest}
	seen := map[string]bool{session: true}
	for len(queue) > 0 {
		started, err := codexSubagentsStarted(queue[0])
		queue = queue[1:]
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, id := range started {
			if seen[id] {
				continue
			}
			seen[id] = true
			file, err := codexSessionFile(l.UserHome, id)
			if err != nil {
				return err
			}
			to := filepath.Join(raw, session, "subagents", id+".jsonl")
			if file != "" {
				if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
					return err
				}
				if _, err := copyNewBytes(ctx, file, to); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			queue = append(queue, to)
		}
	}
	return nil
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
				item.Type == "SubAgentActivity" && item.Kind == "started" && codexSessionID.MatchString(item.Thread) &&
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
