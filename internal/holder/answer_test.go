package holder

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/holder/prompttest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/waittest"
)

// answerPatience is the answer's waits in a test: what a loaded machine does
// not reach, unless the run keeps wdx's own.
func answerPatience(t *testing.T) string {
	if runtest.ProductionLimits() {
		return ""
	}
	return waittest.Limit(t).String()
}

// prompted starts a holder whose program is prompttest asking s, read by
// testParser, with env set on it after the answer's waits. It returns the
// program's directory, the daemon's connection once the prompt reads, and the
// holder.
func (f fixture) prompted(t *testing.T, id string, s prompttest.Scenario, env ...string) (string, *Conn, Started) {
	t.Helper()
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.json")
	if err := prompttest.Write(scenario, s); err != nil {
		t.Fatal(err)
	}
	o := f.options(id, f.script(t, "cd "+dir+"\n"+prompttest.Script(f.exe, scenario)))
	o.Env = append(o.Env, parserDirVar+"="+dir)
	waits := map[string]string{answerKeyVar: answerPatience(t), answerCloseVar: answerPatience(t)}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		waits[name] = value
	}
	for name, value := range waits {
		if value != "" {
			o.Env = append(o.Env, name+"="+value)
		}
	}
	started, err := Start(limited(t), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(started) })
	c := f.dial(t, id)
	waitPrompt(t, c, func(p *screen.Prompt) bool { return p != nil })
	return dir, c, started
}

// waitPrompt waits until the holder tells the prompt it reads is as want says.
func waitPrompt(t *testing.T, c *Conn, want func(*screen.Prompt) bool) *screen.Prompt {
	t.Helper()
	for {
		if p := c.Status().Prompt; want(p) {
			return p
		}
		select {
		case <-c.StatusChanged():
		case <-c.Done():
			t.Fatal("the holder went before the prompt read")
		case <-waittest.After(t):
			t.Fatalf("the prompt reads %+v", c.Status().Prompt)
		}
	}
}

// answer sends answers against what c says the screen shows, as a person saw it.
func answer(t *testing.T, c *Conn, answers ...Answer) AnswerReport {
	t.Helper()
	report, err := c.Answer(limited(t), answers)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// seen is an answer to the prompt p, which the person saw.
func seen(p *screen.Prompt) Answer {
	return Answer{Question: p.Question, Options: slices.Clone(p.Options)}
}

func chosen(a Answer, option int) Answer {
	a.Choose = &option
	return a
}

// end ends the program and its holder, and checks no log line holds any of
// words: the prompt's question, labels and the text typed.
func (f fixture) end(t *testing.T, dir, id string, s Started, words ...string) {
	t.Helper()
	step(t, dir, "end")
	f.endHolder(t, id, s)
	for _, line := range f.logged(t) {
		b, _ := json.Marshal(line)
		for _, word := range words {
			if strings.Contains(string(b), word) {
				t.Errorf("a log line holds %q: %s", word, b)
			}
		}
	}
}

func wantAnswered(t *testing.T, dir, how string, answers ...[]string) {
	t.Helper()
	var got *prompttest.Answered
	waittest.For(t, "the program to say how the prompt closed", func() bool {
		var err error
		got, err = prompttest.ReadAnswered(dir)
		return err == nil && got != nil
	})
	if got.How != how || !slices.EqualFunc(got.Answers, answers, slices.Equal[[]string]) {
		t.Fatalf("the prompt closed %s with %v, want %s with %v", got.How, got.Answers, how, answers)
	}
}

func wantKeys(t *testing.T, dir string, want ...string) {
	t.Helper()
	if got := prompttest.Keys(dir); !slices.Equal(got, want) {
		t.Fatalf("the program took keys %v, want %v", got, want)
	}
}

func wantReport(t *testing.T, got AnswerReport, result AnswerResult, entered bool) {
	t.Helper()
	if got.Result != result || got.Entered != entered {
		t.Fatalf("report %+v, want %s with entered %v", got, result, entered)
	}
}

var (
	permission = prompttest.Scenario{YesNo: &prompttest.YesNo{Title: "Bash command", Command: "rm -rf build", Options: []string{"Yes", "Yes, and allow rm", "No"}}}
	colors     = prompttest.Question{Tab: "Color", Question: "Which color?", Options: []string{"Red", "Blue"}}
	sizes      = prompttest.Question{Tab: "Size", Question: "Which size?", Options: []string{"Small", "Large"}}
	fruits     = prompttest.Question{Tab: "Fruits", Question: "Which fruits?", Options: []string{"Apple", "Pear", "Plum"}, Multi: true}
)

// TestAnswerYesNo: the cursor goes to the option one key at a time, each seen on
// the screen, and Enter is pressed once.
func TestAnswerYesNo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000701"
	dir, c, s := f.prompted(t, id, permission)
	wantReport(t, answer(t, c, chosen(seen(c.Status().Prompt), 2)), Answered, true)
	wantAnswered(t, dir, "answered", []string{"No"})
	wantKeys(t, dir, "down", "down", "enter")
	line := f.waitLogged(t, "prompt answered")
	if intAttr(t, line, "keys") != 3 || intAttr(t, line, "answers") != 1 {
		t.Errorf("logged %v", line)
	}
	f.end(t, dir, id, s, "rm -rf build", "Do you want to proceed?", "allow rm", "Bash command")
}

// TestAnswerTypedText: a decision's text option gets its text as a paste, seen
// in its label before Enter.
func TestAnswerTypedText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000702"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{colors}})
	a := chosen(seen(c.Status().Prompt), 2)
	a.Text = "teal green"
	wantReport(t, answer(t, c, a), Answered, true)
	wantAnswered(t, dir, "answered", []string{"teal green"})
	wantKeys(t, dir, "down", "down", "paste", "enter")
	f.end(t, dir, id, s, "teal", "Which color?", "Red", "Blue", "Type something")
}

// TestAnswerTicks: a checkbox answer ticks with Space where a box must change,
// types into its text option, which ticks it, presses Enter on Submit and
// passes Claude's review with Enter on Submit answers: one Enter each.
func TestAnswerTicks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000703"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{fruits}})
	a := seen(c.Status().Prompt)
	a.Tick, a.Text = []int{0, 2, 3}, "kiwi"
	wantReport(t, answer(t, c, a), Answered, true)
	wantAnswered(t, dir, "answered", []string{"Apple", "Plum", "kiwi"})
	wantKeys(t, dir, "space", "down", "down", "space", "down", "paste", "down", "enter", "enter")
	f.waitLogged(t, "prompt review passed")
	f.end(t, dir, id, s, "kiwi", "Apple", "Plum", "Pear", "Which fruits?")
}

// TestAnswerUnticks: a box ticked on the screen and not in the answer is
// unticked; one ticked in both is left.
func TestAnswerUnticks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000704"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{fruits}})
	// A person ticks Apple and Pear on Claude's screen first.
	session, err := Attach(limited(t), identity.HolderSocket(f.home, id), id, Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.Write([]byte(keySpace + keyDown + keySpace)); err != nil {
		t.Fatal(err)
	}
	p := waitPrompt(t, c, func(p *screen.Prompt) bool { return p != nil && slices.Equal(p.Ticked, []int{0, 1}) && p.Selected == 1 })
	a := seen(p)
	a.Tick = []int{0}
	wantReport(t, answer(t, c, a), Answered, true)
	wantAnswered(t, dir, "answered", []string{"Apple"})
	wantKeys(t, dir, "space", "down", "space", "space", "down", "down", "down", "enter", "enter")
	f.end(t, dir, id, s, "Apple", "Which fruits?")
}

// TestAnswerChat: Chat about this is Enter on it, and the prompt closes.
func TestAnswerChat(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000705"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{fruits}})
	p := c.Status().Prompt
	wantReport(t, answer(t, c, chosen(seen(p), len(p.Options)-1)), Answered, true)
	wantAnswered(t, dir, "chat")
	wantKeys(t, dir, "down", "down", "down", "down", "down", "enter")
	f.end(t, dir, id, s, "Which fruits?", "Chat about this")
}

// TestAnswerTwoQuestions: each answer is given once the screen shows its
// question, and the review is passed after the last.
func TestAnswerTwoQuestions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000706"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{colors, sizes}})
	first := chosen(seen(c.Status().Prompt), 1)
	second := chosen(Answer{Question: "Which size?", Options: []string{"Small", "Large", "Type something.", "Chat about this"}}, 0)
	wantReport(t, answer(t, c, first, second), Answered, true)
	wantAnswered(t, dir, "answered", []string{"Blue"}, []string{"Small"})
	wantKeys(t, dir, "down", "enter", "enter", "enter")
	f.end(t, dir, id, s, "Which size?", "Which color?", "Blue", "Small")
}

// TestAnswerSecondQuestionChanged: a question that is not the one sent stops the
// answer there, the first answered as it stays on Claude's screen.
func TestAnswerSecondQuestionChanged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000707"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{colors, sizes}})
	first := chosen(seen(c.Status().Prompt), 0)
	second := chosen(Answer{Question: "Which shape?", Options: []string{"Small", "Large", "Type something.", "Chat about this"}}, 0)
	report := answer(t, c, first, second)
	wantReport(t, report, AnswerPromptChanged, true)
	if report.Question != 1 {
		t.Errorf("stopped at answer %d", report.Question)
	}
	wantKeys(t, dir, "enter")
	f.end(t, dir, id, s, "Which shape?", "Which size?")
}

// TestAnswerPromptChangedBefore: an answer to a prompt the screen does not show
// presses nothing.
func TestAnswerPromptChangedBefore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000708"
	dir, c, s := f.prompted(t, id, permission)
	a := chosen(seen(c.Status().Prompt), 1)
	a.Options[1] = "Yes, and allow rm -r"
	wantReport(t, answer(t, c, a), AnswerPromptChanged, false)
	other := chosen(seen(c.Status().Prompt), 1)
	other.Question = "Do you want to make this edit?"
	wantReport(t, answer(t, c, other), AnswerPromptChanged, false)
	f.end(t, dir, id, s, "allow rm")
	wantKeys(t, dir)
}

// TestAnswerPromptChangedDuring: a prompt Claude replaces while the cursor moves
// stops the answer before its next key.
func TestAnswerPromptChangedDuring(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000709"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{colors}, ChangeAfter: 1})
	wantReport(t, answer(t, c, chosen(seen(c.Status().Prompt), 2)), AnswerPromptChanged, false)
	f.end(t, dir, id, s, "Which color?")
	wantKeys(t, dir, "down")
}

// TestAnswerPersonsKey: a person's key that moves the cursor while the holder
// answers stops it: the screen is not what its own key would make.
func TestAnswerPersonsKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000710"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{YesNo: permission.YesNo, HoldAfter: 1})
	session, err := Attach(limited(t), identity.HolderSocket(f.home, id), id, Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	reported := make(chan AnswerReport, 1)
	go func() {
		report, _ := c.Answer(limited(t), []Answer{chosen(seen(c.Status().Prompt), 2)})
		reported <- report
	}()
	waittest.For(t, "the program to hold the holder's key", func() bool {
		_, err := os.Stat(filepath.Join(dir, "held"))
		return err == nil
	})
	if _, err := session.Write([]byte(keyDown)); err != nil {
		t.Fatal(err)
	}
	select {
	case report := <-reported:
		wantReport(t, report, AnswerPromptChanged, false)
	case <-waittest.After(t):
		t.Fatal("no report")
	}
	f.end(t, dir, id, s)
	wantKeys(t, dir, "down", "down")
}

// TestAnswerNotConfirmed: a prompt that stays after Enter gets no second one.
func TestAnswerNotConfirmed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000711"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{YesNo: permission.YesNo, DeafToEnter: true}, answerCloseVar+"=200ms")
	wantReport(t, answer(t, c, chosen(seen(c.Status().Prompt), 0)), AnswerNotConfirmed, true)
	f.end(t, dir, id, s)
	wantKeys(t, dir, "enter")
}

// TestAnswerInvalid: an answer the prompt cannot take presses nothing.
func TestAnswerInvalid(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000712"
	dir, c, s := f.prompted(t, id, prompttest.Scenario{Questions: []prompttest.Question{colors}})
	p := c.Status().Prompt
	ticks := seen(p)
	ticks.Tick = []int{0}
	submitless := chosen(seen(p), len(p.Options))
	text := chosen(seen(p), 0)
	text.Text = "teal"
	lines := chosen(seen(p), 2)
	lines.Text = "teal\rgreen"
	for _, a := range []Answer{ticks, submitless, text, lines, seen(p)} {
		if report := answer(t, c, a); report.Result != AnswerInvalid || report.Reason == "" {
			t.Errorf("%+v answered %+v", a, report)
		}
	}
	f.end(t, dir, id, s, "teal")
	wantKeys(t, dir)
}

// TestAnswerOnAShell: a holder that reads no screen answers nothing.
func TestAnswerOnAShell(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	id := "term-00000713"
	s := f.start(t, id, f.script(t, "exec sleep 1000"))
	c := f.dial(t, id)
	if !c.Answers() {
		t.Fatal("the holder does not say it answers")
	}
	if report := answer(t, c, chosen(Answer{Question: "Do you want to proceed?", Options: []string{"Yes"}}, 0)); report.Result != AnswerUnread {
		t.Fatalf("report %+v", report)
	}
	killHolder(s)
	waitGone(t, s.HolderPID)
}

// TestAnswerToOlderHolder: a holder whose hello does not say it answers is sent
// no answer.
func TestAnswerToOlderHolder(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(newFixture(t).dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, frameHello, Hello{Version: Version, TerminalID: "term-00000714", Confirms: true})
		_, _, _ = readFrame(conn) // until the daemon closes
	}()
	c, err := Dial(limited(t), socket, "term-00000714")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Answer(limited(t), []Answer{{Question: "q"}}); !errors.Is(err, ErrAnswerUnsupported) {
		t.Fatalf("want it refused as unsupported, got %v", err)
	}
}
