package aca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
)

// Antigravity is Google's Antigravity CLI. It has written only its command, its
// delivering and its models; the rest are stand-ins. The wdx doc ACA_ANTIGRAVITY.
type Antigravity struct {
	NoBackUp
	NoBeginSession
	NoBrief
	NoDetached
	NoSteer
}

// AntigravityID is Antigravity's ID.
const AntigravityID = "antigravity"

// ID implements AiCodingAgent.
func (Antigravity) ID() string { return AntigravityID }

// Program implements AiCodingAgent.
func (Antigravity) Program() string { return "agy" }

// antigravityNoteDir is the folder of an agent's dir that holds only its launch note,
// as --add-dir makes a folder part of the workspace.
const antigravityNoteDir = "antigravity-note"

// Command implements AiCodingAgent. Antigravity names its own conversations and
// cannot be given one, so l.SessionID is not used: every start begins a new
// conversation. It has no flag for the system prompt, so the note is the rules
// file of a folder added to the workspace for this session. What --settings
// holds is for this session only, never saved, over the user's own settings.
func (Antigravity) Command(l Launch) ([]string, error) {
	command := []string{"agy"}
	if l.Model != "" {
		command = append(command, "--model", l.Model)
	}
	if l.Permissions == PermissionsFull {
		command = append(command, "--dangerously-skip-permissions")
	}
	if l.Note != "" && l.BackupDir != "" {
		dir, err := antigravityWriteNote(l.BackupDir, l.Note)
		if err != nil {
			return nil, fmt.Errorf("write the launch note: %w", err)
		}
		command = append(command, "--add-dir", dir)
	}
	// Queued, as the inbox types messages during a turn and a user's
	// send-immediately would have them interrupt it.
	settings := map[string]any{"queuedMessages": "queue"}
	if l.Dir != "" {
		// Its question whether to trust a new folder would stop the agent until a
		// human answers it.
		settings["trustedWorkspaces"] = []string{l.Dir}
	}
	if l.Permissions == PermissionsFull {
		settings["enableTerminalSandbox"] = false
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	return append(command, "--settings", string(data)), nil
}

// antigravityWriteNote writes note as the rules file of its folder in backup, and
// says the folder.
func antigravityWriteNote(backup, note string) (string, error) {
	dir := filepath.Join(backup, antigravityNoteDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, atomicfile.Write(filepath.Join(dir, "AGENTS.md"), []byte(note+"\n"), 0o600)
}

// Deliver implements AiCodingAgent, as Claude Code's: a bracketed paste keeps a
// message's newlines content, and Enter is a key of its own. Antigravity needed no
// wait before Enter on 1.5.0; Claude Code's is kept as a margin. A paste-end
// marker in the message is taken out, or the rest would be typed as keys.
func (Antigravity) Deliver(w io.Writer, message string) error {
	for strings.Contains(message, antigravityPasteEnd) {
		message = strings.ReplaceAll(message, antigravityPasteEnd, "")
	}
	if _, err := io.WriteString(w, antigravityPasteStart+message+antigravityPasteEnd); err != nil {
		return err
	}
	time.Sleep(antigravityPasteSettle)
	_, err := io.WriteString(w, "\r")
	return err
}

const (
	antigravityPasteStart  = "\x1b[200~"
	antigravityPasteEnd    = "\x1b[201~"
	antigravityPasteSettle = 150 * time.Millisecond
)

// Interrupt implements AiCodingAgent. Ctrl+C would end it.
func (Antigravity) Interrupt() []byte { return []byte("\x1b") }

// ScreenParser implements AiCodingAgent.
func (Antigravity) ScreenParser() ScreenParser { return NoScreenParser{} }

// antigravityModelsTime is how long `agy models` may take: it asks over the network.
const antigravityModelsTime = 30 * time.Second

// Models implements AiCodingAgent: what `agy models` lists. None is its default,
// which the list does not say.
func (Antigravity) Models(ctx context.Context, l Launch) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, antigravityModelsTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.Shell, "-lc", "agy models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("agy models: %w", err)
	}
	return parseAntigravityModels(string(out))
}

var antigravityModelID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// parseAntigravityModels reads the lines `agy models` prints on stdout, an id and
// a name between a tab. Other lines, such as what a login shell's startup files
// print, are not models.
func parseAntigravityModels(out string) ([]Model, error) {
	var models []Model
	for line := range strings.Lines(out) {
		id, name, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		name = strings.TrimSpace(name)
		if !ok || !antigravityModelID.MatchString(id) || name == "" || strings.Contains(name, "\t") {
			continue
		}
		models = append(models, Model{ID: id, Name: name})
	}
	if len(models) == 0 {
		return nil, errors.New("agy models listed none")
	}
	return models, nil
}
