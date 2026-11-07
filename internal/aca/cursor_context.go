package aca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// What Cursor's status line is and keeps, in the backup (the wdx doc CURSOR_CONTEXT).
const (
	// cursorStatusFile is what Cursor last gave its status line.
	cursorStatusFile = "cursor-status.json"
	// cursorStatusScript is the status line's command, which Cursor runs as `sh
	// <script>`: it splits its statusLine command on whitespace and quotes as a shell
	// would, but runs no shell (string-argv, spawned without one; checked on
	// 2026.11.05), so wdx's cat and mv, and the user's command in braces, need one.
	cursorStatusScript = "cursor-status.sh"
)

// cursorStatusLine writes cursorStatusScript in the backup, statusLineCommand's text
// with the user's own command after wdx's, and returns the statusLine for the agent's
// settings: `sh <script>`, keeping the user's padding, updateIntervalMs and timeoutMs.
// users is the user's own statusLine; one that is not a command gives wdx's alone.
func cursorStatusLine(ctx context.Context, l Launch, users json.RawMessage) (json.RawMessage, error) {
	var theirs struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	var kept map[string]json.RawMessage
	if len(users) > 0 {
		if err := json.Unmarshal(users, &theirs); err != nil {
			logging.FromContext(ctx).Warn("user's cursor status line not read", "err", err)
		} else if err := json.Unmarshal(users, &kept); err != nil {
			return nil, err
		}
	}
	var command string
	if theirs.Type == "command" {
		command = strings.TrimSpace(theirs.Command)
	}
	script := filepath.Join(l.BackupDir, cursorStatusScript)
	arg, err := cursorArgument(script)
	if err != nil {
		return nil, err
	}
	text := statusLineCommand(filepath.Join(l.BackupDir, cursorStatusFile), command) + "\n"
	if err := os.MkdirAll(l.BackupDir, 0o700); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(script, []byte(text), 0o600); err != nil {
		return nil, err
	}
	line := map[string]any{"type": "command", "command": "sh " + arg}
	if command != "" {
		for _, key := range []string{"padding", "updateIntervalMs", "timeoutMs"} {
			if value, ok := kept[key]; ok {
				line[key] = value
			}
		}
	}
	return json.Marshal(line)
}

// cursorArgument is path as one argument of a command Cursor splits: on whitespace,
// with single or double quotes around an argument and no escapes (string-argv). It is
// given as it is when it holds none of those, else in the kind of quote it does not
// hold; one holding both kinds cannot be given.
func cursorArgument(path string) (string, error) {
	switch {
	case !strings.ContainsAny(path, `"'`) && strings.IndexFunc(path, unicode.IsSpace) < 0:
		return path, nil
	case !strings.Contains(path, `"`):
		return `"` + path + `"`, nil
	case !strings.Contains(path, `'`):
		return `'` + path + `'`, nil
	}
	return "", fmt.Errorf("Cursor cannot be given the path %q: it holds both kinds of quote", path)
}

// ContextSize implements AiCodingAgent, from what Cursor last gave its status line: the
// tokens in the context, the share of the window it shows rounded to tokens, and the
// model's window. Before the first message and after /clear Cursor gives null, read as
// 0 tokens and no window; in a chat whose size it knows, the size stays through a turn
// and changes with each response, so after a compaction it is the summary's request's
// only at the next response. The status file is in the backup, so there is nothing
// else to follow. Checked on 2026.11.05 (the wdx doc CURSOR_CONTEXT).
func (Cursor) ContextSize(l Launch) (ContextSize, []string, error) {
	if l.BackupDir == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	size, err := readStatusLine(l.BackupDir, cursorStatusFile)
	return size, nil, err
}
