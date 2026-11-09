package aca

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/screen"
)

// ClaudeCode is Anthropic's Claude Code CLI.
type ClaudeCode struct{}

// ClaudeCodeID is ClaudeCode's ID.
const ClaudeCodeID = "claude_code"

// ID implements AiCodingAgent.
func (ClaudeCode) ID() string { return ClaudeCodeID }

// Program implements AiCodingAgent.
func (ClaudeCode) Program() string { return "claude" }

// Models implements AiCodingAgent. Claude Code has no command that lists its models,
// so these are its aliases, each the newest of its class. None is its default, which
// is the user's own setting.
func (ClaudeCode) Models(context.Context, Launch) ([]Model, error) {
	return []Model{{ID: "fable", Name: "Fable"}, {ID: "opus", Name: "Opus"}, {ID: "sonnet", Name: "Sonnet"}, {ID: "haiku", Name: "Haiku"}}, nil
}

// claudeCodeEnv runs Claude Code on its classic screen, without the mouse. On
// its alternate screen it asks for the mouse, so tide gives it every drag and
// tide's own selection never starts, and its own copy does not reach the
// clipboard of a terminal that ignores OSC 52. On its classic screen the wheel
// scrolls the terminal's history in tide, as for any program.
var claudeCodeEnv = []string{"env", "CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=1", "CLAUDE_CODE_DISABLE_MOUSE=1"}

// Command implements AiCodingAgent. A session is resumed only once it has a
// transcript: Claude Code writes none until something is typed, and resuming
// such a session fails with "No conversation found". One Claude Code has
// deleted comes back from the backup.
func (ClaudeCode) Command(ctx context.Context, l Launch) ([]string, error) {
	command := append(slices.Clone(claudeCodeEnv), "claude", "--session-id", l.SessionID)
	resume, err := claudeCodeTranscriptAt(l)
	if err != nil {
		return nil, fmt.Errorf("look for the transcript of session %s: %w", l.SessionID, err)
	}
	if resume {
		command = append(slices.Clone(claudeCodeEnv), "claude", "--resume", l.SessionID)
	}
	if l.Model != "" {
		command = append(command, "--model", l.Model)
	}
	if l.Permissions == PermissionsFull {
		command = append(command, "--permission-mode", "bypassPermissions")
	}
	if l.Note != "" {
		// Unlike a first message, it outlives a compaction. A resumed conversation keeps the
		// system prompt it recorded until then (--system-prompt-snapshot, 2.1.317).
		command = append(command, "--append-system-prompt", l.Note)
	}
	if l.BackupDir == "" {
		return command, nil
	}
	settings, err := claudeCodeWriteSettings(ctx, l)
	if err != nil {
		return nil, fmt.Errorf("write the settings of its backup: %w", err)
	}
	return append(command, "--settings", settings), nil
}

// Bracketed paste keeps a message's newlines content, not Enter.
const (
	claudeCodePasteStart = "\x1b[200~"
	claudeCodePasteEnd   = "\x1b[201~"
	// claudeCodeSendNow is Ctrl+X Ctrl+S, which sends what is queued now.
	claudeCodeSendNow = "\x18\x13"
)

// Paste implements AiCodingAgent. A paste-end marker in the message is taken out,
// or the rest would be typed as keys.
func (ClaudeCode) Paste(w io.Writer, message string) error {
	for strings.Contains(message, claudeCodePasteEnd) {
		message = strings.ReplaceAll(message, claudeCodePasteEnd, "")
	}
	message = escapeBang(message)
	_, err := io.WriteString(w, claudeCodePasteStart+message+claudeCodePasteEnd)
	return err
}

// Submit implements AiCodingAgent: Enter, which queues the message during a turn,
// for Claude to take in at the next tool result, and submits it at rest. An Enter
// that comes while a paste settles is taken as part of it.
func (ClaudeCode) Submit(w io.Writer, _ func()) error {
	_, err := io.WriteString(w, "\r")
	return err
}

// SendNow implements AiCodingAgent: Ctrl+X Ctrl+S, which sends the queued messages
// to the running turn at once. It moves a foreground command to the background,
// cuts an answer being streamed and drops thinking; with nothing queued, at rest or
// in a turn, it does nothing, and in the same write as Enter it is lost. Checked on
// 2.1.319 (the wdx doc CLAUDE_STEER).
func (ClaudeCode) SendNow(w io.Writer) error {
	_, err := io.WriteString(w, claudeCodeSendNow)
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
	// A model's name is a word and a version, so that a name a wrap has cut is not
	// read as a shorter one, and what leaves the holder cannot be other text.
	claudeCodeBannerModel  = regexp.MustCompile(`^▝▜[^A-Za-z]*` + claudeCodeModelName + `(?:\s+\(.*\))?(?:\s+·.*)?$`)
	claudeCodeChangedModel = regexp.MustCompile(`^⎿\s+(?:Set model to|Kept model as) ` + claudeCodeModelName + `(?:\s|$)`)
	claudeCodeModelCommand = regexp.MustCompile(`^❯[ \x{00a0}]/model(?:\s|$)`)
)

const claudeCodeModelName = `([A-Z][a-z]+ \d+(?:\.\d+)?)`

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
		return screen.ScreenState{Activity: screen.ActivityWaiting, Prompt: claudeCodePrompt(lines)}
	}
	box, ok := claudeCodeInputBox(lines)
	if !ok {
		// A multi-line draft's footer has this too, under the box's rule, even
		// when the box's top has scrolled away. Plan approval has no such rule.
		if len(tail) == min(len(lines), claudeCodeTailLines) && strings.Contains(strings.Join(tail, " "), "ctrl+g to edit in") {
			return screen.ScreenState{Activity: screen.ActivityWaiting, Prompt: claudeCodePrompt(lines)}
		}
		return screen.ScreenState{Activity: screen.ActivityUnknown}
	}
	inTurn := strings.Contains(box.footer, "esc to interrupt")
	state := screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true, Draft: box.hasDraft(undimmed), InTurn: inTurn}
	if state.Draft {
		state.DraftText = box.text(undimmed)
	} else {
		state.Model = claudeCodeModel(lines[:box.first])
	}
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

// claudeCodeModel reads the model from what is above the input box, lowest
// first: a confirmation is below the banner, which /model opus[1m] leaves as it
// was. Only with the box showing and empty, and at the bottom of the conversation:
// the transcript view and a screen scrolled back show old confirmations, and the
// suggestions of a command being typed cover the newest.
func claudeCodeModel(above []string) string {
	for i := len(above) - 1; i > 0; i-- {
		line := strings.TrimSpace(above[i])
		switch {
		case strings.Contains(line, "Jump to bottom"):
			return ""
		case strings.HasPrefix(line, "⎿"):
			if m := claudeCodeChangedModel.FindStringSubmatch(line); m != nil && claudeCodeModelCommand.MatchString(above[i-1]) {
				return m[1]
			}
		case strings.HasPrefix(line, "▝▜"):
			if m := claudeCodeBannerModel.FindStringSubmatch(line); m != nil && strings.HasPrefix(strings.TrimSpace(above[i-1]), "▐▛") {
				return m[1]
			}
		}
	}
	return ""
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

// text is what the box shows that is not dim, without the ❯. The ! of bash mode
// stays, as it is part of what would run.
func (b claudeCodeBox) text(undimmed []string) string {
	return draftText(undimmed[min(b.first, len(undimmed)):min(b.last+1, len(undimmed))], "❯")
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
