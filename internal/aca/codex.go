package aca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Codex is OpenAI's Codex CLI: the wdx docs ACA_CODEX, ACA_READY, CODEX_ACTIVITY,
// CODEX_STEER, CODEX_RESUME, CODEX_BACKUP and CODEX_BRIEF.
type Codex struct {
	NoDetached
}

// CodexID is Codex's ID.
const CodexID = "codex"

// ID implements AiCodingAgent.
func (Codex) ID() string { return CodexID }

// Program implements AiCodingAgent.
func (Codex) Program() string { return "codex" }

// Command implements AiCodingAgent. Codex names its own sessions: l.SessionID is
// resumed once it names one Codex keeps, which BackUp makes it after the first turn,
// and before that a start begins a new conversation. One Codex deleted is put back
// from the backup's copy, where Codex kept it, and resumed. The overrides last for
// this run only: Codex writes none of them to the user's config. It is told not to
// offer an update and that l.Dir is trusted, so that it asks nothing before it
// starts, and to report each turn to the backup.
func (Codex) Command(_ context.Context, l Launch) ([]string, error) {
	command := []string{"codex"}
	session, err := codexSessionFile(l.UserHome, l.SessionID)
	if err != nil {
		return nil, fmt.Errorf("look for Codex's session %s: %w", l.SessionID, err)
	}
	resume := session != ""
	if !resume {
		if resume, err = codexRestore(l); err != nil {
			return nil, fmt.Errorf("put back Codex's session %s from its backup: %w", l.SessionID, err)
		}
	}
	if resume {
		command = append(command, "resume", l.SessionID)
	}
	command = append(command,
		"-c", "check_for_update_on_startup=false",
		// A table, as Codex splits a dotted key at every ".", which paths have. It
		// replaces the user's projects for this run, which hold only their trust.
		"-c", "projects={"+tomlString(l.Dir)+"={trust_level=\"trusted\"}}",
	)
	if l.BackupDir != "" {
		// It replaces the user's own notify for this run (the wdx doc CODEX_RESUME).
		command = append(command, "-c", codexNotify(l.BackupDir))
	}
	if l.Model != "" {
		command = append(command, "-m", l.Model)
	}
	if l.Permissions == PermissionsFull {
		command = append(command, "--dangerously-bypass-approvals-and-sandbox")
	}
	if l.Note != "" {
		// Added to Codex's own instructions, which base_instructions would replace.
		command = append(command, "-c", "developer_instructions="+tomlString(l.Note))
	}
	return command, nil
}

// tomlString is s as a TOML basic string: JSON's escapes are TOML's.
func tomlString(s string) string {
	quoted, _ := json.Marshal(s) // a string always marshals
	return string(quoted)
}

const (
	codexPasteStart = "\x1b[200~"
	codexPasteEnd   = "\x1b[201~"
	// codexPasteSettle keeps the key apart from the paste: Codex takes keys that come
	// fast as a paste, where Enter is a newline.
	codexPasteSettle = 150 * time.Millisecond
	// codexSubmit is Enter, which gives a message to the running turn at its next
	// step, and submits it at rest. Tab would queue it for after the turn.
	codexSubmit = "\r"
)

// Deliver implements AiCodingAgent: the message is read at the turn's next step, as
// Claude Code reads one. A paste-end marker in the message is taken out, or the rest
// would be typed as keys, and a message starting with "/" gets a space before it, or
// Codex reads it as a command and submits nothing.
func (Codex) Deliver(w io.Writer, message string) error {
	return codexType(w, message)
}

// Steer implements AiCodingAgent: the same keys as Deliver, as Enter already steers.
// Checked on 0.163.0 while Codex thinks, runs a tool and streams, and with messages
// queued, which it keeps for after the turn (the wdx doc CODEX_STEER).
func (Codex) Steer(w io.Writer, message string) error {
	return codexType(w, message)
}

// codexType pastes message, then presses Enter once the paste has settled.
func codexType(w io.Writer, message string) error {
	for strings.Contains(message, codexPasteEnd) {
		message = strings.ReplaceAll(message, codexPasteEnd, "")
	}
	if strings.HasPrefix(message, "/") {
		message = " " + message
	}
	if _, err := io.WriteString(w, codexPasteStart+message+codexPasteEnd); err != nil {
		return err
	}
	time.Sleep(codexPasteSettle)
	_, err := io.WriteString(w, codexSubmit)
	return err
}

// ScreenParser implements AiCodingAgent.
func (Codex) ScreenParser() ScreenParser { return CodexScreenParser{} }

// Interrupt implements AiCodingAgent.
func (Codex) Interrupt() []byte { return []byte("\x1b") }

// Models implements AiCodingAgent: what `codex debug models` lists, leaving out the
// hidden ones. None says it is the default, which is the user's own setting.
func (Codex) Models(ctx context.Context, l Launch) ([]Model, error) {
	out, err := claudeCodeRun(ctx, l, "codex debug models")
	if err != nil {
		return nil, err
	}
	// A login shell's startup files can print before it.
	if at := bytes.IndexByte(out, '{'); at > 0 {
		out = out[at:]
	}
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out, &catalog); err != nil {
		return nil, fmt.Errorf("read codex debug models: %w", err)
	}
	var models []Model
	for _, m := range catalog.Models {
		if m.Visibility == "list" && m.Slug != "" {
			models = append(models, Model{ID: m.Slug, Name: m.DisplayName})
		}
	}
	return models, nil
}
