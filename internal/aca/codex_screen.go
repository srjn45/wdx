package aca

import (
	"regexp"
	"strings"

	"github.com/srjn45/wdx/internal/screen"
)

// CodexScreenParser reads Codex's screen: whether its box is ready for a message,
// whether the box holds a draft, the model, and what it is doing. Checked on Codex
// 0.163.0; the wdx docs ACA_READY and CODEX_ACTIVITY.
//
// The box is drawn while Codex still loads, and a message submitted then was lost
// (its Tab dropped, when wdx pressed Tab to submit), so the box counts as ready only once
// the status line shows under it, after one blank line:
//
//	› Ask Codex to do anything
//
//	  GPT-6-Sol medium · ~/dev/wdx
//	  ? for shortcuts
//
// While it loads, the line where the status line goes is blank.
//
// What it is doing shows in the lines just above the box: "• Working (5s • esc to
// interrupt)" in a turn, "Worked for 3s" or "■ Conversation interrupted" once it
// ended, "1 background terminal running" while a command it started outlives the
// turn. While it streams an answer nothing says so, and the activity is unknown, for
// the motion fallback to decide.
type CodexScreenParser struct{}

const (
	codexBox   = "› "
	codexShell = "! "
)

var (
	codexWorking = regexp.MustCompile(`^• Working \(\d+[hms]`)
	// A narrow screen cuts the Working line, so a turn is seen only while this shows
	// whole.
	codexInTurn      = regexp.MustCompile(`esc to interrupt\)`)
	codexBackground  = regexp.MustCompile(`^\s+\d+ background terminals? running`)
	codexTurnEnded   = regexp.MustCompile(`^\s+Worked for \d`)
	codexInterrupted = "■ Conversation interrupted"
	// Messages waiting below the Working line: queued for after the turn, or
	// steered into it and not yet taken in.
	codexWaitingMessages = []string{"• Queued follow-up inputs", "• Messages to be submitted after next tool call"}
	// An approval replaces the box.
	codexPrompt = "Press enter to confirm or esc to cancel"
)

// codexEfforts are the words the status line can put after the model's name.
var codexEfforts = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "default": true}

// Parse implements ScreenParser.
func (CodexScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	unknown := screen.ScreenState{Activity: screen.ActivityUnknown}
	if codexWaiting(lines) {
		return screen.ScreenState{Activity: screen.ActivityWaiting}
	}
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
	state := screen.ScreenState{InputBox: true, Model: codexModel(lines[status])}
	state.Activity, state.InTurn = codexActivity(lines[:top])
	// Shell mode runs what the box holds, so it is never a place for a message.
	if strings.HasPrefix(lines[top], strings.TrimSpace(codexShell)) {
		state.Draft = true
		state.DraftText = draftText(undimmed[min(top, len(undimmed)):min(end, len(undimmed))], "")
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
	if state.Draft {
		state.DraftText = draftText(undimmed[top:min(end, len(undimmed))], strings.TrimSpace(codexBox))
	}
	return state
}

// codexWaiting is true when the last lines hold the footer of an approval, which a
// narrow screen wraps.
func codexWaiting(lines []string) bool {
	var last []string
	for i := len(lines) - 1; i >= 0 && len(last) < 3; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			last = append([]string{line}, last...)
		}
	}
	return strings.Contains(strings.Join(last, " "), codexPrompt)
}

// codexActivity reads the block of lines just above the box, past the blocks of
// messages waiting, which sit between it and the box.
func codexActivity(above []string) (screen.Activity, bool) {
	block, start := codexBlockAbove(above)
	for len(block) > 0 && codexMessagesWaiting(block[0]) {
		block, start = codexBlockAbove(above[:start])
	}
	if len(block) == 0 {
		return screen.ActivityUnknown, false
	}
	last := block[len(block)-1]
	switch {
	case codexWorking.MatchString(last):
		return screen.ActivityWorking, codexInTurn.MatchString(last)
	case codexBackground.MatchString(last):
		return screen.ActivityWorking, false
	case codexTurnEnded.MatchString(last):
		return screen.ActivityIdle, false
	}
	// A narrow screen wraps the interrupted line, so it can start a line or two up.
	for i := len(block) - 1; i >= max(0, len(block)-3); i-- {
		if strings.HasPrefix(block[i], codexInterrupted) {
			return screen.ActivityIdle, false
		}
		if strings.HasPrefix(block[i], "•") || strings.HasPrefix(block[i], "›") {
			break
		}
	}
	return screen.ActivityUnknown, false
}

func codexMessagesWaiting(line string) bool {
	for _, heading := range codexWaitingMessages {
		if strings.HasPrefix(line, heading) {
			return true
		}
	}
	return false
}

// codexBlockAbove is the last run of lines that are not blank in above, and where
// it starts.
func codexBlockAbove(above []string) (block []string, start int) {
	end := len(above)
	for end > 0 && strings.TrimSpace(above[end-1]) == "" {
		end--
	}
	start = end
	for start > 0 && strings.TrimSpace(above[start-1]) != "" {
		start--
	}
	return above[start:end], start
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
