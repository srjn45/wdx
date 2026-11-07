package aca

import "strings"

// escapeBang puts a \ before a message's first ! when only whitespace comes before
// it, so that the message reaches the model: Claude Code, Codex and Antigravity run
// a box starting with ! as a shell command themselves, without approval, and never
// show it to the model (the wdx doc LEADING_BANG). A message the sender wrote as
// \!x gets a second \, so the model reads the sender's own. Every ACA's typing
// function calls it, after the paste-end marker is taken out.
func escapeBang(message string) string {
	rest := strings.TrimLeft(message, " \t\r\n")
	if !strings.HasPrefix(strings.TrimLeft(rest, `\`), "!") {
		return message
	}
	return message[:len(message)-len(rest)] + `\` + rest
}
