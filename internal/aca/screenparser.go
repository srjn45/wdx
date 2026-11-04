package aca

import "github.com/srjn45/wdx/internal/screen"

// ScreenParser reads an ACA's state from its screen. It keeps no state: the
// result is a pure function of the lines.
type ScreenParser interface {
	// Parse takes the plain text of the screen's lines, top first, and the
	// same lines with their dim cells blank.
	Parse(lines, undimmed []string) screen.ScreenState
}

// NoScreenParser is the parser of an ACA that has none of its own yet: it reads
// nothing from the screen but an input box, so that the motion fallback decides
// the activity, a still screen reading idle and a moving one working, and the
// inbox can deliver. docs/AGENT_STATE.md.
type NoScreenParser struct{}

// Parse implements ScreenParser.
func (NoScreenParser) Parse(_, _ []string) screen.ScreenState {
	return screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true}
}
