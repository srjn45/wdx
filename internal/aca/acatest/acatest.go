// Package acatest is an AI coding agent for tests, which run neither a real
// AI CLI nor the network: its program is a shell.
package acatest

import (
	"fmt"
	"os"
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
}

// ID implements aca.AiCodingAgent.
func (a *ACA) ID() string { return aca.ClaudeCode{}.ID() }

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
