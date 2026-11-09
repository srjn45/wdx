package aca

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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
// The footer's right part is dim; its left, what agy is doing, is not, and a narrow
// screen never cuts it:
//
//	? for shortcuts                   at rest
//	esc to cancel                     in a turn, an approval or a menu
//	  Press up to edit queued messages  in a turn, a message waiting to go in
//	(empty)                           a draft in the box, whatever agy does
//
// A turn also shows a spinner just above the box, "⣷  Generating...", which a draft
// does not hide. Work that outlives a turn, a background task or a subagent, shows
// as a row of a panel between the box and the footer, "● [09:54:17] sleep 25
// running", and is counted on the footer's right, "· 1 task(s) · /tasks", which a
// narrow screen cuts. An approval, the trust prompt and the model picker replace
// the box and end with "↑/↓ Navigate · …"; the "/" menu shows it below the box.
//
// The footer is the first line under the box's lower rule, past the panel's rows
// and its rule, and past the "/" menu's hint. What comes under the footer is not
// read: the user's own status line, which wdx runs with its own and agy draws there
// (the wdx doc ANTIGRAVITY_CONTEXT).
type AntigravityScreenParser struct{}

const (
	antigravityBox  = "> "
	antigravityRule = '─'

	antigravityAtRest   = "? for shortcuts"
	antigravityMenuHint = "↑/↓ Navigate"
	antigravityPanelRow = "● "
)

var (
	// What the footer's left says in a turn.
	antigravityInTurn = []string{"esc to cancel", "Press up to edit queued messages"}
	// The footer's right part counts background work.
	antigravityBackground = regexp.MustCompile(`· \d+ (task|subagent)\(s\)`)
)

// Parse implements ScreenParser.
func (AntigravityScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	unknown := screen.ScreenState{Activity: screen.ActivityUnknown}
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 {
		return unknown
	}
	top, end := antigravityFindBox(lines[:last])
	hint := antigravityMenu(lines[:last+1])
	if top < 0 {
		if hint >= 0 {
			return screen.ScreenState{Activity: screen.ActivityWaiting}
		}
		return unknown
	}
	footer := antigravityFooter(lines[:last+1], end+1, hint)
	if footer < 0 || footer >= len(undimmed) {
		return unknown
	}
	state := unknown
	// The "/" menu's footer says esc to cancel at rest too.
	if hint < 0 {
		state.Activity, state.InTurn = antigravityActivity(lines[:top], lines[end+1:footer], lines[footer], undimmed[footer])
	}
	model := antigravityModel(lines[footer], undimmed[footer])
	if model == "" {
		return state
	}
	state.InputBox, state.Model = true, model
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
	if state.Draft {
		state.DraftText = draftText(undimmed[top:min(end, len(undimmed))], strings.TrimSpace(antigravityBox))
	}
	return state
}

// antigravityMenu is the line among the last three that ends a menu or a prompt to
// choose from, with the hint a narrow screen cuts; -1 when none does.
func antigravityMenu(lines []string) int {
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 3; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if strings.Contains(lines[i], antigravityMenuHint) {
			return i
		}
	}
	return -1
}

// antigravityFooter is the footer's line: the first from below, the line under the
// box's lower rule, that is not blank, a rule or a row of the panel, or the first
// after the "/" menu's hint when it is at hint; -1 when there is none.
func antigravityFooter(lines []string, below, hint int) int {
	if hint >= below {
		below = hint + 1
	}
	for i := below; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || antigravityRuleLine(lines[i]) || strings.HasPrefix(line, antigravityPanelRow) {
			continue
		}
		return i
	}
	return -1
}

// antigravityActivity reads what agy is doing from the footer, the lines above the
// box and its upper rule, and those between the box's lower rule and the footer.
func antigravityActivity(above, below []string, footer, undimmed string) (screen.Activity, bool) {
	left := strings.TrimSpace(undimmed)
	if slices.Contains(antigravityInTurn, left) || antigravitySpinner(above) {
		return screen.ActivityWorking, true
	}
	if antigravityBackground.MatchString(footer) {
		return screen.ActivityWorking, false
	}
	for _, line := range below {
		if strings.HasPrefix(strings.TrimSpace(line), antigravityPanelRow) {
			return screen.ActivityWorking, false
		}
	}
	if left == antigravityAtRest {
		return screen.ActivityIdle, false
	}
	return screen.ActivityUnknown, false
}

// antigravitySpinner is true when the block of lines just above the box, its upper
// rule and what agy shows over it, holds the spinner: a braille character starting
// a line, which only tips and messages waiting to go in follow. An answer's lines
// are indented.
func antigravitySpinner(above []string) bool {
	for i := len(above) - 1; i >= 0 && strings.TrimSpace(above[i]) != ""; i-- {
		if r, _ := utf8.DecodeRuneInString(above[i]); unicode.In(r, unicode.Braille) {
			return true
		}
	}
	return false
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
