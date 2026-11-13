package prompttest

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/screen"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// read is what Claude Code's parser reads of the program's screen now.
func read(t *testing.T, out *bytes.Buffer) screen.ScreenState {
	t.Helper()
	s := screen.New(80, 40, nil)
	defer s.Close()
	s.Write(out.Bytes())
	return aca.ClaudeCodeScreenParser{}.Parse(s.Text(), s.Undimmed())
}

// keyed has the program take keys, as one read each, and draw.
func keyed(t *testing.T, p *program, out *bytes.Buffer, keys ...string) screen.ScreenState {
	t.Helper()
	for _, k := range keys {
		out.Reset()
		p.take([]byte(k))
		p.draw()
	}
	return read(t, out)
}

const (
	up, down, space, enter = "\x1b[A", "\x1b[B", " ", "\r"
)

func paste(text string) string { return "\x1b[200~" + text + "\x1b[201~" }

func wantPrompt(t *testing.T, state screen.ScreenState, want screen.Prompt) {
	t.Helper()
	if state.Activity != screen.ActivityWaiting || !state.Prompt.Equal(&want) {
		t.Fatalf("read %s %+v, want %+v", state.Activity, state.Prompt, want)
	}
}

func decision(title, question string, selected int, kinds []screen.OptionKind, options ...string) screen.Prompt {
	return screen.Prompt{Kind: screen.PromptDecision, Title: title, Question: question, Options: options, Kinds: kinds, Selected: selected, Ticked: []int{}, Questions: []string{}, Answered: []int{}}
}

var (
	radioKinds = []screen.OptionKind{screen.OptionChoice, screen.OptionChoice, screen.OptionText, screen.OptionChat}
	multiKinds = []screen.OptionKind{screen.OptionChoice, screen.OptionChoice, screen.OptionText, screen.OptionSubmit, screen.OptionChat}
)

// TestScreensRead: the parser reads every screen the program draws as it reads
// the recordings: a permission, a radio decision and its typed text, a
// checkbox decision, two questions and the review, and the input box once a
// prompt closed.
func TestScreensRead(t *testing.T) {
	t.Parallel()
	t.Run("permission", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		p := newProgram(Scenario{YesNo: &YesNo{Title: "Bash command", Command: "go test ./...", Options: []string{"Yes", "No, and tell Claude what to do differently (esc)"}}}, &out, t.TempDir())
		p.draw()
		want := screen.Prompt{Kind: screen.PromptYesNo, Title: "Bash command", Question: "Do you want to proceed?", Detail: "go test ./...",
			Options: []string{"Yes", "No, and tell Claude what to do differently (esc)"}, Kinds: []screen.OptionKind{screen.OptionChoice, screen.OptionText},
			Selected: 0, Ticked: []int{}, Questions: []string{}, Answered: []int{}}
		wantPrompt(t, read(t, &out), want)
		want.Selected = 1
		wantPrompt(t, keyed(t, p, &out, down), want)
		if state := keyed(t, p, &out, enter); state.Activity != screen.ActivityIdle || state.Prompt != nil {
			t.Fatalf("after Enter: %s %+v", state.Activity, state.Prompt)
		}
	})
	t.Run("radio", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		p := newProgram(Scenario{Questions: []Question{{Tab: "Color", Question: "Which color?", Options: []string{"Red", "Blue"}}}}, &out, t.TempDir())
		p.draw()
		wantPrompt(t, read(t, &out), decision("Color", "Which color?", 0, radioKinds, "Red", "Blue", "Type something.", "Chat about this"))
		wantPrompt(t, keyed(t, p, &out, down, down, down), decision("Color", "Which color?", 3, radioKinds, "Red", "Blue", "Type something.", "Chat about this"))
		wantPrompt(t, keyed(t, p, &out, up, paste("teal")), decision("Color", "Which color?", 2, radioKinds, "Red", "Blue", "teal", "Chat about this"))
	})
	t.Run("checkbox", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		p := newProgram(Scenario{Questions: []Question{{Tab: "Fruits", Question: "Which fruits?", Options: []string{"Apple", "Pear"}, Multi: true}}}, &out, t.TempDir())
		p.draw()
		want := decision("Fruits", "Which fruits?", 0, multiKinds, "Apple", "Pear", "Type something", "Submit", "Chat about this")
		want.Multi = true
		wantPrompt(t, read(t, &out), want)
		want.Selected, want.Ticked, want.Options = 2, []int{0, 2}, []string{"Apple", "Pear", "kiwi", "Submit", "Chat about this"}
		wantPrompt(t, keyed(t, p, &out, space, down, down, paste("kiwi")), want)
		want.Selected = 3
		wantPrompt(t, keyed(t, p, &out, down), want)
		want.Selected = 4
		wantPrompt(t, keyed(t, p, &out, down), want)
	})
	t.Run("two questions", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		p := newProgram(Scenario{Questions: []Question{
			{Tab: "Color", Question: "Which color?", Options: []string{"Red", "Blue"}},
			{Tab: "Size", Question: "Which size?", Options: []string{"Small", "Large"}},
		}}, &out, t.TempDir())
		p.draw()
		first := decision("Color", "Which color?", 0, radioKinds, "Red", "Blue", "Type something.", "Chat about this")
		first.Questions = []string{"Color", "Size"}
		wantPrompt(t, read(t, &out), first)
		second := decision("Size", "Which size?", 0, radioKinds, "Small", "Large", "Type something.", "Chat about this")
		second.Questions, second.Answered = []string{"Color", "Size"}, []int{0}
		wantPrompt(t, keyed(t, p, &out, enter), second)
		review := decision("Review your answers", "Ready to submit your answers?", 0, []screen.OptionKind{screen.OptionChoice, screen.OptionChoice}, "Submit answers", "Cancel")
		review.Questions, review.Answered, review.Review = []string{"Color", "Size"}, []int{0, 1}, true
		wantPrompt(t, keyed(t, p, &out, down, enter), review)
		if state := keyed(t, p, &out, enter); state.Prompt != nil {
			t.Fatalf("after Submit answers: %+v", state.Prompt)
		}
		if !slices.Equal(p.answers[0], []string{"Red"}) || !slices.Equal(p.answers[1], []string{"Large"}) {
			t.Errorf("answers %v", p.answers)
		}
	})
}

// TestNextKey: keys split off a read as the holder writes them.
func TestNextKey(t *testing.T) {
	t.Parallel()
	in := []byte(down + paste("a b") + "x" + enter + "\x1b")
	var names []string
	for {
		key, rest, ok := nextKey(in)
		if !ok {
			break
		}
		names = append(names, keyName(key))
		in = rest
	}
	if want := []string{"down", "paste", "char", "enter", "esc"}; !slices.Equal(names, want) {
		t.Fatalf("keys %v, want %v", names, want)
	}
}
