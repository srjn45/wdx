package aca

import (
	"strings"

	"github.com/srjn45/wdx/internal/screen"
)

// CodexScreenParser reads from Codex's screen only what the inbox needs to deliver
// safely: whether its box is ready for a message, whether the box holds a draft, and
// the model. The activity is the motion fallback's. Checked on Codex 0.163.0; the
// wdx doc ACA_READY.
//
// The box is drawn while Codex still loads, and a Tab typed then is lost, so the box
// counts as ready only once the status line shows under it, after one blank line:
//
//	› Ask Codex to do anything
//
//	  GPT-6-Sol medium · ~/dev/wdx
//	  ? for shortcuts
//
// While it loads, the line where the status line goes is blank.
type CodexScreenParser struct{}

const (
	codexBox   = "› "
	codexShell = "! "
)

// codexEfforts are the words the status line can put after the model's name.
var codexEfforts = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "default": true}

// Parse implements ScreenParser.
func (CodexScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	unknown := screen.ScreenState{Activity: screen.ActivityUnknown}
	top := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexBoxLine(lines[i]) {
			top = i
			break
		}
	}
	if top < 0 {
		return unknown
	}
	// A draft of several lines goes on below the first, up to a blank line.
	end := top + 1
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	status := end + 1
	if status >= len(lines) || strings.TrimSpace(lines[status]) == "" {
		return unknown
	}
	state := screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true, Model: codexModel(lines[status])}
	// Shell mode runs what the box holds, so it is never a place for a message.
	if strings.HasPrefix(lines[top], strings.TrimSpace(codexShell)) {
		state.Draft = true
		return state
	}
	// The placeholder is dim, so the undimmed box is blank unless a human typed.
	for i := top; i < end && i < len(undimmed); i++ {
		text := undimmed[i]
		if i == top {
			// Lines come trimmed, so an empty box's is the mark alone.
			text = strings.TrimPrefix(text, strings.TrimSpace(codexBox))
		}
		if strings.TrimSpace(text) != "" {
			state.Draft = true
		}
	}
	return state
}

// codexBoxLine is the first line of the box, in shell mode too. Lines come trimmed,
// so an empty one is the mark alone.
func codexBoxLine(line string) bool {
	for _, mark := range []string{codexBox, codexShell} {
		if line == strings.TrimSpace(mark) || strings.HasPrefix(line, mark) {
			return true
		}
	}
	return false
}

// codexModel is the model the status line names, as GPT-6-Sol in
// "GPT-6-Sol medium · ~/dev/wdx", or "" when the line does not read so.
func codexModel(status string) string {
	first, _, ok := strings.Cut(strings.TrimSpace(status), " · ")
	if !ok {
		return ""
	}
	words := strings.Fields(first)
	if len(words) > 1 && codexEfforts[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}
