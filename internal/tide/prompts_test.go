package tide

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// yesNoPrompt is a Bash permission, Claude's cursor on its first option.
func yesNoPrompt() api.AgentPrompt {
	return api.AgentPrompt{
		Kind: api.AgentPromptKindYesNo, Title: ptr("Bash command"), Detail: ptr("go test ./..."),
		Question: "Do you want to proceed?", Options: []string{"Yes", "Yes, and don't ask again", "No"},
		Kinds: []api.AgentPromptKinds{"choice", "choice", "choice"}, Selected: ptr(0),
		Ticked: []int{}, Questions: []string{}, Answered: []int{},
	}
}

// radioPrompt is a decision of one question with radio options.
func radioPrompt() api.AgentPrompt {
	return api.AgentPrompt{
		Kind: api.AgentPromptKindDecision, Title: ptr("Store"), Question: "Which store keeps the sessions?",
		Options: []string{"SQLite", "Files", "Type something.", "Chat about this"},
		Kinds:   []api.AgentPromptKinds{"choice", "choice", "text", "chat"}, Selected: ptr(0),
		Ticked: []int{}, Questions: []string{}, Answered: []int{},
	}
}

// checkboxPrompt is a decision whose options are checkboxes, the first ticked on
// Claude's screen.
func checkboxPrompt() api.AgentPrompt {
	return api.AgentPrompt{
		Kind: api.AgentPromptKindDecision, Title: ptr("Pages"), Question: "Which pages get the badge?", Multi: true,
		Options: []string{"Agents", "Tide", "Plans", "Type something", "Submit", "Chat about this"},
		Kinds:   []api.AgentPromptKinds{"choice", "choice", "choice", "text", "submit", "chat"}, Selected: ptr(0),
		Ticked: []int{0}, Questions: []string{}, Answered: []int{},
	}
}

// questionsPrompt is a decision of two questions, the first shown.
func questionsPrompt() api.AgentPrompt {
	return api.AgentPrompt{
		Kind: api.AgentPromptKindDecision, Title: ptr("Color"), Question: "Which accent color?",
		Options: []string{"Green", "Blue", "Type something.", "Chat about this"},
		Kinds:   []api.AgentPromptKinds{"choice", "choice", "text", "chat"}, Selected: ptr(0),
		Ticked: []int{}, Questions: []string{"Color", "Size"}, Answered: []int{},
	}
}

// secondQuestion is questionsPrompt once its first question was answered.
func secondQuestion() api.AgentPrompt {
	p := questionsPrompt()
	p.Title, p.Question, p.Answered = ptr("Size"), "Which base size?", []int{0}
	p.Options = []string{"13 px", "14 px", "Type something.", "Chat about this"}
	return p
}

func prompted(name string, p api.AgentPrompt) api.Agent {
	a := running(name)
	a.Aca, a.Prompt = "claude_code", &p
	waiting := api.AgentActivityWaiting
	a.Activity = &waiting
	return a
}

// sentAnswer is an answer tide sent.
type sentAnswer struct {
	agentID string
	answer  api.PromptAnswers
}

// promptsModel is a window whose agents are agents, its prompts' tab open and
// focused, the answers it sends recorded and answered with what refuse returns.
func promptsModel(t *testing.T, agents ...api.Agent) (*Model, *[]sentAnswer, *error) {
	t.Helper()
	m := newAgentsModel(110, 30, agents...)
	var logs bytes.Buffer
	m.log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() {
		for _, secret := range []string{"Bash command", "go test", "proceed", "SQLite", "Which", "Green", "Agents", "kiwi", "mauve", "Size", "Color", "13 px"} {
			if strings.Contains(logs.String(), secret) {
				t.Errorf("a log line holds %q:\n%s", secret, logs.String())
			}
		}
	})
	var sent []sentAnswer
	var refuse error
	m.answerPrompt = func(_ context.Context, id string, a api.PromptAnswers) error {
		sent = append(sent, sentAnswer{id, a})
		return refuse
	}
	m.layout.setFocus(project)
	keys(m, promptsKey)
	if !m.promptsShown() || m.layout.focus != agent {
		t.Fatalf("tab %q, focus %s", m.list().active, m.layout.focus)
	}
	return m, &sent, &refuse
}

// agentPaneText is what the agent pane shows, a line per row.
func agentPaneText(m *Model) string {
	var rows []string
	for line := range strings.Lines(text(m)) {
		if cells := strings.SplitN(lightSides(line), "│", 4); len(cells) == 4 {
			rows = append(rows, cells[2])
		}
	}
	return strings.Join(rows, "\n")
}

// wantNoText fails the test if the agent pane shows any of unwanted.
func wantNoText(t *testing.T, m *Model, unwanted ...string) {
	t.Helper()
	got := agentPaneText(m)
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Fatalf("%q on:\n%s", u, got)
		}
	}
}

func wantHint(t *testing.T, m *Model, want string) {
	t.Helper()
	if row := statusRow(m); !strings.Contains(row, want) {
		t.Fatalf("status %q, want %q", row, want)
	}
}

func wantSent(t *testing.T, sent []sentAnswer, n int) sentAnswer {
	t.Helper()
	if len(sent) != n {
		t.Fatalf("%d answers sent, want %d: %+v", len(sent), n, sent)
	}
	if n == 0 {
		return sentAnswer{}
	}
	return sent[n-1]
}

func TestPromptsOpens(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(110, 30, append(demoAgents(), prompted("arch", yesNoPrompt()), prompted("fixes", radioPrompt()))...)
	m.layout.setFocus(project)
	if !m.menuOffers(promptsKey) {
		t.Fatal("the list does not offer it")
	}
	keys(m, "P")
	if !m.promptsShown() || m.layout.focus != agent {
		t.Fatalf("tab %q, focus %s", m.list().active, m.layout.focus)
	}
	if !strings.Contains(titleRow(m), "━[? prompts 2(-)]━") {
		t.Fatalf("title %q", titleRow(m))
	}
	if m.menuOffers(promptsKey) {
		t.Error("its own tab offers it")
	}
	// It is of no agent: nothing attaches, and agents' commands are not offered.
	if _, _, ok := m.attachedTerminal(); ok {
		t.Error("an agent attaches")
	}
	for _, key := range []string{"x", "c", infoKey} {
		if m.menuOffers(key) {
			t.Errorf("it offers %s", key)
		}
	}
	// From another tab C-q P goes back to it, and opens no second.
	keys(m, "alt+left", "C-q", "P")
	if !m.promptsShown() || len(m.list().tabs) != 2 {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}

	// With none waiting it says so, and its title drops the count and the mark.
	sentAgents(m, demoAgents()...)
	if !strings.Contains(titleRow(m), "━[prompts(-)]━") {
		t.Fatalf("title %q", titleRow(m))
	}
	wantText(t, m, "no prompts")
	wantHint(t, m, "Alt+← list")

	// The daemon keeps it as a tab of its own kind, and gives it back.
	tab, ok := apiTab(promptsTab)
	if !ok || tab.Kind != api.TabKindPrompts || tab.AgentId != nil {
		t.Fatalf("as the API names it: %+v %v", tab, ok)
	}
	if back, ok := tideTab(tab); !ok || back != promptsTab {
		t.Fatalf("back %q %v", back, ok)
	}

	keys(m, "C-q", "-")
	if m.promptsShown() || slices.Contains(m.list().tabs, promptsTab) {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}
}

// TestPromptsKept: the tab the daemon kept comes back with the project.
func TestPromptsKept(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(110, 30, prompted("arch", yesNoPrompt()))
	agents := []api.Agent{prompted("arch", yesNoPrompt())}
	identify(agents, nil)
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{
		Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents},
		Tabs:    []api.Tab{{Kind: api.TabKindPrompts}}, TabsRevision: 1,
	}})
	if !m.promptsShown() {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}
}

func TestPromptsRows(t *testing.T) {
	t.Parallel()
	gone := prompted("gone", radioPrompt())
	gone.State = api.AgentStateExited
	m, _, _ := promptsModel(t, prompted("arch", yesNoPrompt()), running("idle-one"), gone,
		prompted("fixes", radioPrompt()), prompted("pages", checkboxPrompt()))
	wantText(t, m,
		"▸ arch   yes/no    Bash command · go test ./...",
		"  fixes  decision  Which store keeps the sessions?",
		"  pages  decision  Which pages get the badge?",
	)
	wantNoText(t, m, "idle-one", "gone")
	wantHint(t, m, "↑↓ move  Enter options  o open agent")

	keys(m, "down", "down", "down")
	wantText(t, m, "▸ pages")
	keys(m, "up")
	wantText(t, m, "▸ fixes")

	// The cursor stays on its agent as rows come and go, and takes the next row
	// when its agent's prompt closes.
	sentAgents(m, prompted("new", yesNoPrompt()), prompted("arch", yesNoPrompt()), prompted("fixes", radioPrompt()), prompted("pages", checkboxPrompt()))
	wantText(t, m, "▸ fixes")
	sentAgents(m, prompted("new", yesNoPrompt()), prompted("arch", yesNoPrompt()), running("fixes"), prompted("pages", checkboxPrompt()))
	wantText(t, m, "▸ pages")

	// o opens the agent's tab and goes to it, answering nothing.
	keys(m, "o")
	if a, ok := m.activeAgent(); !ok || a.Name != "pages" || m.layout.focus != agent {
		t.Fatalf("active %q %v, focus %s", a.Name, ok, m.layout.focus)
	}
}

func TestPromptsYesNo(t *testing.T) {
	t.Parallel()
	p := yesNoPrompt()
	p.Selected = ptr(1)
	m, sent, _ := promptsModel(t, prompted("other", radioPrompt()), prompted("arch", p))
	keys(m, "down", "enter")
	wantText(t, m,
		"‹ prompts  ·  arch  ·  yes/no  ·  2 of 2",
		"    Bash command",
		"    go test ./...",
		"    Do you want to proceed?",
		"  ❯ 2. Yes, and don't ask again",
		"    3. No",
	)
	wantHint(t, m, "Enter choose")

	// Tide's own cursor: Up, Down and a digit move it, never Claude's, and a digit
	// answers nothing.
	keys(m, "up")
	wantText(t, m, "  ❯ 1. Yes")
	keys(m, "3")
	wantText(t, m, "  ❯ 3. No")
	keys(m, "down")
	wantText(t, m, "  ❯ 3. No")
	wantSent(t, *sent, 0)

	// Esc goes back to the list and keeps the cursor; Enter opens it again there.
	keys(m, "esc")
	wantText(t, m, "▸ arch")
	keys(m, "enter")
	wantText(t, m, "  ❯ 3. No")

	keys(m, "enter")
	a := wantSent(t, *sent, 1)
	if a.agentID != "agent-arch" || *a.answer.Choose != 2 || *a.answer.Question != p.Question || !slices.Equal(*a.answer.Options, p.Options) || a.answer.Tick != nil || a.answer.Text != nil {
		t.Fatalf("sent %+v", a)
	}
	// Answered, it goes back to the list, where the row stays until the stream says
	// the prompt closed.
	wantText(t, m, "▸ arch")
	sentAgents(m, prompted("other", radioPrompt()), running("arch"))
	wantNoText(t, m, "arch")
}

// TestPromptsOnce: Enter is not sent twice while the answer is on its way, and
// nothing moves meanwhile.
func TestPromptsOnce(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("arch", yesNoPrompt()))
	keys(m, "enter")
	_, cmd := m.Update(key(tea.KeyEnter, 0))
	if cmd == nil {
		t.Fatal("Enter sends nothing")
	}
	wantText(t, m, "answering…")
	wantHint(t, m, "answering…")
	for _, k := range keyMsgs("enter", "down", "enter") {
		if _, again := m.Update(k); again != nil {
			t.Fatal("a second answer while the first is on its way")
		}
	}
	wantText(t, m, "  ❯ 1. Yes")
	run(m, cmd)
	wantSent(t, *sent, 1)
}

func TestPromptsRadio(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("fixes", radioPrompt()))
	keys(m, "enter")
	wantText(t, m, "decision", "    Which store keeps the sessions?", "  ❯ 1. (•) SQLite", "    2. ( ) Files", "      ─────", "    4. Chat about this")

	// The text option takes what is typed, o and digits too; Enter with nothing
	// typed sends nothing.
	keys(m, "down", "down")
	wantHint(t, m, "type your answer  Enter send")
	keys(m, "enter")
	wantText(t, m, "type an answer first")
	wantSent(t, *sent, 0)
	keys(m, "mauve o2x", "backspace")
	wantText(t, m, "  ❯ 3. (•) mauve o2▏")
	if m.list().active != promptsTab {
		t.Fatal("o left the tab while typing")
	}
	// The text is kept on leaving the option.
	keys(m, "up")
	wantText(t, m, "    3. ( ) mauve o2")
	keys(m, "down")
	a := func() sentAnswer {
		keys(m, "enter")
		return wantSent(t, *sent, 1)
	}()
	if *a.answer.Choose != 2 || a.answer.Text == nil || *a.answer.Text != "mauve o2" {
		t.Fatalf("sent %+v", a.answer)
	}
}

// TestPromptsRadioChoice: Enter on a radio option answers it.
func TestPromptsRadioChoice(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("fixes", radioPrompt()))
	keys(m, "enter", "down", "enter")
	if a := wantSent(t, *sent, 1); *a.answer.Choose != 1 || a.answer.Text != nil {
		t.Fatalf("sent %+v", a.answer)
	}
}

// TestPromptsChat: chat declines at once and goes to the agent's tab, focused.
func TestPromptsChat(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("fixes", radioPrompt()), running("other"))
	keys(m, "enter", "4")
	wantHint(t, m, "Enter chat in the agent's tab")
	keys(m, "enter")
	if a := wantSent(t, *sent, 1); *a.answer.Choose != 3 {
		t.Fatalf("sent %+v", a.answer)
	}
	if a, ok := m.activeAgent(); !ok || a.Name != "fixes" || m.layout.focus != agent {
		t.Fatalf("active %q %v, focus %s", a.Name, ok, m.layout.focus)
	}
}

func TestPromptsCheckbox(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("pages", checkboxPrompt()))
	keys(m, "enter")
	// Claude's ticks are where tide's start.
	wantText(t, m, "  ❯ 1. [✔] Agents", "    2. [ ] Tide", "       Submit · 1 ticked", "    5. Chat about this")
	wantHint(t, m, "Space or Enter tick")

	// Space and Enter tick and untick; nothing is sent.
	keys(m, " ", "down", "enter", "down", " ", "enter")
	wantText(t, m, "    1. [ ] Agents", "    2. [✔] Tide", "  ❯ 3. [ ] Plans")
	wantSent(t, *sent, 0)

	// Typing ticks the text option; emptying it unticks it; Enter on it unticked and
	// empty asks for text.
	keys(m, "down", "enter")
	wantText(t, m, "type something first")
	keys(m, "k")
	wantText(t, m, "  ❯ 4. [✔] k▏")
	keys(m, "backspace")
	wantText(t, m, "  ❯ 4. [ ] Type something▏")
	keys(m, "kiwi")
	wantHint(t, m, "type to tick it")

	// Submit with nothing ticked sends nothing and says so.
	keys(m, "up", "up", "enter", "down", "down", "backspace", "backspace", "backspace", "backspace", "down")
	wantText(t, m, "Submit · 0 ticked")
	keys(m, "enter")
	wantText(t, m, "nothing ticked: nothing sent")
	wantSent(t, *sent, 0)

	keys(m, "up", "kiwi", "up", "up", " ", "down", "down", "down")
	wantHint(t, m, "Enter send 2 ticked")
	keys(m, "enter")
	a := wantSent(t, *sent, 1).answer
	if a.Choose != nil || !slices.Equal(*a.Tick, []int{1, 3}) || a.Text == nil || *a.Text != "kiwi" {
		t.Fatalf("sent choose %v tick %v text %v", a.Choose, a.Tick, a.Text)
	}
}

func TestPromptsQuestions(t *testing.T) {
	t.Parallel()
	m, sent, _ := promptsModel(t, prompted("polish", questionsPrompt()))
	wantText(t, m, "polish  decision  Which accent color?")
	keys(m, "enter")
	wantText(t, m, "decision (1 of 2)", "☐ Color  ☐ Size  ✔ Submit", "    Which accent color?")
	wantHint(t, m, "Enter answer, next question")

	// The first is sent as it is answered: Claude shows the next, which is no
	// change under the person.
	keys(m, "down", "enter")
	if a := wantSent(t, *sent, 1); *a.answer.Choose != 1 || *a.answer.Question != "Which accent color?" {
		t.Fatalf("sent %+v", a.answer)
	}
	sentAgents(m, prompted("polish", secondQuestion()))
	wantText(t, m, "decision (2 of 2)", "☒ Color  ☐ Size", "    Which base size?", "  ❯ 1. (•) 13 px")
	wantNoText(t, m, "changed")
	wantHint(t, m, "Enter keep, then review")

	// The last is kept in tide, and its review shows: nothing is sent.
	keys(m, "down", "down", "14", "enter")
	wantSent(t, *sent, 1)
	wantText(t, m,
		"decision (review)",
		"    Review your answers",
		"    ● Which accent color?",
		"      → Blue",
		"    ● Which base size?",
		"      → 14",
		"  ❯ 1. Submit answers",
		"    2. Back to the questions",
	)
	wantHint(t, m, "Enter submit all answers")

	// Back to the questions opens the one still open, its answer kept.
	keys(m, "down")
	wantHint(t, m, "Enter back to the question")
	keys(m, "enter")
	wantText(t, m, "  ❯ 3. (•) 14▏")
	keys(m, "up", "enter")
	wantText(t, m, "      → 14 px")
	// Esc leaves the review to the list and keeps it.
	keys(m, "esc", "enter")
	wantText(t, m, "Review your answers")

	keys(m, "enter")
	a := wantSent(t, *sent, 2).answer
	if *a.Choose != 1 || *a.Question != "Which base size?" || a.Text != nil {
		t.Fatalf("sent %+v", a)
	}
	sentAgents(m, running("polish"))
	wantText(t, m, "no prompts")
}

// TestPromptsReviewRefused: a refused review answer goes back to the question,
// and says why.
func TestPromptsReviewRefused(t *testing.T) {
	t.Parallel()
	m, sent, refuse := promptsModel(t, prompted("polish", secondQuestion()))
	keys(m, "enter", "enter")
	wantText(t, m, "Review your answers", "→ answered in the agent's tab", "→ 13 px")
	*refuse = &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodePromptChanged, Detail: "changed"}}
	keys(m, "enter")
	wantSent(t, *sent, 1)
	wantText(t, m, "not answered: the prompt changed before it was", "  ❯ 1. (•) 13 px")
	wantNoText(t, m, "Review your answers")
}

// TestPromptsChanged: a prompt that changes while open is drawn again from Claude's,
// with what tide had dropped; one that closes leaves; a refused answer says why.
func TestPromptsChanged(t *testing.T) {
	t.Parallel()
	m, sent, refuse := promptsModel(t, prompted("fixes", radioPrompt()), prompted("arch", yesNoPrompt()))
	keys(m, "enter", "down", "down", "mauve")

	// Claude's cursor moving is not a change.
	moved := radioPrompt()
	moved.Selected = ptr(1)
	sentAgents(m, prompted("fixes", moved), prompted("arch", yesNoPrompt()))
	wantText(t, m, "  ❯ 3. (•) mauve▏")
	wantNoText(t, m, "changed")

	asked := radioPrompt()
	asked.Question, asked.Selected = "Which store keeps the tokens?", ptr(1)
	sentAgents(m, prompted("fixes", asked), prompted("arch", yesNoPrompt()))
	wantText(t, m, "·  changed", "    Which store keeps the tokens?", "  ❯ 2. (•) Files", "    3. ( ) Type something.")
	wantNoText(t, m, "mauve")

	// A refused answer says why, on the tab.
	*refuse = &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeAnswerFailed, Detail: "the prompt did not close"}}
	keys(m, "enter")
	wantSent(t, *sent, 1)
	wantText(t, m, "not answered: the prompt did not close")
	keys(m, "down")
	wantNoText(t, m, "not answered")
	*refuse = errors.New("no daemon")

	// It closes while open: back to the list, its row gone.
	sentAgents(m, running("fixes"), prompted("arch", yesNoPrompt()))
	wantText(t, m, "▸ arch")
	wantNoText(t, m, "fixes", "tokens")
}
