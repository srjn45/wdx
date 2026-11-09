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

// Antigravity is Google's Antigravity CLI. It has written every method but
// Detached, as nothing of its conversations runs outside its terminal. The wdx
// docs ACA_ANTIGRAVITY and ANTIGRAVITY_PARITY.
type Antigravity struct {
	NoDetached
}

// AntigravityID is Antigravity's ID.
const AntigravityID = "antigravity"

// ID implements AiCodingAgent.
func (Antigravity) ID() string { return AntigravityID }

// Program implements AiCodingAgent.
func (Antigravity) Program() string { return "agy" }

// Command implements AiCodingAgent. Antigravity names its own conversations:
// l.SessionID is resumed once it names one Antigravity keeps, which BackUp makes it
// after the first message, its db put back from the backup when Antigravity's is
// gone; before that a start begins a new conversation, as one whose db is gone
// would, silently. It has no flag for the system prompt, so
// the note is the rules file of a folder of the backup added to the workspace for
// this session, whose hooks report each conversation to the backup. What
// --settings holds is for this session only, never saved, over the user's own
// settings: among them the status line that keeps the context size in the backup.
func (Antigravity) Command(ctx context.Context, l Launch) ([]string, error) {
	command := []string{"agy"}
	resume, err := antigravityKeeps(l.UserHome, l.SessionID)
	if err == nil && !resume {
		resume, err = antigravityRestore(l)
	}
	if err != nil {
		return nil, fmt.Errorf("look for Antigravity's conversation %s: %w", l.SessionID, err)
	}
	if resume {
		command = append(command, "--conversation="+l.SessionID)
	}
	if l.Model != "" {
		command = append(command, "--model", l.Model)
	}
	if l.Permissions == PermissionsFull {
		command = append(command, "--dangerously-skip-permissions")
	}
	if l.BackupDir != "" {
		dir, err := antigravityWriteFolder(l.BackupDir, l.Dir, l.Note)
		if err != nil {
			return nil, fmt.Errorf("write Antigravity's folder in the backup: %w", err)
		}
		command = append(command, "--add-dir", dir)
	}
	// Queued, so that alt+m, which does the opposite of this setting, sends a
	// message to the running turn rather than queueing it for after the turn.
	settings := map[string]any{"queuedMessages": "queue"}
	if l.Dir != "" {
		// Its question whether to trust a new folder would stop the agent until a
		// human answers it.
		settings["trustedWorkspaces"] = []string{l.Dir}
	}
	if l.Permissions == PermissionsFull {
		settings["enableTerminalSandbox"] = false
	}
	if l.BackupDir != "" {
		settings["statusLine"] = antigravityStatusLine(ctx, l)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	return append(command, "--settings", string(data)), nil
}

// antigravityWriteFolder writes the agent's folder in backup, which --add-dir makes
// part of the workspace, and says where it is: the note as its rules file, and the
// hooks that report each conversation. Antigravity creates new files in the first
// of its workspace folders by path, which this one nearly always is, so the note
// ends by naming the project as where to work.
func antigravityWriteFolder(backup, project, note string) (string, error) {
	dir := filepath.Join(backup, antigravityDir)
	if err := antigravityMoveOldFolder(backup, dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		return "", err
	}
	rules := filepath.Join(dir, "AGENTS.md")
	if note == "" {
		if err := os.Remove(rules); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	} else if err := atomicfile.Write(rules, []byte(antigravityNote(dir, project, note)), 0o600); err != nil {
		return "", err
	}
	hooks, err := antigravityHooks(backup)
	if err != nil {
		return "", err
	}
	return dir, atomicfile.Write(filepath.Join(dir, ".agents", "hooks.json"), hooks, 0o600)
}

// antigravityOldDir is what the agent's folder was called while it held only the
// note: an agent started before then has one, moved at its next start.
const antigravityOldDir = "antigravity-note"

// antigravityMoveOldFolder moves the old folder in backup to dir, or removes it when
// dir is there too, which a start that failed between the two could leave.
func antigravityMoveOldFolder(backup, dir string) error {
	old := filepath.Join(backup, antigravityOldDir)
	if _, err := os.Lstat(old); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return os.Rename(old, dir)
	} else if err != nil {
		return err
	}
	return os.RemoveAll(old)
}

// antigravityNote is the rules file's text: note, then where to work.
func antigravityNote(dir, project, note string) string {
	text := note + "\n\nThe workspace folder `" + dir + "` holds only these instructions from wdx: never create or change files there."
	if project != "" {
		text += " Your project is `" + project + "`, where you were started: work and create files there."
	}
	return text + "\n"
}

// Paste implements AiCodingAgent. A bracketed paste keeps a message's newlines
// content, and a paste-end marker in the message is taken out, or the rest would be
// typed as keys.
func (Antigravity) Paste(w io.Writer, message string) error {
	for strings.Contains(message, antigravityPasteEnd) {
		message = strings.ReplaceAll(message, antigravityPasteEnd, "")
	}
	message = escapeBang(message)
	_, err := io.WriteString(w, antigravityPasteStart+message+antigravityPasteEnd)
	return err
}

// Submit implements AiCodingAgent: alt+m, which gives the message to the running
// turn at its next step, as Claude Code reads one, and submits it at rest, as Enter
// does. It waits for the next step: the running tool's end, which it does not stop,
// or the end of the model's answer when no tool runs. alt+m is one write, or
// Antigravity could read its Esc alone, which interrupts the turn. Checked on 1.5.2
// (the wdx doc ANTIGRAVITY_PARITY).
func (Antigravity) Submit(w io.Writer, _ func()) error {
	_, err := io.WriteString(w, antigravitySendNow)
	return err
}

// SendNow implements AiCodingAgent: nothing, as Submit already steers.
func (Antigravity) SendNow(io.Writer) error { return nil }

const (
	antigravityPasteStart = "\x1b[200~"
	antigravityPasteEnd   = "\x1b[201~"
	// antigravitySendNow is alt+m, which sends a message to the running turn with
	// queuedMessages set to queue, and submits it at rest.
	antigravitySendNow = "\x1bm"
)

// Interrupt implements AiCodingAgent. Ctrl+C would end it.
func (Antigravity) Interrupt() []byte { return []byte("\x1b") }

// ScreenParser implements AiCodingAgent.
func (Antigravity) ScreenParser() ScreenParser { return AntigravityScreenParser{} }

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
