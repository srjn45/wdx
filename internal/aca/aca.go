// Package aca holds what differs between the AI coding agents wdx runs:
// Claude Code for now, Codex and others later. See docs/AGENT.md.
package aca

// AiCodingAgent is one AI CLI wdx can run in an agent.
type AiCodingAgent interface {
	// ID names it in the database and the API, as "claude_code".
	ID() string
	// Command is what to run, first the program; it starts the session or
	// resumes it. It runs through the user's login shell, which finds the
	// program.
	Command(Launch) ([]string, error)
}

// Launch is one start of an agent.
type Launch struct {
	Dir       string
	SessionID string
	// UserHome is the user's home directory, which holds what an AI CLI
	// keeps.
	UserHome string
}
