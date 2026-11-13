package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/holder/prompttest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/waittest"
)

// promptedAgent creates an agent in the project app whose program is prompttest
// asking s, and returns it, its checkout and its prompt once that reads.
func promptedAgent(t *testing.T, home, name string, s prompttest.Scenario) (api.Agent, string, api.AgentPrompt) {
	t.Helper()
	app := newProjectDir(t, home, "app")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(t.TempDir(), "scenario.json")
	if err := prompttest.Write(scenario, s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(prompttest.Script(exe, scenario)), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAgent(t, home, app, name)
	var prompt api.AgentPrompt
	waittest.For(t, "the agent's prompt to read", func() bool {
		v, exit := wdxJSON(t, home, "agent", "get", "--id", a.Id)
		if exit != clikit.ExitOK || v.Agent.Prompt == nil || v.Agent.Prompt.Selected == nil {
			return false
		}
		prompt = *v.Agent.Prompt
		return true
	})
	return a, app, prompt
}

// wantClosed waits until the program says how its prompt closed.
func wantClosed(t *testing.T, app, how string, answers ...[]string) {
	t.Helper()
	var got *prompttest.Answered
	waittest.For(t, "the program to say how the prompt closed", func() bool {
		var err error
		got, err = prompttest.ReadAnswered(app)
		return err == nil && got != nil
	})
	if got.How != how || !slices.EqualFunc(got.Answers, answers, slices.Equal[[]string]) {
		t.Fatalf("the prompt closed %s with %v, want %s with %v", got.How, got.Answers, how, answers)
	}
}

// runPrompt runs wdx prompt in app with answers typed.
func runPrompt(t *testing.T, home, app string, answers ...string) (out string, stderr string, exit int) {
	t.Helper()
	typed := answering(t, answers...)
	r := wdxWith(t, home, func(env *Env) { typed(env); env.WorkDir = app }, "prompt")
	return r.Stdout, r.Stderr, r.Exit
}

// wantNotLogged checks no line of the home's logs holds any of words.
func wantNotLogged(t *testing.T, home string, words ...string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		logtest.Scan(t, f, 4<<20, func(raw []byte) {
			for _, word := range words {
				if bytes.Contains(raw, []byte(word)) {
					t.Errorf("a log line holds %q: %s", word, raw)
				}
			}
		})
	}
}

var colorQuestion = prompttest.Question{Tab: "Color", Question: "Which hue suits the sky?", Options: []string{"Crimson", "Azure"}}

func TestPromptChoice(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	a, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}})

	out, stderr, exit := runPrompt(t, home, app, "1", "2")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent painter in app\n") {
		t.Fatalf("want it answered, got exit %d, %q, %q", exit, out, stderr)
	}
	for _, want := range []string{"1 prompt in app", "painter", "decision", "Which hue suits the sky?", "1  Crimson", "2  Azure", "3  Type something.", "4  Chat about this", "(type your own)"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the dialogue lacks %q: %q", want, stderr)
		}
	}
	wantClosed(t, app, "answered", []string{"Azure"})
	wantNotLogged(t, home, "Which hue suits the sky?", "Crimson", "Azure", "Type something")
	_ = a
}

func TestPromptYesNo(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, _ := promptedAgent(t, home, "runner", prompttest.Scenario{YesNo: &prompttest.YesNo{
		Title: "Bash command", Command: "go test ./...", Options: []string{"Yes", "No"},
	}})

	out, stderr, exit := runPrompt(t, home, app, "1", "1")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent runner in app\n") {
		t.Fatalf("want it answered, got exit %d, %q, %q", exit, out, stderr)
	}
	if !strings.Contains(stderr, "yes/no") || !strings.Contains(stderr, "Bash command") || !strings.Contains(stderr, "go test ./...") {
		t.Errorf("the dialogue lacks the prompt: %q", stderr)
	}
	wantClosed(t, app, "answered", []string{"Yes"})
	wantNotLogged(t, home, "go test ./...")
}

func TestPromptText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}})

	out, stderr, exit := runPrompt(t, home, app, "1", "3", "vermilion red")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent painter") {
		t.Fatalf("want it answered, got exit %d, %q, %q", exit, out, stderr)
	}
	wantClosed(t, app, "answered", []string{"vermilion red"})
	wantNotLogged(t, home, "vermilion")
}

func TestPromptChat(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	a, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}})

	out, stderr, exit := runPrompt(t, home, app, "1", "4")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent painter") || !strings.Contains(out, "agent attach --id "+a.Id) || !strings.Contains(out, "tab") {
		t.Fatalf("want it declined, and where to go on, got exit %d, %q, %q", exit, out, stderr)
	}
	wantClosed(t, app, "chat")
}

func TestPromptTicks(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	fruit := prompttest.Question{Tab: "Fruit", Question: "Which fruits do you like?", Options: []string{"Apple", "Pear", "Plum"}, Multi: true}
	_, app, p := promptedAgent(t, home, "grower", prompttest.Scenario{Questions: []prompttest.Question{fruit}})
	if !p.Multi || !slices.Contains(p.Kinds, api.AgentPromptKindsSubmit) {
		t.Fatalf("want a checkbox list with a Submit row, got %+v", p)
	}

	// The Submit row has no number: Chat about this is the one after Type something.
	out, stderr, exit := runPrompt(t, home, app, "1", "nope", "1 3", "y")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent grower") {
		t.Fatalf("want it answered, got exit %d, %q, %q", exit, out, stderr)
	}
	if strings.Contains(stderr, "Submit") || !strings.Contains(stderr, "give the numbers of the options") || !strings.Contains(stderr, "Send Apple, Plum?") {
		t.Errorf("unexpected dialogue: %q", stderr)
	}
	wantClosed(t, app, "answered", []string{"Apple", "Plum"})
	wantNotLogged(t, home, "Which fruits", "Apple", "Plum")
}

// TestPromptTicksNotConfirmed: nothing is pressed when the ticks are not
// confirmed, nor when none are given.
func TestPromptTicksNotConfirmed(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	fruit := prompttest.Question{Tab: "Fruit", Question: "Which fruits do you like?", Options: []string{"Apple", "Pear"}, Multi: true}
	_, app, _ := promptedAgent(t, home, "grower", prompttest.Scenario{Questions: []prompttest.Question{fruit}})

	_, stderr, exit := runPrompt(t, home, app, "1", "1 2", "n")
	if exit != clikit.ExitRejected || !strings.Contains(stderr, "[cancelled]") {
		t.Fatalf("want cancelled, got exit %d, %q", exit, stderr)
	}
	_, stderr, exit = runPrompt(t, home, app, "1", "")
	if exit != clikit.ExitRejected || !strings.Contains(stderr, "nothing ticked") || !strings.Contains(stderr, "[cancelled]") {
		t.Fatalf("want nothing sent, got exit %d, %q", exit, stderr)
	}
	if keys := prompttest.Keys(app); len(keys) != 0 {
		t.Fatalf("keys were pressed: %v", keys)
	}
}

var sizeQuestion = prompttest.Question{Tab: "Size", Question: "Which base size fits?", Options: []string{"Petite", "Grande"}}

func TestPromptSeveralQuestions(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, p := promptedAgent(t, home, "styler", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion, sizeQuestion}})
	if !slices.Equal(p.Questions, []string{"Color", "Size"}) {
		t.Fatalf("questions %v", p.Questions)
	}

	out, stderr, exit := runPrompt(t, home, app, "1", "2", "2", "y")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent styler") {
		t.Fatalf("want it answered, got exit %d, %q, %q", exit, out, stderr)
	}
	for _, want := range []string{"Color · question 1 of 2\n", "Size · question 2 of 2\n", "Review your answers", "Which hue suits the sky?\n    → Azure", "Which base size fits?\n    → Grande", "Submit these answers?"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the dialogue lacks %q: %q", want, stderr)
		}
	}
	wantClosed(t, app, "answered", []string{"Azure"}, []string{"Grande"})
	wantNotLogged(t, home, "Which base size fits?", "Grande", "Petite")
}

// TestPromptReviewNotConfirmed: the last answer is sent only on an explicit yes.
func TestPromptReviewNotConfirmed(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, _ := promptedAgent(t, home, "styler", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion, sizeQuestion}})

	_, stderr, exit := runPrompt(t, home, app, "1", "2", "2", "")
	if exit != clikit.ExitRejected || !strings.Contains(stderr, "[cancelled]") || !strings.Contains(stderr, "nothing was submitted") {
		t.Fatalf("want cancelled, got exit %d, %q", exit, stderr)
	}
	if got, err := prompttest.ReadAnswered(app); err != nil || got != nil {
		t.Fatalf("the prompt closed: %+v, %v", got, err)
	}
	// Only the first question's Down and Enter were pressed.
	if keys := prompttest.Keys(app); slices.Contains(keys, "enter") && len(keys) > 2 {
		t.Fatalf("keys beyond the first question's: %v", keys)
	}
}

// TestPromptChanged: a prompt that changed while it was answered is said, and
// the list is read again.
func TestPromptChanged(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}, ChangeAfter: 1})

	out, stderr, exit := runPrompt(t, home, app, "1", "2", "1", "1")
	if exit != clikit.ExitOK || !strings.HasPrefix(out, "answered agent painter") {
		t.Fatalf("want it answered the second time, got exit %d, %q, %q", exit, out, stderr)
	}
	if !strings.Contains(stderr, "the prompt changed") || strings.Count(stderr, "1 prompt in app") != 2 || !strings.Contains(stderr, "(asked again)") {
		t.Errorf("want the change said and the list read again: %q", stderr)
	}
	wantClosed(t, app, "answered", []string{"Crimson"})
}

func TestPromptQuit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}})

	for _, answers := range [][]string{{"q"}, {"1", "q"}} {
		_, stderr, exit := runPrompt(t, home, app, answers...)
		if exit != clikit.ExitRejected || !strings.Contains(stderr, "[cancelled]") {
			t.Fatalf("%v: want cancelled, got exit %d, %q", answers, exit, stderr)
		}
	}
	if keys := prompttest.Keys(app); len(keys) != 0 {
		t.Fatalf("keys were pressed: %v", keys)
	}
}

func TestPromptNone(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(claudeAtRest), 0o600); err != nil {
		t.Fatal(err)
	}
	newAgent(t, home, app, "idle")

	out, _, exit := runPrompt(t, home, app)
	if exit != clikit.ExitOK || out != "no prompts\n" {
		t.Fatalf("want no prompts, got exit %d, %q", exit, out)
	}
	v, exit := wdxJSONWith(t, home, in(app), "prompt")
	if exit != clikit.ExitOK || len(v.Agents) != 0 {
		t.Fatalf("want none, got exit %d, %+v", exit, v)
	}
}

// TestPromptJSON: --json lists the agents with a prompt, and asks nothing.
func TestPromptJSON(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	a, app, _ := promptedAgent(t, home, "painter", prompttest.Scenario{Questions: []prompttest.Question{colorQuestion}})

	v, exit := wdxJSONWith(t, home, in(app), "prompt")
	if exit != clikit.ExitOK || len(v.Agents) != 1 || v.Agents[0].Id != a.Id || v.Agents[0].Prompt == nil || v.Agents[0].Prompt.Question != colorQuestion.Question {
		t.Fatalf("want the agent with its prompt, got exit %d, %+v", exit, v)
	}
	if got, err := prompttest.ReadAnswered(app); err != nil || got != nil || len(prompttest.Keys(app)) != 0 {
		t.Fatalf("--json pressed something: %+v, %v", got, err)
	}
	v, exit = wdxJSON(t, home, "prompt", "--project-name", "app")
	if exit != clikit.ExitOK || len(v.Agents) != 1 {
		t.Fatalf("want the agent by project name, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "prompt", "--project-name", "nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
}

// TestPromptNoInput: without a terminal or with --no-input, and no --json, it
// says to use --json.
func TestPromptNoInput(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")

	for _, args := range [][]string{{"prompt"}, {"prompt", "--no-input"}} {
		out := wdxWith(t, home, in(app), args...)
		if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "[usage]") || !strings.Contains(out.Stderr, "--json") {
			t.Fatalf("%v: want a usage error that says --json, got %+v", args, out)
		}
	}
	typed := answering(t, "1")
	out := wdxWith(t, home, func(env *Env) { typed(env); env.WorkDir = app }, "prompt", "--no-input")
	if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "--no-input") {
		t.Fatalf("want --no-input to be the reason, got %+v", out)
	}
	v, exit := wdxJSONWith(t, home, in(app), "prompt", "--no-input")
	if exit != clikit.ExitOK || len(v.Agents) != 0 {
		t.Fatalf("--json goes with --no-input: exit %d, %+v", exit, v)
	}
}

// TestPromptList: the list's rows, in the order given, with the kind and the text
// cut to the width.
func TestPromptList(t *testing.T) {
	t.Parallel()
	title, detail := "Bash command", "go test ./... -run TestSomethingWithAVeryLongNameThatMustBeCut -count=1"
	agents := []api.Agent{
		{Name: "wdx-arch", ProjectName: "wdx", Prompt: &api.AgentPrompt{Kind: api.AgentPromptKindYesNo, Title: &title, Detail: &detail, Question: "Do you want to proceed?"}},
		{Name: "cor-fixes-2", ProjectName: "wdx", Prompt: &api.AgentPrompt{Kind: api.AgentPromptKindDecision, Question: "Which store keeps the sessions?"}},
		{Name: "ui-polish", ProjectName: "wdx", Prompt: &api.AgentPrompt{Kind: api.AgentPromptKindDecision, Review: true, Question: "Ready to submit your answers?"}},
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	d := &promptDialogue{cmd: cmd, answers: typedLines("3"), out: &out, st: style.New(false)}
	got, err := d.pick(agents)
	if err != nil || got.Name != "ui-polish" {
		t.Fatalf("picked %q, %v", got.Name, err)
	}
	want := "3 prompts in wdx\n" +
		"  1  wdx-arch     yes/no    Bash command · go test ./... -run TestSomethingWithAVeryLon…\n" +
		"  2  cor-fixes-2  decision  Which store keeps the sessions?\n" +
		"  3  ui-polish    review    Ready to submit your answers?\n" +
		"Which one? (1-3, q to quit): "
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// typedLines is the lines a person types, read as the dialogue reads them.
func typedLines(lines ...string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

func TestParseTicks(t *testing.T) {
	t.Parallel()
	for input, want := range map[string][]int{"1 3": {0, 2}, "3,1": {0, 2}, " 2 ": {1}, "1, 2": {0, 1}} {
		if got, ok := parseTicks(input, 3); !ok || !slices.Equal(got, want) {
			t.Errorf("%q: got %v, %v, want %v", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "0", "4", "1 1", "x", "1 x"} {
		if got, ok := parseTicks(input, 3); ok {
			t.Errorf("%q: got %v, want none", input, got)
		}
	}
}

func TestPromptListLog(t *testing.T) {
	t.Parallel()
	title := "Bash command"
	list := promptList{api.AgentList{Agents: []api.Agent{{Name: "a", Prompt: &api.AgentPrompt{Kind: api.AgentPromptKindYesNo, Title: &title, Question: "Secret question?", Options: []string{"Secret option"}}}}}}
	b, err := json.Marshal(list.LogValue().Any())
	if err != nil || strings.Contains(string(b), "Secret") {
		t.Fatalf("the log value holds the prompt's text: %s, %v", b, err)
	}
}
