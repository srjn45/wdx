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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// Cursor is Anysphere's Cursor CLI, cursor-agent: the wdx docs ACA_CURSOR and
// CURSOR_PARITY.
type Cursor struct {
	NoDetached
}

// CursorID is Cursor's ID.
const CursorID = "cursor"

// ID implements AiCodingAgent.
func (Cursor) ID() string { return CursorID }

// Program implements AiCodingAgent. Its installer puts the same binary on the PATH as
// agent too, a name too common to say Cursor is installed.
func (Cursor) Program() string { return "cursor-agent" }

// ScreenParser implements AiCodingAgent.
func (Cursor) ScreenParser() ScreenParser { return CursorScreenParser{} }

// cursorPluginDir is the plugin Command writes into an agent's backup to give the
// launch note, as Cursor has no flag that adds to its system prompt.
const cursorPluginDir = "cursor-plugin"

// cursorConfigDir is the agent's own Cursor config folder in its backup, which
// CURSOR_CONFIG_DIR points Cursor at: its settings and its chats. The login, hooks,
// MCP servers and rules stay the user's, as Cursor reads them from the home.
const cursorConfigDir = "cursor-config"

// cursorSettingsFile is Cursor's settings file, in ~/.cursor or CURSOR_CONFIG_DIR.
const cursorSettingsFile = "cli-config.json"

// Command implements AiCodingAgent. Cursor names its own chats: l.SessionID is
// resumed once it names a chat of the agent's config folder made in l.Dir, which
// BackUp makes it after the first message, and before that a start begins a new
// chat, which the launch note reaches only then, as --resume runs no sessionStart
// hook. A chat whose store Cursor lost is put back from the backup's snapshot of it,
// and resumed. --trust skips the prompt a directory Cursor has not been told to trust starts
// with, which a message delivered into makes it quit; Cursor saves that trust for the
// directory. With a backup, Cursor runs with a config folder of the agent's own, so
// --model changes only the agent's settings; without one, --model also becomes the
// user's default in ~/.cursor/cli-config.json, so it is passed only when a model was
// asked for (docs/ACA.md).
func (Cursor) Command(ctx context.Context, l Launch) ([]string, error) {
	command := []string{"cursor-agent", "--trust"}
	var config string
	if l.BackupDir != "" {
		config = filepath.Join(l.BackupDir, cursorConfigDir)
		restored, err := cursorRestore(ctx, l)
		if err != nil {
			return nil, fmt.Errorf("put back Cursor's chat %s from its backup: %w", l.SessionID, err)
		}
		resume, err := cursorResumes(config, l.SessionID, l.Dir)
		if err != nil {
			return nil, fmt.Errorf("look for Cursor's chat %s: %w", l.SessionID, err)
		}
		if restored || resume {
			command = append(command, "--resume", l.SessionID)
		}
	}
	if l.Model != "" {
		command = append(command, "--model", l.Model)
	}
	if l.Permissions == PermissionsFull {
		command = append(command, "--force", "--sandbox", "disabled", "--approve-mcps")
	}
	if l.BackupDir == "" {
		return command, nil
	}
	if err := cursorWriteSettings(ctx, config, l); err != nil {
		return nil, fmt.Errorf("write the agent's Cursor settings: %w", err)
	}
	command = append([]string{"env", "CURSOR_CONFIG_DIR=" + config}, command...)
	if l.Note == "" {
		return command, nil
	}
	dir, err := cursorWritePlugin(filepath.Join(l.BackupDir, cursorPluginDir), l.Note)
	if err != nil {
		return nil, fmt.Errorf("write the plugin that gives the launch note: %w", err)
	}
	return append(command, "--plugin-dir", dir), nil
}

// cursorWriteSettings writes the agent's cli-config.json in config: the user's, key
// for key, as it is at this start, with steering on, so that Enter on a queued
// message steers it into the turn rather than stopping the running tool, and with the
// status line that keeps the context size in the backup, the user's own run after it.
// What the agent changed in its own copy is lost. A user file that is missing or
// cannot be read as an object gives those two alone, as Cursor's defaults are then
// the user's too.
func cursorWriteSettings(ctx context.Context, config string, l Launch) error {
	settings := map[string]json.RawMessage{}
	if l.UserHome != "" {
		user := filepath.Join(l.UserHome, ".cursor", cursorSettingsFile)
		data, err := os.ReadFile(user)
		if err == nil {
			err = json.Unmarshal(data, &settings)
		}
		if err == nil && settings == nil {
			err = errors.New("null, not an object")
		}
		if err != nil {
			settings = map[string]json.RawMessage{}
			if !errors.Is(err, os.ErrNotExist) {
				logging.FromContext(ctx).Warn("user cursor settings not copied", "path", user, "err", err)
			}
		}
	}
	settings["steering"] = json.RawMessage("true")
	statusLine, err := cursorStatusLine(ctx, l, settings["statusLine"])
	if err != nil {
		return err
	}
	settings["statusLine"] = statusLine
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(config, cursorSettingsFile), data, 0o600)
}

// cursorWritePlugin writes, in dir, a plugin whose sessionStart hook gives note to
// every conversation that starts: the hook's additional_context, which Cursor adds to
// what the model reads. The hook only prints a file, so no script is needed.
func cursorWritePlugin(dir, note string) (string, error) {
	notePath := filepath.Join(dir, "note.json")
	quoted := "'" + strings.ReplaceAll(notePath, "'", `'\''`) + "'"
	files := []struct {
		path    string
		content any
	}{
		{notePath, map[string]any{"additional_context": note}},
		{filepath.Join(dir, "hooks.json"), map[string]any{"version": 1, "hooks": map[string]any{
			"sessionStart": []any{map[string]any{"command": "cat " + quoted}},
		}}},
		{filepath.Join(dir, ".cursor-plugin", "plugin.json"), map[string]any{"name": "wdx", "hooks": "hooks.json"}},
	}
	for _, f := range files {
		data, err := json.Marshal(f.content)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
			return "", err
		}
		if err := atomicfile.Write(f.path, data, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Bracketed paste keeps a message's newlines content, not Enter.
const (
	cursorPasteStart = "\x1b[200~"
	cursorPasteEnd   = "\x1b[201~"
	// cursorPasteSettle is the wait before each Enter, which is lost when it comes
	// in the same write as the paste.
	cursorPasteSettle = 150 * time.Millisecond
)

// Deliver implements AiCodingAgent: as Steer, so a message sent while Cursor works
// is read at its next step rather than after the turn.
func (Cursor) Deliver(w io.Writer, message string) error { return cursorType(w, message) }

// Steer implements AiCodingAgent: the paste, Enter, which queues it as the newest
// follow-up in a turn, then Enter, which steers the oldest follow-up, with the
// steering setting Command pins: during a tool the model gets it at once, while it
// streams at the next tool call or the stream's end. At rest the first Enter
// submits and the second does nothing. Checked on 2026.11.05 (the wdx doc
// CURSOR_PARITY).
func (Cursor) Steer(w io.Writer, message string) error { return cursorType(w, message) }

// cursorType pastes message, then presses Enter twice, each once the keys before it
// have settled. A paste-end marker in the message is taken out, or the rest would be
// typed as keys, and a final backslash gets a space, as Cursor reads backslash then
// Enter as a new line and leaves the message in the box.
func cursorType(w io.Writer, message string) error {
	for strings.Contains(message, cursorPasteEnd) {
		message = strings.ReplaceAll(message, cursorPasteEnd, "")
	}
	if strings.HasSuffix(message, `\`) {
		message += " "
	}
	if _, err := io.WriteString(w, cursorPasteStart+message+cursorPasteEnd); err != nil {
		return err
	}
	for range 2 {
		time.Sleep(cursorPasteSettle)
		if _, err := io.WriteString(w, "\r"); err != nil {
			return err
		}
	}
	return nil
}

// Interrupt implements AiCodingAgent: Ctrl+C, as Esc does not stop a turn that is
// writing text. Ctrl+C puts the message that began the turn back in the input box,
// and pressed again with nothing to stop, it quits. The holder never presses it, as
// Cursor steers.
func (Cursor) Interrupt() []byte { return []byte{0x03} }

const cursorCommandTime = 30 * time.Second

// Models implements AiCodingAgent: what `cursor-agent models` lists for the user's
// account, which it asks Cursor's servers for.
func (Cursor) Models(ctx context.Context, l Launch) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, cursorCommandTime)
	defer cancel()
	const command = "cursor-agent models"
	cmd := exec.CommandContext(ctx, l.Shell, "-lc", command)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0 {
			return nil, fmt.Errorf("%s: %w: %s", command, err, bytes.TrimSpace(exit.Stderr))
		}
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	models := parseCursorModels(out)
	if len(models) == 0 {
		return nil, fmt.Errorf("%s listed no models: %q", command, firstLine(out))
	}
	return models, nil
}

// cursorModelLine is a line of `cursor-agent models`, "<id> - <name>". An id may hold
// a parameterized model's brackets.
var cursorModelLine = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._:\[\]=,-]*) - (.+)$`)

const cursorDefaultMark = " (default)"

// parseCursorModels reads the models out of what `cursor-agent models` prints, skipping
// its heading, its tip and what a login shell's startup files print. Some names end in
// zero-width spaces, and some have two spaces inside.
func parseCursorModels(out []byte) []Model {
	var models []Model
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		m := cursorModelLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if m == nil {
			continue
		}
		name := strings.Join(strings.Fields(strings.ReplaceAll(m[2], "\u200b", "")), " ")
		model := Model{ID: m[1], Name: name}
		if n, ok := strings.CutSuffix(name, cursorDefaultMark); ok {
			model.Name, model.Default = n, true
		}
		if model.Name != "" {
			models = append(models, model)
		}
	}
	return models
}

// firstLine is the first line of out, for an error that says what came instead.
func firstLine(out []byte) string {
	line, _, _ := bytes.Cut(bytes.TrimSpace(out), []byte("\n"))
	return string(line)
}
