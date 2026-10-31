package aca

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/screen"
)

// ClaudeCode is Anthropic's Claude Code CLI.
type ClaudeCode struct{}

// ID implements AiCodingAgent.
func (ClaudeCode) ID() string { return "claude_code" }

// Command implements AiCodingAgent. A session is resumed only once it has a
// transcript: Claude Code writes none until something is typed, and resuming
// such a session fails with "No conversation found".
func (ClaudeCode) Command(l Launch) ([]string, error) {
	transcripts, err := filepath.Glob(filepath.Join(l.UserHome, ".claude", "projects", "*", l.SessionID+".jsonl"))
	if err != nil {
		return nil, fmt.Errorf("look for the transcript of session %s: %w", l.SessionID, err)
	}
	if len(transcripts) > 0 {
		return []string{"claude", "--resume", l.SessionID}, nil
	}
	return []string{"claude", "--session-id", l.SessionID}, nil
}

// Bracketed paste keeps a message's newlines content, not Enter.
const (
	claudeCodePasteStart = "\x1b[200~"
	claudeCodePasteEnd   = "\x1b[201~"
	// claudeCodePasteSettle is the wait before Enter, which can be dropped while
	// a paste settles.
	claudeCodePasteSettle = 150 * time.Millisecond
)

// Deliver implements AiCodingAgent. A paste-end marker in the message is taken
// out, or the rest would be typed as keys.
func (ClaudeCode) Deliver(w io.Writer, message string) error {
	for strings.Contains(message, claudeCodePasteEnd) {
		message = strings.ReplaceAll(message, claudeCodePasteEnd, "")
	}
	if _, err := io.WriteString(w, claudeCodePasteStart+message+claudeCodePasteEnd); err != nil {
		return err
	}
	time.Sleep(claudeCodePasteSettle)
	_, err := io.WriteString(w, "\r")
	return err
}

// Interrupt implements AiCodingAgent.
func (ClaudeCode) Interrupt() []byte { return []byte("\x1b") }

// ScreenParser implements AiCodingAgent.
func (ClaudeCode) ScreenParser() ScreenParser { return ClaudeCodeScreenParser{} }

// ClaudeCodeScreenParser reads Claude Code's state from the bottom of its
// screen, since its replies can hold any of the words it looks for. See
// docs/AGENT_STATE.md.
type ClaudeCodeScreenParser struct{}

var (
	claudeCodeBackgroundWork = regexp.MustCompile(`\b\d+ shells?\b|↓ to manage`)
	claudeCodeAtRest         = regexp.MustCompile(`\? for shortcuts|← 1 agent|← for agents`)
	// Claude drops these from the footer while the box holds text. The count
	// and a cut-off word stay in, as "← 1 ag…".
	claudeCodeNoDraft = regexp.MustCompile(`\? for shortcuts|← \d+ ag|← for agents`)
	// A prompt's option has a plain space after the ❯ and the box a no-break
	// one, so a draft that reads "1. buy milk" stays a draft.
	claudeCodeOption = regexp.MustCompile(`^❯ +\d+\.\s`)
)

// Parse implements ScreenParser.
func (ClaudeCodeScreenParser) Parse(lines, undimmed []string) screen.ScreenState {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return screen.ScreenState{Activity: screen.ActivityUnknown}
	}
	tail := claudeCodeTail(lines)
	if strings.Contains(strings.Join(tail[max(0, len(tail)-2):], " "), "Esc to cancel") {
		return screen.ScreenState{Activity: screen.ActivityWaiting}
	}
	box, ok := claudeCodeInputBox(lines)
	if !ok {
		// A multi-line draft's footer has this too, under the box's rule, even
		// when the box's top has scrolled away. Plan approval has no such rule.
		if len(tail) == min(len(lines), claudeCodeTailLines) && strings.Contains(strings.Join(tail, " "), "ctrl+g to edit in") {
			return screen.ScreenState{Activity: screen.ActivityWaiting}
		}
		return screen.ScreenState{Activity: screen.ActivityUnknown}
	}
	inTurn := strings.Contains(box.footer, "esc to interrupt")
	state := screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true, Draft: box.hasDraft(undimmed), InTurn: inTurn}
	switch {
	case inTurn,
		claudeCodeBackgroundWork.MatchString(box.footer),
		box.subagentRow:
		state.Activity = screen.ActivityWorking
	case claudeCodeAtRest.MatchString(box.footer):
		state.Activity = screen.ActivityIdle
	}
	return state
}

const claudeCodeTailLines = 3

// claudeCodeTail is the last lines, which a narrow screen wraps a prompt's
// footer onto, from below the last rule: above it is what the user typed into
// an input box.
func claudeCodeTail(lines []string) []string {
	var tail []string
	for _, line := range lines[max(0, len(lines)-claudeCodeTailLines):] {
		if isClaudeCodeRule(line) {
			tail = nil
			continue
		}
		tail = append(tail, strings.TrimSpace(line))
	}
	return tail
}

type claudeCodeBox struct {
	// first and last are the lines between the box's two rules.
	first, last int
	// footer is what is just below the box: its lines up to a blank one,
	// which starts the panel of subagents, joined.
	footer string
	// subagentRow is a ◯ row in the panel, which a cut-off footer cannot hide.
	subagentRow bool
	// bashMode is a box whose first line starts with !.
	bashMode bool
}

// hasDraft looks for text that is not dim in the box: Claude dims what it puts
// there itself, a placeholder or a suggestion. Without color nothing is dim, so
// a footer that only shows with no text in the box overrules it. In bash mode a
// message would run as a shell command, so even the ! alone counts.
func (b claudeCodeBox) hasDraft(undimmed []string) bool {
	if b.bashMode {
		return true
	}
	if claudeCodeNoDraft.MatchString(b.footer) {
		return false
	}
	for i := b.first; i <= b.last && i < len(undimmed); i++ {
		text := strings.TrimLeft(undimmed[i], " \u00a0")
		if i == b.first {
			text = strings.TrimPrefix(strings.TrimPrefix(text, "❯"), "!")
		}
		if strings.Trim(text, " \u00a0") != "" {
			return true
		}
	}
	return false
}

// claudeCodeInputBox finds the input box nearest the bottom, a line starting ❯,
// or ! in bash mode, between two rules. A numbered option under a ❯ belongs to
// a prompt, whatever its footer says.
func claudeCodeInputBox(lines []string) (claudeCodeBox, bool) {
	closing := len(lines) - 1
	for closing >= 0 && !isClaudeCodeRule(lines[closing]) {
		closing--
	}
	opening := closing - 1
	hasPrompt := false
	for opening >= 0 && !isClaudeCodeRule(lines[opening]) {
		line := lines[opening]
		if claudeCodeOption.MatchString(line) {
			return claudeCodeBox{}, false
		}
		hasPrompt = hasPrompt || strings.HasPrefix(line, "❯") || strings.HasPrefix(line, "!")
		opening--
	}
	if opening < 0 || !hasPrompt {
		return claudeCodeBox{}, false
	}
	box := claudeCodeBox{first: opening + 1, last: closing - 1, bashMode: strings.HasPrefix(lines[opening+1], "!")}
	var footer []string
	inFooter := true
	for _, line := range lines[closing+1:] {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			inFooter = false
		case inFooter:
			footer = append(footer, line)
		case strings.HasPrefix(line, "◯"):
			box.subagentRow = true
		}
	}
	box.footer = strings.Join(footer, " ")
	return box, true
}

func isClaudeCodeRule(line string) bool {
	return strings.HasPrefix(line, "─") && strings.HasSuffix(line, "─")
}
