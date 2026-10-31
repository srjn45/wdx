package aca

import "github.com/srjn45/wdx/internal/screen"

// ScreenParser reads an ACA's state from its screen. It keeps no state: the
// result is a pure function of the lines.
type ScreenParser interface {
	// Parse takes the plain text of the screen's lines, top first, and the
	// same lines with their dim cells blank.
	Parse(lines, undimmed []string) screen.ScreenState
}
