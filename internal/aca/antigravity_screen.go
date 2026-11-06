package aca

import (
	"strings"

	"github.com/srjn45/wdx/internal/screen"
)

// AntigravityScreenParser reads Antigravity's screen: whether its box is ready for
// a message, whether the box holds a draft, and the model. Checked on agy 1.5.2; the
// wdx doc ANTIGRAVITY_PARITY.
//
// The box sits between two rules, with the footer at the bottom of the screen, a
// panel of background work between them at times:
//
//	────────────────────────────────────────
//	> a draft
//	────────────────────────────────────────
//	? for shortcuts   Gemini 3.8 Flash · low
//
// The box is drawn 0.4 s after start, the model on the footer's right a second
// later, and the box counts as ready only then, as a margin on a loaded machine.
// The footer's right part is dim; its left, what agy is doing, is not.
//
// What it is doing is left to the motion fallback for now.
type AntigravityScreenParser struct{}

const (
	antigravityBox  = "> "
	antigravityRule = '─'
)

// Parse implements ScreenParser.
func (AntigravityScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	unknown := screen.ScreenState{Activity: screen.ActivityUnknown}
	footer := len(lines) - 1
	for footer >= 0 && strings.TrimSpace(lines[footer]) == "" {
		footer--
	}
	if footer < 0 || footer >= len(undimmed) {
		return unknown
	}
	model := antigravityModel(lines[footer], undimmed[footer])
	if model == "" {
		return unknown
	}
	top, end := antigravityFindBox(lines[:footer])
	if top < 0 {
		return unknown
	}
	state := screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true, Model: model}
	for i := top; i < end && i < len(undimmed); i++ {
		text := undimmed[i]
		if i == top {
			// Lines come trimmed, so an empty box's is the mark alone.
			text = strings.TrimPrefix(text, strings.TrimSpace(antigravityBox))
		}
		if strings.TrimSpace(text) != "" {
			state.Draft = true
		}
	}
	return state
}

// antigravityFindBox is where the box's lines start and end in above: the last line
// starting with the mark whose lines, the first and those indented below it, end at
// a rule. An earlier message shows as such a line too, but the answer or a blank
// line follows it, and an approval's or a menu's selected entry has no rule below.
func antigravityFindBox(above []string) (top, end int) {
	for top = len(above) - 1; top >= 0; top-- {
		line := above[top]
		if line != strings.TrimSpace(antigravityBox) && !strings.HasPrefix(line, antigravityBox) {
			continue
		}
		end = top + 1
		for end < len(above) && strings.HasPrefix(above[end], "  ") {
			end++
		}
		if end < len(above) && antigravityRuleLine(above[end]) {
			return top, end
		}
	}
	return -1, -1
}

func antigravityRuleLine(line string) bool {
	return line != "" && strings.Trim(line, string(antigravityRule)) == ""
}

// antigravityModel is the model the footer's dim right part names, as Gemini 3.8
// Flash in "Gemini 3.8 Flash · low · 1 task(s) · /tasks", or "" when it names none:
// while agy starts, or when a narrow screen cuts it.
func antigravityModel(footer, undimmed string) string {
	right, ok := strings.CutPrefix(footer, undimmed)
	if !ok {
		return ""
	}
	model, _, ok := strings.Cut(strings.TrimSpace(right), " · ")
	if !ok {
		return ""
	}
	return model
}
