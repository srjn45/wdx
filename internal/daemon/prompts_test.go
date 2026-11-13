package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/holder/prompttest"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/waittest"
)

// promptedAgent creates an agent in app whose program is prompttest asking s,
// and returns it once its prompt reads.
func (td *testDaemon) promptedAgent(t *testing.T, app string, s prompttest.Scenario) (api.Agent, api.AgentPrompt) {
	t.Helper()
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
	a := td.createAgent(t, "", app, "helper")
	return a, td.waitPrompt(t, "helper", 0)
}

func (td *testDaemon) answer(t *testing.T, a api.Agent, body api.PromptAnswers) *api.AnswerAgentPromptResponse {
	t.Helper()
	r, err := td.client.AnswerAgentPromptWithResponse(context.Background(), "id:"+a.Id, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// seenAnswer is an answer to p, which the person saw, choosing option choose.
func seenAnswer(p api.AgentPrompt, choose int) api.PromptAnswer {
	return api.PromptAnswer{Question: p.Question, Options: p.Options, Choose: &choose}
}

func one(a api.PromptAnswer) api.PromptAnswers {
	return api.PromptAnswers{Question: &a.Question, Options: &a.Options, Choose: a.Choose, Tick: a.Tick, Text: a.Text}
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

// wantNotLogged checks no line the daemon or the holder logged holds any of words.
func wantNotLogged(t *testing.T, home string, words ...string) {
	t.Helper()
	for _, line := range logLines(t, home) {
		b, _ := json.Marshal(line)
		for _, word := range words {
			if strings.Contains(string(b), word) {
				t.Errorf("a log line holds %q: %s", word, b)
			}
		}
	}
}

var colorPrompt = prompttest.Scenario{Questions: []prompttest.Question{{Tab: "Color", Question: "Which color suits?", Options: []string{"Crimson", "Azure"}}}}

// TestAnswerAgentPrompt: the answer goes to the agent's holder, which types it,
// and the agent comes back answered; the prompt's kinds reach the API, and no
// question, label or text reaches a log line.
func TestAnswerAgentPrompt(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a, p := td.promptedAgent(t, app, colorPrompt)
	if want := []api.AgentPromptKinds{"choice", "choice", "text", "chat"}; !slices.Equal(p.Kinds, want) || p.Review || len(p.Questions) != 0 || p.Answered == nil {
		t.Errorf("prompt %+v", p)
	}
	answer := seenAnswer(p, 2)
	text := "vermilion red"
	answer.Text = &text
	r := td.answer(t, a, one(answer))
	if r.JSON200 == nil || r.JSON200.Result != api.ResultAnswered || r.JSON200.Agent.Id != a.Id {
		t.Fatalf("answer: %s", r.Body)
	}
	wantClosed(t, app, "answered", []string{text})
	wantNotLogged(t, td.home, "Which color suits?", "Crimson", "Azure", "vermilion", "Type something")
}

// TestAnswerAgentPromptQuestions: a decision of several questions is answered in
// one request, and Claude's review passed.
func TestAnswerAgentPromptQuestions(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	sizes := prompttest.Question{Tab: "Size", Question: "Which size fits?", Options: []string{"Petite", "Grande"}}
	a, p := td.promptedAgent(t, app, prompttest.Scenario{Questions: []prompttest.Question{colorPrompt.Questions[0], sizes}})
	if !slices.Equal(p.Questions, []string{"Color", "Size"}) {
		t.Errorf("questions %v", p.Questions)
	}
	second := api.PromptAnswer{Question: sizes.Question, Options: []string{"Petite", "Grande", "Type something.", "Chat about this"}, Choose: new(int)}
	*second.Choose = 1
	answers := []api.PromptAnswer{seenAnswer(p, 0), second}
	r := td.answer(t, a, api.PromptAnswers{Answers: &answers})
	if r.JSON200 == nil || r.JSON200.Result != api.ResultAnswered {
		t.Fatalf("answer: %s", r.Body)
	}
	wantClosed(t, app, "answered", []string{"Crimson"}, []string{"Grande"})
	wantNotLogged(t, td.home, "Which size fits?", "Petite", "Grande")
}

// TestAnswerAgentPromptChanged: an answer to a prompt the screen does not show is
// prompt_changed, with the agent, and presses nothing.
func TestAnswerAgentPromptChanged(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a, p := td.promptedAgent(t, app, colorPrompt)
	answer := seenAnswer(p, 0)
	answer.Question = "Which colour suits?"
	r := td.answer(t, a, one(answer))
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePromptChanged)
	if got := detail(t, r.ApplicationproblemJSONDefault, "agent"); got["id"] != a.Id {
		t.Errorf("details.agent %v", got)
	}
	if (*r.ApplicationproblemJSONDefault.Details)["entered"] != false {
		t.Errorf("details %v", *r.ApplicationproblemJSONDefault.Details)
	}
	if keys := prompttest.Keys(app); len(keys) != 0 {
		t.Errorf("keys pressed: %v", keys)
	}
	wantNotLogged(t, td.home, "Which colour suits?", "Which color suits?", "Crimson")
}

// TestAnswerAgentPromptInvalid: an answer the prompt cannot take is
// invalid_argument, and a body that is neither one answer nor answers usage.
func TestAnswerAgentPromptInvalid(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a, p := td.promptedAgent(t, app, colorPrompt)
	ticked := seenAnswer(p, 0)
	ticked.Tick = &[]int{0}
	r := td.answer(t, a, one(ticked))
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	if (*r.ApplicationproblemJSONDefault.Details)["argument"] != "answer" {
		t.Errorf("details %v", *r.ApplicationproblemJSONDefault.Details)
	}
	both := one(seenAnswer(p, 0))
	both.Answers = &[]api.PromptAnswer{seenAnswer(p, 0)}
	for _, body := range []api.PromptAnswers{both, {}, {Choose: new(int)}} {
		wantProblem(t, td.answer(t, a, body).ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
	}
	if keys := prompttest.Keys(app); len(keys) != 0 {
		t.Errorf("keys pressed: %v", keys)
	}
}

// TestAnswerAgentPromptNotRunning: an agent that is not running has no prompt
// to answer.
func TestAnswerAgentPromptNotRunning(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "run"), []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := td.createExitedAgent(t, app, "helper")
	r := td.answer(t, a, one(api.PromptAnswer{Question: "q", Options: []string{"Yes"}, Choose: new(int)}))
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentNotRunning)
	r = td.answer(t, api.Agent{Id: "agent-none"}, one(api.PromptAnswer{Question: "q", Options: []string{"Yes"}, Choose: new(int)}))
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

// TestAnswerAgentPromptOtherACA: another ACA's prompts are not read, so its
// agent's are not answered.
func TestAnswerAgentPromptOtherACA(t *testing.T) {
	t.Parallel()
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.ACAs = []aca.AiCodingAgent{&acatest.ACA{}, &acatest.Bare{Name: "codex"}}
	})
	app := td.create(t, "app").Worktrees[0].Path
	codex := "codex"
	c, err := td.client.CreateAgentWithResponse(context.Background(), api.CreateAgent{Dir: app, Name: optional("helper"), Aca: &codex})
	if err != nil || c.JSON201 == nil {
		t.Fatalf("create agent: %v %s", err, c.Body)
	}
	r := td.answer(t, c.JSON201.Agent, one(api.PromptAnswer{Question: "q", Options: []string{"Yes"}, Choose: new(int)}))
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAcaUnsupported)
}
