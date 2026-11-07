package aca_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

const (
	yesNo    = screen.PromptYesNo
	decision = screen.PromptDecision

	planQuestion = "Claude has written up a plan and is ready to execute. Would you like to proceed?"
	autoMode     = "Yes, and switch to auto mode · auto mode handles these prompts for you"
	acceptEdits  = "Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)"
	pingAgain    = "Yes, and don’t ask again for: ping *"
)

func prompt(kind screen.PromptKind, title, question, detail string, selected int, options ...string) *screen.Prompt {
	return &screen.Prompt{Kind: kind, Title: title, Question: question, Detail: detail, Options: options, Selected: selected}
}

func trust(path string) *screen.Prompt {
	return prompt(yesNo, "Accessing workspace", "Accessing workspace:", path, 0, "No, exit", "Yes, I trust this folder")
}

// bash is the permission of touch in dir, whose second option names the dir.
func bash(dir string, selected int) *screen.Prompt {
	return prompt(yesNo, "Bash command", "Do you want to proceed?", "touch wdx-a.txt", selected,
		"Yes", "Yes, and always allow access to "+dir+" from this project", autoMode, "No")
}

func ping(title, command string) *screen.Prompt {
	return prompt(yesNo, title, "Do you want to proceed?", command, 0, "Yes", pingAgain, autoMode, "No")
}

var (
	write     = prompt(yesNo, "Create file", "Do you want to create wdx-b.txt?", "wdx-b.txt", 0, "Yes", acceptEdits, "No")
	plan      = prompt(yesNo, "", planQuestion, "", 0, "Yes, and use auto mode", "Yes, manually approve edits", "Tell Claude what to change")
	color     = prompt(decision, "Color", "Which color do you choose?", "", 0, "Red", "Blue", "Type something.", "Chat about this")
	redOrBlue = prompt(decision, "Color", "Red or blue?", "", 0, "Red", "Blue", "Type something.", "Chat about this")
	fruits    = &screen.Prompt{Kind: decision, Title: "Fruits", Question: "Which fruits do you like?", Multi: true,
		Options: []string{"Apple", "Pear", "Plum", "Type something", "Chat about this"}}
)

// moved is p with the cursor on another option.
func moved(p *screen.Prompt, selected int) *screen.Prompt {
	m := *p
	m.Selected = selected
	return &m
}

// withOptions is p with other labels, as a width wraps or cuts them.
func withOptions(p *screen.Prompt, options ...string) *screen.Prompt {
	m := *p
	m.Options = options
	return &m
}

// promptRecordings is the prompt each marked step of a recording shows; a step
// not named shows none, as after an answer or an Esc.
var promptRecordings = []struct {
	dir     string
	prompts map[string]*screen.Prompt
}{
	{"prompts", map[string]*screen.Prompt{
		"01_trust":                    trust("/tmp/recording-0/wdxrec2-vdi0klmk"),
		"04_permission_bash":          bash("/tmp/recording-0/wdxrec2-vdi0klmk", 0),
		"05_permission_bash_moved":    bash("/tmp/recording-0/wdxrec2-vdi0klmk", 1),
		"08_permission_write":         write,
		"10_permission_ping":          ping("Bash command", "ping -c 25 127.0.0.1"),
		"14_decision_radio":           color,
		"17_decision_checkbox":        fruits,
		"18_decision_checkbox_ticked": fruits,
	}},
	{"plan", map[string]*screen.Prompt{"03_plan_approval": plan}},
	{"classic-plan", map[string]*screen.Prompt{"03_plan_approval": plan}},
	{"background-prompts", map[string]*screen.Prompt{
		"02_start_approval_1": ping("Bash command", "ping -c 100 127.0.0.1"),
		"03_start_approval_2": ping("Bash command · from the general-purpose agent", "ping -c 100 127.0.0.1"),
		"04_start_approval_3": ping("Bash command · from the general-purpose agent", "ping -c 100 127.0.0.1"),
		// Its second option wraps onto two more lines at 120 columns.
		"06_permission_while_bg": prompt(yesNo, "Bash command", "Do you want to proceed?", "touch a.txt", 0, "Yes",
			"Yes, and always allow access to /tmp/recording-0/-xxxx-xxxxxx-xxx-xxx/00000000-0000-0000-0000-000000000000/recordings/xxx/work/w-1sjfinv8 from this project",
			autoMode, "No"),
		"08_decision_while_bg": redOrBlue,
	}},
	{"background-prompts-60x24", map[string]*screen.Prompt{
		"02_start_approval_1": ping("Bash command", "ping -c 100 127.0.0.1"),
		"03_start_approval_2": ping("Bash command · from the general-purpose agent", "ping -c 100 127.0.0.1"),
		"04_start_approval_3": ping("Bash command · from the general-purpose agent", "ping -c 100 127.0.0.1"),
		// The path wraps in the middle of a word, so the joined label holds a space it has not.
		"06_permission_while_bg": prompt(yesNo, "Bash command", "Do you want to proceed?", "touch a.txt", 0, "Yes",
			"Yes, and always allow access to /tmp/recording-0/-xxxx-xxxxxx-xxx-xxx/0000000000000000 b3-b0000000000000000/recordings/xxx/work/w-rcwgsg7a from this project",
			autoMode, "No"),
		"08_decision_while_bg": redOrBlue,
	}},
	{"narrow-40x24", map[string]*screen.Prompt{
		// At 40 columns Claude draws its third option's overflow over the fourth:
		// "4. No" reads "4. Nor you", on 2.1.319 too. Read as shown.
		"05_permission": prompt(yesNo, "Bash command", "Do you want to proceed?", "touch a.txt", 0, "Yes",
			"Yes, and always allow access to /tmp/recording-0/-xxxx-xxxxxx-xxx- wdx/000000000000000000000000000000 000000/recordings/xxx/work/w-cfl69 zya from this project",
			"Yes, and switch to auto mode · auto mode handles these prompts", "Nor you"),
		"07_decision": redOrBlue,
	}},
	{"../2.1.319/prompts-120x40", promptsOf2319("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-3uvixuiq", 120)},
	{"../2.1.319/prompts-60x24", promptsOf2319("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-zaawlzfn", 60)},
	{"../2.1.319/prompts-40x24", promptsOf2319("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-c3kiwmuh", 40)},
	{"../2.1.319/plan-120x40", map[string]*screen.Prompt{"01_trust": trust("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-rt6j4xfq"), "03_plan_approval": plan}},
	{"../2.1.319/plan-60x24", map[string]*screen.Prompt{"01_trust": trust("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-z0a708tv"), "03_plan_approval": plan}},
	{"../2.1.319/plan-40x24", map[string]*screen.Prompt{"01_trust": trust("/xxx/xxxxxx-xxxx/xxx-xxxxxxx/work/w-d_9_6927"), "03_plan_approval": plan}},
}

// promptsOf2319 is what a 2.1.319 prompts recording shows, in the work directory
// dir at the given width: the same as 2.1.314 but at 40 columns, where the Bash
// permission's options wrap and overflow as narrow-40x24's.
func promptsOf2319(dir string, cols int) map[string]*screen.Prompt {
	permission := bash(dir, 0)
	if cols == 40 {
		permission = withOptions(permission, "Yes",
			"Yes, and always allow access to "+dir[:strings.LastIndex(dir, "/")+1]+" "+dir[strings.LastIndex(dir, "/")+1:]+" from this project",
			"Yes, and switch to auto mode · auto mode handles these prompts", "Nor you")
	}
	return map[string]*screen.Prompt{
		"01_trust":                    trust(dir),
		"04_permission_bash":          permission,
		"05_permission_bash_moved":    moved(permission, 1),
		"08_permission_write":         write,
		"14_decision_radio":           color,
		"17_decision_checkbox":        fruits,
		"18_decision_checkbox_ticked": fruits,
	}
}

func TestClaudeCodePromptOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, rec := range promptRecordings {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			steps := replayed(t, rec.dir).steps
			for name := range rec.prompts {
				if !slices.ContainsFunc(steps, func(s markedRead) bool { return s.name == name }) {
					t.Errorf("%s: not in marks.log", name)
				}
			}
			for _, step := range steps {
				state := aca.ClaudeCodeScreenParser{}.Parse(step.lines, step.undimmed)
				want := rec.prompts[step.name]
				if want != nil && state.Activity != waiting {
					t.Errorf("%s: %s, want waiting", step.name, state.Activity)
				}
				if !state.Prompt.Equal(want) {
					t.Errorf("%s:\n got %s\nwant %s\nbottom of the screen:\n%s", step.name, promptString(state.Prompt), promptString(want), bottom(step.lines))
				}
			}
		})
	}
}

// namedKind is the kind a step's name says its prompt has, if it names one.
func namedKind(step string) (screen.PromptKind, bool) {
	switch {
	case strings.Contains(step, "decision"):
		return decision, true
	case strings.Contains(step, "permission"), strings.Contains(step, "approval"), strings.Contains(step, "trust"):
		return yesNo, true
	}
	return "", false
}

// TestClaudeCodePromptOnlyWhenWaiting reads every recording's every moment: a
// prompt is never read on a screen whose activity is not waiting, and never
// with a kind that neither the marked step before it nor the one after, of
// those naming a kind, says: the step marking a prompt falls while it is open.
func TestClaudeCodePromptOnlyWhenWaiting(t *testing.T) {
	t.Parallel()
	for _, dir := range allRecordings(t) {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, dir)
			for _, m := range rep.moments {
				state := aca.ClaudeCodeScreenParser{}.Parse(m.lines, m.undimmed)
				if state.Prompt == nil {
					continue
				}
				if state.Activity != waiting {
					t.Errorf("at %s: a %s prompt while %s:\n%s", m.at, state.Prompt.Kind, state.Activity, bottom(m.lines))
				}
				if before, after, ok := stepsNamingKind(rep.steps, m.at); ok && !slices.Contains(kindsNamed(before, after), state.Prompt.Kind) {
					t.Errorf("at %s: a %s prompt, between the steps %q and %q:\n%s", m.at, state.Prompt.Kind, before, after, bottom(m.lines))
				}
			}
		})
	}
}

// stepsNamingKind is the name of the last marked step at or before at whose name
// says a kind, and of the first after it; "" for one that does not exist, and
// ok false when neither does.
func stepsNamingKind(steps []markedRead, at time.Duration) (before, after string, ok bool) {
	for _, step := range steps {
		if _, names := namedKind(step.name); !names {
			continue
		}
		ok = true
		if step.at <= at {
			before = step.name
		} else if after == "" {
			after = step.name
		}
	}
	return before, after, ok
}

// kindsNamed is the kinds the named steps say, of those that exist.
func kindsNamed(names ...string) []screen.PromptKind {
	var kinds []screen.PromptKind
	for _, name := range names {
		if kind, ok := namedKind(name); ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// allRecordings is every recording of Claude Code in testdata, by the name
// replayed takes.
func allRecordings(t *testing.T) []string {
	t.Helper()
	var dirs []string
	root := filepath.Join("testdata", "claude_code")
	versions, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		recordings, err := os.ReadDir(filepath.Join(root, version.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recordings {
			if _, err := os.Stat(filepath.Join(root, version.Name(), rec.Name(), "raw.out")); err != nil {
				continue
			}
			if version.Name() == "2.1.314" {
				dirs = append(dirs, rec.Name())
			} else {
				dirs = append(dirs, "../"+version.Name()+"/"+rec.Name())
			}
		}
	}
	return dirs
}

// TestClaudeCodePromptRules: the kind comes from the footer and the line above
// the options, never from the options' words, and a screen that cannot be read
// is waiting without a prompt.
func TestClaudeCodePromptRules(t *testing.T) {
	t.Parallel()
	const rule = "────────────────"
	const dashed = "╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌"
	cases := []struct {
		name  string
		lines []string
		want  *screen.Prompt
	}{
		{"options without a question", []string{rule, " ❯ 1. Yes", "   2. No", "", " Esc to cancel"}, nil},
		{"a question without options", []string{rule, " Do you want to proceed?", "", " Esc to cancel"}, nil},
		{"options under a reply, no question", []string{rule, " Sure, here it is:", " ❯ 1. Yes", "   2. No", "", " Esc to cancel"}, nil},
		{"a plan footer without its sentence", []string{rule, " Ready?", "", " ❯ 1. Yes", "", " ctrl+g to edit in VS Code · ~/.claude/plans/x.md"}, nil},
		{"a trust footer without the workspace", []string{rule, " Trust?", "", " ❯ No, exit", "   Yes", "", " Enter to confirm · Esc to cancel"}, nil},
		{"a decision footer without numbered options", []string{rule, " ☐ Color", "", " Which?", "", " ❯ Red", "", " Enter to select · ↑/↓ to navigate · Esc to cancel"}, nil},
		{"a permission without the rule on screen", []string{" touch a.txt", dashed, " Do you want to proceed?", " ❯ 1. Yes", "   2. No", "", " Esc to cancel · Tab to amend"},
			prompt(yesNo, "", "Do you want to proceed?", "", 0, "Yes", "No")},
		{"a wrapped question", []string{rule, " Create file", " a.txt", " Do you want to create", " a.txt?", " ❯ 1. Yes", "   2. No", "", " Esc to cancel"},
			prompt(yesNo, "Create file", "Do you want to create a.txt?", "a.txt", 0, "Yes", "No")},
		{"a wrapped label", []string{rule, " Do you want to proceed?", "   1. Yes", " ❯ 2. Yes, and always allow", "      /tmp from this project", "   3. No", "", " Esc to cancel"},
			prompt(yesNo, "", "Do you want to proceed?", "", 1, "Yes", "Yes, and always allow /tmp from this project", "No")},
		{"a wrapped footer", []string{rule, " ☐ Color", "", " Which?", "", " ❯ 1. Red", "   2. Blue", "", " Enter to select · ↑/↓ to navigate · Esc", " to cancel"},
			prompt(decision, "Color", "Which?", "", 0, "Red", "Blue")},
		{"a decision's descriptions and rule", []string{rule, " ☐ Color", "", " Which?", "", "   1. Red", "     1. favourite", " ❯ 2. Blue", rule, "   3. Chat about this", "", " Enter to select · Esc to cancel"},
			prompt(decision, "Color", "Which?", "", 1, "Red", "Blue", "Chat about this")},
		{"a decision without a tab line", []string{rule, " Which?", "", " ❯ 1. Red", "", " Enter to select · Esc to cancel"},
			prompt(decision, "", "Which?", "", 0, "Red")},
		{"checkboxes", []string{rule, "←  ☒ Fruits  ✔ Submit  →", "", " Which?", "", " ❯ 1. [✔] Apple", "   2. [ ] Pear", "", " Enter to select · Esc to cancel"},
			&screen.Prompt{Kind: decision, Title: "Fruits", Question: "Which?", Options: []string{"Apple", "Pear"}, Multi: true}},
		{"no cursor", []string{rule, " Do you want to proceed?", "   1. Yes", "   2. No", "", " Esc to cancel"},
			prompt(yesNo, "", "Do you want to proceed?", "", -1, "Yes", "No")},
		{"a command's detail is the dashed block", []string{rule, " Bash command", " Lists files", dashed, " ls -la", " ls /tmp", dashed, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel"},
			prompt(yesNo, "Bash command", "Do you want to proceed?", "ls -la\nls /tmp", 0, "Yes")},
		{"a subagent's command", []string{rule, " Bash command · from the general-purpose agent", " Tip: auto mode handles these prompts", " Lists files", dashed, " ls", dashed, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel"},
			prompt(yesNo, "Bash command · from the general-purpose agent", "Do you want to proceed?", "ls", 0, "Yes")},
		{"a file's detail is its name", []string{rule, " Edit file", " a.txt", dashed, " 1 - old", " 1 + new", dashed, " Do you want to make this edit to a.txt?", " ❯ 1. Yes", "", " Esc to cancel"},
			prompt(yesNo, "Edit file", "Do you want to make this edit to a.txt?", "a.txt", 0, "Yes")},
		{"the trust prompt's path wrapped", []string{rule, " Accessing workspace:", "", " /tmp/a-long-", " path", "", " Security guide", "", " ❯ No, exit", "   Yes, I trust this folder", "", " Enter to confirm · Esc to cancel"},
			trust("/tmp/a-long-path")},
		{"the plan approval of 2.1.314", []string{"  ────", "   " + planQuestion, "", "   ❯ 1. Yes, and use auto mode", "     2. Tell Claude what to change", "        shift+tab to approve with this feedback", "", "   ctrl+g to edit in VS Code · ~/.claude/plans/x.md"},
			prompt(yesNo, "", planQuestion, "", 0, "Yes, and use auto mode", "Tell Claude what to change")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			state := aca.ClaudeCodeScreenParser{}.Parse(slices.Clone(c.lines), nil)
			if state.Activity != waiting {
				t.Fatalf("%s, want waiting", state.Activity)
			}
			if !state.Prompt.Equal(c.want) {
				t.Errorf("got %s\nwant %s", promptString(state.Prompt), promptString(c.want))
			}
		})
	}
}

func promptString(p *screen.Prompt) string {
	if p == nil {
		return "no prompt"
	}
	return strings.Join([]string{string(p.Kind), "title " + p.Title, "question " + p.Question, "detail " + p.Detail,
		"options " + strings.Join(p.Options, " | "), "selected " + strings.Repeat("*", max(0, p.Selected+1)), "multi " + map[bool]string{true: "yes", false: "no"}[p.Multi]}, "; ")
}
