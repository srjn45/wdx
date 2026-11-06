package aca

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/srjn45/wdx/internal/screen"
)

// CursorScreenParser reads Cursor's screen: whether its box is ready for a message,
// whether the box holds a draft, the model, and what it is doing. Checked on
// cursor-agent 2026.11.05; the wdx docs CURSOR_PARITY and CURSOR_ACTIVITY.
//
// The screen ends with the box, then, past two blank lines, the footer: a line of
// tasks still running, the status line and the directory:
//
//	⠘⠤ Running  16 tokens
//	   Tip: Use /plan to iterate on an implementation plan before code changes.
//
//
//	→ Add a follow-up                                          ctrl+c to stop
//
//
//	1 task
//	Gemini 3.8 Flash Low · 0.8%                                Run Everything
//	~/w/repo · master
//
// While Cursor loads, the screen is blank; the box and the footer come together. The
// box counts only with the footer under it, as the / menu, an approval's options
// and "Press Ctrl+C again to exit" take the footer's place.
//
// What it is doing: "ctrl+c to stop" at the end of the box's line, or a spinner
// line just above the box, is a turn; a line of tasks under it is a command the
// turn left running; none of them is idle. A follow-up the panel above marks
// "· steering" is a steer pending, which another Enter would cut the turn for. Unlike Codex's, this parser reads idle
// from what is missing, so a Cursor that changes how a turn shows would read idle
// in one.
type CursorScreenParser struct{}

// cursorBoxMarks begin the box's first line: a prompt, a shell command, or a
// cloud agent's prompt.
var cursorBoxMarks = []string{"→", "!", "&"}

var (
	cursorInTurn = regexp.MustCompile(`\sctrl\+c to stop$`)
	cursorTasks  = regexp.MustCompile(`^\d+ tasks?$`)
	// cursorFollowUps begins the panel of queued follow-ups, cursorSteering ends
	// the one being steered.
	cursorFollowUps = "┌─ follow-ups"
	cursorSteering  = "· steering"
	// cursorCloudHint shows under the box in cloud mode.
	cursorCloudHint = "^ "
	// An approval replaces the box and the footer; Esc on it gives a box of its own
	// without the footer.
	cursorPrompts = []string{"Run this command?", "Skip & tell the agent what to do instead", "Tell the agent what to do instead (Enter to send"}
)

// Parse implements ScreenParser.
func (CursorScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	unknown := screen.ScreenState{Activity: screen.ActivityUnknown}
	footer, footerStart := codexBlockAbove(lines)
	if cursorWaiting(footer) {
		return screen.ScreenState{Activity: screen.ActivityWaiting}
	}
	tasks := false
	for len(footer) > 0 {
		line := strings.TrimSpace(footer[0])
		if cursorTasks.MatchString(line) {
			tasks = true
		} else if !strings.HasPrefix(line, cursorCloudHint) {
			break
		}
		footer = footer[1:]
	}
	// The status line, which a narrow screen wraps once, and the directory.
	if len(footer) < 2 || len(footer) > 3 {
		return unknown
	}
	if slices.ContainsFunc(footer, cursorBoxLine) {
		return unknown
	}
	box, top := codexBlockAbove(lines[:footerStart])
	if len(box) == 0 || !cursorBoxLine(box[0]) {
		return unknown
	}
	state := screen.ScreenState{InputBox: true}
	if len(footer) == 2 {
		state.Model = cursorModel(footer[0])
	}
	state.Activity, state.InTurn = cursorActivity(box[0], lines[:top], tasks)
	state.SteerPending = cursorSteerPending(lines[:top])
	// The mark and the placeholder are dim, but for the cursor's cell; what a human
	// typed, and the mark with it, is not.
	if top < len(undimmed) && cursorBoxLine(undimmed[top]) {
		state.Draft = true
	}
	return state
}

// cursorWaiting is true when the last block of the screen is an approval, or the
// box Esc on one gives, whose lines a narrow screen may wrap.
func cursorWaiting(block []string) bool {
	trimmed := make([]string, len(block))
	for i, line := range block {
		trimmed[i] = strings.TrimSpace(line)
	}
	text := strings.Join(trimmed, " ")
	for _, prompt := range cursorPrompts {
		if strings.Contains(text, prompt) {
			return true
		}
	}
	return false
}

// cursorActivity reads the box's line and the block just above the box. The panel
// of follow-ups sits above that block, past a blank line.
func cursorActivity(boxLine string, above []string, tasks bool) (screen.Activity, bool) {
	if cursorInTurn.MatchString(boxLine) {
		return screen.ActivityWorking, true
	}
	// A draft in a turn takes "ctrl+c to stop" off the box's line, but the spinner
	// stays.
	if block, _ := codexBlockAbove(above); len(block) > 0 && cursorSpinner(block[0]) {
		return screen.ActivityWorking, true
	}
	if tasks {
		return screen.ActivityWorking, false
	}
	return screen.ActivityIdle, false
}

// cursorSteerPending is true when the panel of follow-ups marks one steering: Cursor
// takes it at the next tool call or the end of the text it streams, and Enter on it
// meanwhile stops the stream and sends it. The panel is the block just above the
// box, or above the spinner's block in a turn; a narrow screen wraps the mark onto
// a line of its own. A follow-up stuck steering after a human's Ctrl+C stays marked
// until the next turn.
func cursorSteerPending(above []string) bool {
	block, start := codexBlockAbove(above)
	if len(block) > 0 && cursorSpinner(block[0]) {
		block, _ = codexBlockAbove(above[:start])
	}
	if len(block) == 0 || !strings.HasPrefix(strings.TrimSpace(block[0]), cursorFollowUps) {
		return false
	}
	for _, line := range block[1:] {
		if strings.HasSuffix(strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")), cursorSteering) {
			return true
		}
	}
	return false
}

// cursorSpinner is the line that shows a turn: two braille cells, then what Cursor
// is doing, as "⠘⠤ Running  16 tokens".
func cursorSpinner(line string) bool {
	runes := []rune(strings.TrimSpace(line))
	return len(runes) > 2 && unicode.In(runes[0], unicode.Braille) && unicode.In(runes[1], unicode.Braille) && runes[2] == ' '
}

// cursorBoxLine is the box's first line. Lines come trimmed, so an empty one may be
// the mark alone.
func cursorBoxLine(line string) bool {
	line = strings.TrimSpace(line)
	for _, mark := range cursorBoxMarks {
		if line == mark || strings.HasPrefix(line, mark+" ") {
			return true
		}
	}
	return false
}

// cursorModel is the model the status line names, as Gemini 3.8 Flash Low in
// "Gemini 3.8 Flash Low · 0.8%   Run Everything", its spaces as Models gives them,
// or "" when the line does not read so: before the first turn, the status line has
// no " · ", and the model cannot be told from the mode at its right.
func cursorModel(status string) string {
	first, _, ok := strings.Cut(strings.TrimSpace(status), " · ")
	if !ok {
		return ""
	}
	return strings.Join(strings.Fields(first), " ")
}
