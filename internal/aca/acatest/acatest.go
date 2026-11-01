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
	// Script is what its shell runs; defaultScript when "".
	Script string
	// Conversation, when set, is the file BackUp takes the transcript from, and
	// Session the one holding the session it says it is in now.
	Conversation string
	Session      string
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
func (a *ACA) BackUp(_ context.Context, l aca.Launch) (string, error) {
	session := l.SessionID
	if data, err := os.ReadFile(a.Session); err == nil {
		session = strings.TrimSpace(string(data))
	}
	data, err := os.ReadFile(a.Conversation)
	if errors.Is(err, os.ErrNotExist) || a.Conversation == "" {
		return session, nil
	}
	if err != nil {
		return "", err
	}
	return session, os.WriteFile(filepath.Join(l.BackupDir, aca.TranscriptFile), data, 0o600)
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
	script := a.Script
	if script == "" {
		script = defaultScript
	}
	return []string{"/bin/sh", "-c", script}, nil
}

// Sessions returns the sessions a's Launches recorded, oldest first.
func (a *ACA) Sessions() ([]string, error) {
	data, err := os.ReadFile(a.Launches)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}
