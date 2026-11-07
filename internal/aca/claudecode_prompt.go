package aca

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/srjn45/wdx/internal/screen"
)

// claudeCodePromptKind is which of Claude Code's prompts a footer belongs to.
type claudeCodePromptKind int

const (
	claudeCodePermission claudeCodePromptKind = iota
	claudeCodePlanApproval
	claudeCodeTrust
	claudeCodeDecision
)

var (
	// claudeCodeNumbered is an option as "❯ 1. Yes", "  2. [ ] Pear": the
	// cursor, the number, the box of a checkbox and the label.
	claudeCodeNumbered  = regexp.MustCompile(`^(\s*(?:❯\s+)?)(\d+)\.\s+(\[[ ✔]\]\s+)?(.*?)\s*$`)
	claudeCodeQuestion  = regexp.MustCompile(`^Do you want\b.*\?$`)
	claudeCodeTabName   = regexp.MustCompile(`[☐☒] (.+?)(?:\s{2,}|$)`)
	claudeCodePlanAsks  = "Claude has written up a plan"
	claudeCodeWorkspace = "Accessing workspace:"
	claudeCodeCommand   = " command"
)

// claudeCodePrompt reads the prompt at the bottom of a screen that reads
// waiting, lines without trailing blanks: its kind from the footer and the
// line above the options, then the options, the question, the title and the
// detail (docs/AGENT_STATE.md, Why it is waiting). nil when the screen does not
// show one it can read, so a kind is never guessed.
func claudeCodePrompt(lines []string) *screen.Prompt {
	footerStart := len(lines)
	for footerStart > 0 && !isBlank(lines[footerStart-1]) {
		footerStart--
	}
	if footerStart == 0 || footerStart == len(lines) {
		return nil
	}
	footer := strings.Join(trimmed(lines[footerStart:]), " ")
	var kind claudeCodePromptKind
	switch {
	case strings.Contains(footer, "Enter to select"):
		kind = claudeCodeDecision
	case strings.Contains(footer, "Enter to confirm"):
		kind = claudeCodeTrust
	case strings.Contains(footer, "ctrl+g to edit in"):
		kind = claudeCodePlanApproval
	case strings.Contains(footer, "Esc to cancel"):
		kind = claudeCodePermission
	default:
		return nil
	}
	// below is the blank line under the options.
	below := footerStart - 1
	if kind == claudeCodeTrust {
		return claudeCodeTrustPrompt(lines, below)
	}
	first, column := claudeCodeFirstOption(lines, below, kind == claudeCodeDecision)
	if first < 0 {
		return nil
	}
	prompt := &screen.Prompt{Kind: screen.PromptYesNo, Selected: -1, Ticked: []int{}}
	if kind == claudeCodeDecision {
		prompt.Kind = screen.PromptDecision
	}
	for _, line := range lines[first:below] {
		m := claudeCodeNumbered.FindStringSubmatch(line)
		switch {
		case m != nil && utf8.RuneCountInString(m[1]) == column && m[2] == strconv.Itoa(len(prompt.Options)+1):
			if strings.Contains(m[1], "❯") {
				prompt.Selected = len(prompt.Options)
			}
			prompt.Multi = prompt.Multi || m[3] != ""
			if strings.Contains(m[3], "✔") {
				prompt.Ticked = append(prompt.Ticked, len(prompt.Options))
			}
			prompt.Options = append(prompt.Options, m[4])
		case isClaudeCodeRule(line), kind == claudeCodeDecision, kind == claudeCodePlanApproval:
			// A decision's description under its option, the rule before its last
			// option, and the plan's key hint under its last are not labels.
		default:
			// The rest of a label the width wrapped.
			prompt.Options[len(prompt.Options)-1] += " " + strings.TrimSpace(line)
		}
	}
	// The question is the lines right above the options, which a narrow
	// screen wraps; a decision and the plan approval have a blank line between.
	above := first - 1
	for above >= 0 && isBlank(lines[above]) {
		above--
	}
	asks := func(string) bool { return true }
	switch kind {
	case claudeCodePermission:
		asks = claudeCodeQuestion.MatchString
	case claudeCodePlanApproval:
		asks = func(q string) bool { return strings.Contains(q, claudeCodePlanAsks) }
	}
	questionStart, question := claudeCodeQuestionAbove(lines, above, asks)
	if questionStart < 0 {
		return nil
	}
	prompt.Question = question
	switch kind {
	case claudeCodePermission:
		prompt.Title, prompt.Detail = claudeCodePermissionHeader(lines[:questionStart])
	case claudeCodeDecision:
		prompt.Title = claudeCodeDecisionTab(lines[:questionStart])
	}
	return prompt
}

// claudeCodeFirstOption is the line of option 1, looked for upward from the
// blank line below the options, and the column its number is in, where every
// option's is: a numbered line deeper in is a description or a wrapped label.
// -1 when a blank line, a rule or the top comes first. A decision's options
// have a rule before the last, which is passed over.
func claudeCodeFirstOption(lines []string, below int, decision bool) (first, column int) {
	column = -1
	for i := below - 1; i >= 0; i-- {
		line := lines[i]
		if isBlank(line) {
			return -1, column
		}
		if isClaudeCodeRule(line) {
			if decision {
				continue
			}
			return -1, column
		}
		m := claudeCodeNumbered.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if column < 0 {
			column = utf8.RuneCountInString(m[1])
		}
		if m[2] == "1" && utf8.RuneCountInString(m[1]) == column {
			return i, column
		}
	}
	return -1, column
}

// claudeCodeQuestionAbove is the question ending at line last, read upward
// line by line until the joined lines are one that asks, as a narrow screen
// wraps it, and the index of its first line; -1 when a blank line, a rule or
// the top comes before that.
func claudeCodeQuestionAbove(lines []string, last int, asks func(string) bool) (int, string) {
	for start := last; start >= 0 && !isBlank(lines[start]) && !isClaudeCodeAnyRule(lines[start]); start-- {
		if question := strings.Join(trimmed(lines[start:last+1]), " "); asks(question) {
			return start, question
		}
	}
	return -1, ""
}

// claudeCodeTrustPrompt reads the trust prompt: two options without numbers,
// the "Accessing workspace:" line as its question and the path under it as its
// detail, from the block under the last full rule.
func claudeCodeTrustPrompt(lines []string, below int) *screen.Prompt {
	first := below
	for first > 0 && !isBlank(lines[first-1]) && !isClaudeCodeRule(lines[first-1]) {
		first--
	}
	prompt := &screen.Prompt{Kind: screen.PromptYesNo, Selected: -1, Ticked: []int{}}
	for _, line := range trimmed(lines[first:below]) {
		if label, ok := strings.CutPrefix(line, "❯"); ok {
			prompt.Selected = len(prompt.Options)
			line = strings.TrimSpace(label)
		}
		prompt.Options = append(prompt.Options, line)
	}
	block := claudeCodeBlockUnderRule(lines[:first])
	if len(block) == 0 || strings.TrimSpace(block[0]) != claudeCodeWorkspace {
		return nil
	}
	prompt.Title = strings.TrimSuffix(claudeCodeWorkspace, ":")
	prompt.Question = claudeCodeWorkspace
	path := trimmed(block[1:])
	for len(path) > 0 && path[0] == "" {
		path = path[1:]
	}
	end := 0
	for end < len(path) && path[end] != "" {
		end++
	}
	// A narrow screen wraps the path, without a space at the break.
	prompt.Detail = strings.Join(path[:end], "")
	return prompt
}

// claudeCodePermissionHeader reads a permission's title and detail from the
// block between the last full rule and the question: the title is its first
// line, "Bash command", "Create file", or "Bash command · from the
// general-purpose agent" for a subagent's; the detail is the command between
// the dashed rules for a command, whose line under the title is a description,
// or the line under the title, a file's name, for the rest.
func claudeCodePermissionHeader(above []string) (title, detail string) {
	block := trimmed(claudeCodeBlockUnderRule(above))
	for len(block) > 0 && block[len(block)-1] == "" {
		block = block[:len(block)-1]
	}
	if len(block) == 0 {
		return "", ""
	}
	title = block[0]
	if !strings.Contains(title, claudeCodeCommand) {
		if len(block) > 1 && !isClaudeCodeDashedRule(block[1]) {
			detail = block[1]
		}
		return title, detail
	}
	var body []string
	inBody := false
	for _, line := range block[1:] {
		if isClaudeCodeDashedRule(line) {
			if inBody {
				break
			}
			inBody = true
			continue
		}
		if inBody {
			body = append(body, line)
		}
	}
	return title, strings.Join(body, "\n")
}

// claudeCodeDecisionTab is the name of the first tab in the line under the last
// full rule, "☐ Color" or "←  ☐ Fruits  ✔ Submit  →"; "" when there is none.
func claudeCodeDecisionTab(above []string) string {
	block := claudeCodeBlockUnderRule(above)
	if len(block) == 0 {
		return ""
	}
	if m := claudeCodeTabName.FindStringSubmatch(block[0]); m != nil {
		return m[1]
	}
	return ""
}

// claudeCodeBlockUnderRule is the lines under the last full rule in lines; none
// when there is no rule, since above it is the conversation.
func claudeCodeBlockUnderRule(lines []string) []string {
	for i := len(lines) - 1; i >= 0; i-- {
		if isClaudeCodeRule(lines[i]) {
			return lines[i+1:]
		}
	}
	return nil
}

func isClaudeCodeDashedRule(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "╌") && strings.HasSuffix(line, "╌")
}

// isClaudeCodeAnyRule is a full rule, a dashed one, or the plan approval's
// indented rule of 2.1.314.
func isClaudeCodeAnyRule(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "─") || strings.HasPrefix(line, "╌")
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func trimmed(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimSpace(line)
	}
	return out
}
