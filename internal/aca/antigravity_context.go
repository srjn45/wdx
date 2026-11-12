package aca

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// antigravityStatusFile is what Antigravity last gave its status line, which the
// statusLine Command passes in --settings has it keep in the backup.
const antigravityStatusFile = "antigravity-status.json"

// antigravityStatusLine is the statusLine for the agent's settings, whose command
// Antigravity runs through a shell: statusLineCommand's text, the user's own command
// after wdx's, with their padding and timeout_ms kept; and stack_with_default, so that
// Antigravity's own footer, which AntigravityScreenParser reads, stays drawn. A status
// line given with --settings replaces the user's own, and what the command prints is
// drawn under the footer (checked on 1.5.2, the wdx doc ANTIGRAVITY_CONTEXT).
func antigravityStatusLine(ctx context.Context, l Launch) map[string]any {
	theirs, kept := antigravityUserStatusLine(ctx, l.UserHome)
	line := map[string]any{
		"command":            statusLineCommand(filepath.Join(l.BackupDir, antigravityStatusFile), "", theirs),
		"stack_with_default": true,
	}
	for _, key := range []string{"padding", "timeout_ms"} {
		if value, ok := kept[key]; ok {
			line[key] = value
		}
	}
	return line
}

// antigravityUserStatusLine is the command of the user's own status line, in their
// ~/.gemini/antigravity-cli/settings.json, and the rest of its settings; "" and
// nothing when they have none. Antigravity's statusLine has no type: a command is one.
func antigravityUserStatusLine(ctx context.Context, home string) (string, map[string]json.RawMessage) {
	if home == "" {
		return "", nil
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	var settings struct {
		StatusLine json.RawMessage `json:"statusLine"`
	}
	var line struct {
		Command string `json:"command"`
	}
	var kept map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(data, &settings)
	}
	if err == nil && len(settings.StatusLine) > 0 {
		if err = json.Unmarshal(settings.StatusLine, &line); err == nil {
			err = json.Unmarshal(settings.StatusLine, &kept)
		}
	}
	if err != nil {
		logging.FromContext(ctx).Error("user's antigravity status line not read", "path", path, "err", err)
		return "", nil
	}
	command := strings.TrimSpace(line.Command)
	if command == "" {
		return "", nil
	}
	return command, kept
}

// ContextSize implements AiCodingAgent, from what Antigravity last gave its status
// line, Claude Code's JSON: the tokens in the context and the model's window. Before
// the first message and after /clear the tokens are 0 and the window known; after
// /compact the size is the compaction's own request, until the next response. The
// status file is in the backup, so there is nothing else to follow. Checked on 1.5.2
// (the wdx doc ANTIGRAVITY_CONTEXT).
func (Antigravity) ContextSize(l Launch) (ContextSize, []string, error) {
	if l.BackupDir == "" {
		return ContextSize{}, nil, ErrNoContextSize
	}
	size, err := readStatusLine(l.BackupDir, antigravityStatusFile)
	// Its screen gives the model on every read.
	size.Model, size.ModelAt = "", time.Time{}
	return size, nil, err
}
