// Package acatest is an AI coding agent for tests, which run neither a real
// AI CLI nor the network: its program is a shell.
package acatest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/aca"
)

// defaultScript runs the file run in the agent's dir, if there is one, else
// stays until it is hung up on.
const defaultScript = "if [ -f run ]; then . ./run; fi; exec sleep 1000"

// ACA runs Script in /bin/sh, after appending the session it was given to
// the file Launches, one line per start.
type ACA struct {
	// Launches, when set, records each start's session.
	Launches string
	// Notes, when set, records each start's note, one line each.
	Notes string
	// Script is what its shell runs; defaultScript when "".
	Script string
	// Conversation, when set, is the file BackUp takes the transcript from, and
	// Session the one holding the session it says it is in now.
	Conversation string
	Session      string
	// Dir, when set, is the file holding where it says it last worked.
	Dir string
	// Running is what Detached finds, and Stopped what StopDetached took from it.
	Running []aca.DetachedSession
	Stopped []string
}

// ID implements aca.AiCodingAgent.
func (a *ACA) ID() string { return aca.ClaudeCode{}.ID() }

// ScreenParser implements aca.AiCodingAgent: Claude Code's, as a holder goes by
// the id.
func (a *ACA) ScreenParser() aca.ScreenParser { return aca.ClaudeCodeScreenParser{} }

// Deliver implements aca.AiCodingAgent: Claude Code's, as a holder goes by the
// id.
func (a *ACA) Deliver(w io.Writer, message string) error { return aca.ClaudeCode{}.Deliver(w, message) }

// Interrupt implements aca.AiCodingAgent: Claude Code's, as a holder goes by the
// id.
func (a *ACA) Interrupt() []byte { return aca.ClaudeCode{}.Interrupt() }

// BackUp implements aca.AiCodingAgent.
func (a *ACA) BackUp(_ context.Context, l aca.Launch) (aca.Backup, error) {
	backup := aca.Backup{Session: l.SessionID}
	if data, err := os.ReadFile(a.Session); err == nil {
		backup.Session = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(a.Dir); err == nil {
		backup.Dir = strings.TrimSpace(string(data))
	}
	data, err := os.ReadFile(a.Conversation)
	if errors.Is(err, os.ErrNotExist) || a.Conversation == "" {
		return backup, nil
	}
	if err != nil {
		return aca.Backup{}, err
	}
	return backup, os.WriteFile(filepath.Join(l.BackupDir, aca.TranscriptFile), data, 0o600)
}

// BeginSession implements aca.AiCodingAgent: what a says it is in, if it says.
func (a *ACA) BeginSession(_ context.Context, _ aca.Launch, session string) error {
	if a.Session == "" {
		return nil
	}
	return os.WriteFile(a.Session, []byte(session+"\n"), 0o600)
}

// Brief implements aca.AiCodingAgent: Claude Code's, so that a test can give it what
// Claude Code's backup writes.
func (a *ACA) Brief(ctx context.Context, l aca.Launch, limit int) (aca.Brief, error) {
	return aca.ClaudeCode{}.Brief(ctx, l, limit)
}

// Detached implements aca.AiCodingAgent.
func (a *ACA) Detached(context.Context, aca.Launch) ([]aca.DetachedSession, error) {
	return a.Running, nil
}

// StopDetached implements aca.AiCodingAgent.
func (a *ACA) StopDetached(_ context.Context, _ aca.Launch, d aca.DetachedSession) error {
	a.Running = slices.DeleteFunc(a.Running, func(r aca.DetachedSession) bool { return r.ID == d.ID })
	a.Stopped = append(a.Stopped, d.Session)
	return nil
}

// Command implements aca.AiCodingAgent.
func (a *ACA) Command(l aca.Launch) ([]string, error) {
	if a.Launches != "" {
		f, err := os.OpenFile(a.Launches, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := fmt.Fprintln(f, l.SessionID); err != nil {
			return nil, err
		}
	}
	if a.Notes != "" {
		if err := appendLine(a.Notes, l.Note); err != nil {
			return nil, err
		}
	}
	script := a.Script
	if script == "" {
		script = defaultScript
	}
	return []string{"/bin/sh", "-c", script}, nil
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, line)
	return errors.Join(err, f.Close())
}

// NotesGiven returns the notes a's Notes recorded, oldest first.
func (a *ACA) NotesGiven() ([]string, error) {
	data, err := os.ReadFile(a.Notes)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

// Sessions returns the sessions a's Launches recorded, oldest first.
func (a *ACA) Sessions() ([]string, error) {
	data, err := os.ReadFile(a.Launches)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}
