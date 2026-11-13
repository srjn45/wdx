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
	claudeCodeNumbered = regexp.MustCompile(`^(\s*(?:❯\s+)?)(\d+)\.\s+(\[[ ✔]\]\s+)?(.*?)\s*$`)
	claudeCodeQuestion = regexp.MustCompile(`^Do you want\b.*\?$`)
	claudeCodeTabName  = regexp.MustCompile(`([☐☒]) (.+?)(?:\s{2,}|$)`)
	// claudeCodeAsksWords is a yes/no option that takes what to do instead:
	// "Tell Claude what to change", "No, and tell Claude what to do differently".
	claudeCodeAsksWords = regexp.MustCompile(`(?i)\btell Claude what\b`)
	claudeCodePlanAsks  = "Claude has written up a plan"
	claudeCodeWorkspace = "Accessing workspace:"
	claudeCodeCommand   = " command"
	claudeCodeReviewing = "Review your answers"
	claudeCodeReadyAsks = "Ready to submit your answers?"
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
	prompt := claudeCodeOptions(lines[first:below], column, kind)
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
		prompt.Title, prompt.Questions, prompt.Answered = claudeCodeDecisionTabs(lines[:questionStart])
	}
	return prompt
}

// claudeCodeOptions reads the options of a prompt from their lines, those of
// option 1 to the last, whose numbers are in the given column, with what each
// is. A decision's kinds go by position and shape: the options after its rule
// are chat, the last numbered one before it text, and a multi-select's row
// without a number right above the rule is its Submit. A yes/no prompt's are
// choices, but those whose label asks for words.
func claudeCodeOptions(lines []string, column int, kind claudeCodePromptKind) *screen.Prompt {
	prompt := &screen.Prompt{Kind: screen.PromptYesNo, Selected: -1, Ticked: []int{}, Questions: []string{}, Answered: []int{}}
	if kind == claudeCodeDecision {
		prompt.Kind = screen.PromptDecision
	}
	add := func(label string, selected bool, kind screen.OptionKind) {
		if selected {
			prompt.Selected = len(prompt.Options)
		}
		prompt.Options = append(prompt.Options, label)
		prompt.Kinds = append(prompt.Kinds, kind)
	}
	afterRule := false
	numbered := 0
	for i, line := range lines {
		m := claudeCodeNumbered.FindStringSubmatch(line)
		switch {
		case m != nil && utf8.RuneCountInString(m[1]) == column && m[2] == strconv.Itoa(numbered+1):
			numbered++
			prompt.Multi = prompt.Multi || m[3] != ""
			if strings.Contains(m[3], "✔") {
				prompt.Ticked = append(prompt.Ticked, len(prompt.Options))
			}
			optionKind := screen.OptionChoice
			switch {
			case afterRule:
				optionKind = screen.OptionChat
			case kind != claudeCodeDecision && claudeCodeAsksWords.MatchString(m[4]):
				optionKind = screen.OptionText
			}
			add(m[4], strings.Contains(m[1], "❯"), optionKind)
		case isClaudeCodeRule(line):
			// The rule before a decision's Chat about this: the last numbered
			// option above it takes text.
			if kind == claudeCodeDecision && !afterRule {
				afterRule = true
				for j := len(prompt.Kinds) - 1; j >= 0; j-- {
					if prompt.Kinds[j] == screen.OptionChoice {
						prompt.Kinds[j] = screen.OptionText
						break
					}
				}
			}
		case kind == claudeCodeDecision && prompt.Multi && !afterRule && i+1 < len(lines) && isClaudeCodeRule(lines[i+1]):
			// The multi-select's Submit row, with no number, under Type something.
			label, selected := strings.CutPrefix(strings.TrimSpace(line), "❯")
			add(strings.TrimSpace(label), selected, screen.OptionSubmit)
		case kind == claudeCodeDecision, kind == claudeCodePlanApproval:
			// A decision's description under its option and the plan's key hint
			// under its last are not labels.
		default:
			// The rest of a label the width wrapped.
			prompt.Options[len(prompt.Options)-1] += " " + strings.TrimSpace(line)
			if claudeCodeAsksWords.MatchString(prompt.Options[len(prompt.Options)-1]) {
				prompt.Kinds[len(prompt.Kinds)-1] = screen.OptionText
			}
		}
	}
	return prompt
}

// claudeCodeReview reads a decision's review screen, which Claude shows after
// its questions, or after a multi-select's Submit: under the last full rule the
// tab line, "Review your answers", each question and its answer, then "Ready to
// submit your answers?" and two options, Submit answers and Cancel, with no
// footer under them. nil when the screen is not one.
func claudeCodeReview(lines []string) *screen.Prompt {
	block := trimmed(claudeCodeBlockUnderRule(lines))
	if len(block) < 3 || !strings.HasPrefix(block[0], "←") || !strings.Contains(block[0], "✔ Submit") {
		return nil
	}
	heading := 1
	for heading < len(block) && block[heading] == "" {
		heading++
	}
	if heading == len(block) || block[heading] != claudeCodeReviewing {
		return nil
	}
	first, column := claudeCodeFirstOption(lines, len(lines), false)
	if first < 0 {
		return nil
	}
	above := first - 1
	for above >= 0 && isBlank(lines[above]) {
		above--
	}
	if start, _ := claudeCodeQuestionAbove(lines, above, func(q string) bool { return q == claudeCodeReadyAsks }); start < 0 {
		return nil
	}
	prompt := claudeCodeOptions(lines[first:], column, claudeCodeDecision)
	prompt.Title, prompt.Question, prompt.Review = claudeCodeReviewing, claudeCodeReadyAsks, true
	_, prompt.Questions, prompt.Answered = claudeCodeDecisionTabs(lines[:first])
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
	prompt := &screen.Prompt{Kind: screen.PromptYesNo, Selected: -1, Ticked: []int{}, Questions: []string{}, Answered: []int{}}
	for _, line := range trimmed(lines[first:below]) {
		if label, ok := strings.CutPrefix(line, "❯"); ok {
			prompt.Selected = len(prompt.Options)
			line = strings.TrimSpace(label)
		}
		prompt.Options = append(prompt.Options, line)
		prompt.Kinds = append(prompt.Kinds, screen.OptionChoice)
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

// claudeCodeDecisionTabs reads the tab line under the last full rule, "☐ Color"
// for one question or "←  ☒ Color  ☐ Size  ✔ Submit  →" for several: the title,
// the current question's name, and when there are several, every question's
// name and the indexes of those answered, ☒. Which tab is current shows in
// color only, so it is taken to be the first not answered, as Claude moves on
// to the next after each answer; "" when all are, as when the person went back.
func claudeCodeDecisionTabs(above []string) (title string, questions []string, answered []int) {
	questions, answered = []string{}, []int{}
	block := claudeCodeBlockUnderRule(above)
	if len(block) == 0 {
		return "", questions, answered
	}
	tabs := claudeCodeTabName.FindAllStringSubmatch(block[0], -1)
	if len(tabs) == 0 {
		return "", questions, answered
	}
	if len(tabs) == 1 {
		return tabs[0][2], questions, answered
	}
	for i, tab := range tabs {
		questions = append(questions, tab[2])
		if tab[1] == "☒" {
			answered = append(answered, i)
		} else if title == "" {
			title = tab[2]
		}
	}
	return title, questions, answered
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
