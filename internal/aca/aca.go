// Package aca holds what differs between the AI coding agents wdx runs:
// Claude Code for now, Codex and others later. See docs/AGENT.md.
package aca

import "io"

// AiCodingAgent is one AI CLI wdx can run in an agent.
type AiCodingAgent interface {
	// ID names it in the database and the API, as "claude_code".
	ID() string
	// Command is what to run, first the program; it starts the session or
	// resumes it. It runs through the user's login shell, which finds the
	// program.
	Command(Launch) ([]string, error)
	// ScreenParser reads what it is doing from its screen.
	ScreenParser() ScreenParser
	// Deliver puts one message into its input box on w, its terminal's input,
	// and submits it. Everything a message needs waits here, and the holder runs
	// it apart from what must not wait.
	Deliver(w io.Writer, message string) error
	// Interrupt is the input that stops its turn.
	Interrupt() []byte
}

// ByID is the AI coding agent named id, for what cannot be handed one: the
// holder is a process of its own, told an id. docs/AGENT_STATE.md.
func ByID(id string) (AiCodingAgent, bool) {
	for _, known := range []AiCodingAgent{ClaudeCode{}} {
		if known.ID() == id {
			return known, true
		}
	}
	return nil, false
}

// Launch is one start of an agent.
type Launch struct {
	Dir       string
	SessionID string
	// UserHome is the user's home directory, which holds what an AI CLI
	// keeps.
	UserHome string
}
