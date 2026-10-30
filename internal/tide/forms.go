package tide

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// fieldScreen asks for one line of text, a URL or a name, and sends what
// submit makes of it. It waits for the answer, which Esc cannot cancel: the
// daemon finishes a change even when its client leaves.
type fieldScreen struct {
	heading string
	prompt  string // what to type, above the field
	empty   string // the notice when enter is pressed with nothing typed
	working string // what it says while it waits for the answer, "cloning…"
	submit  func(value string) tea.Msg
	value   string
	busy    bool
	failure string // why the last submit failed
}

func (s *fieldScreen) title() string { return s.heading }

func (s *fieldScreen) hint() string { return "type  enter submit  Esc back" }

func (s *fieldScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case projectOpened: // only a failure reaches the screen
		s.busy, s.failure = false, msg.err.Error()
	case tea.KeyPressMsg:
		if s.busy {
			return nil
		}
		s.failure = ""
		switch key := msg.String(); {
		case key == "esc":
			return pop
		case key == "enter" && strings.TrimSpace(s.value) == "":
			s.failure = s.empty
		case key == "enter":
			s.busy = true
			return send(s.submit(strings.TrimSpace(s.value)))
		case key == "backspace" && s.value != "":
			r := []rune(s.value)
			s.value = string(r[:len(r)-1])
		case typed(msg):
			s.value += msg.Text
		}
	}
	return nil
}

// paste types text, its line breaks dropped.
func (s *fieldScreen) paste(text string) {
	if !s.busy {
		s.value += strings.Join(strings.Fields(text), " ")
	}
}

func (s *fieldScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	// A long value loses its start, so what is typed stays in view.
	field := "> " + s.value + "_"
	if over := ansi.StringWidth(field) - area.Dx(); over > 0 {
		field = "…" + ansi.TruncateLeft(field, over+1, "")
	}
	lines := append(strings.Split(wrap(s.prompt, area.Dx()), "\n"), "", field)
	switch {
	case s.busy:
		lines = append(lines, "", st.Muted(s.working))
	case s.failure != "":
		lines = append(lines, "")
		for _, l := range strings.Split(wrap(s.failure, area.Dx()), "\n") {
			lines = append(lines, st.Failure(l))
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// choiceScreen asks a question with a few answers, the CLI's prompts in
// tide (docs/TIDE.md): enter sends the message of the one selected.
type choiceScreen struct {
	heading  string
	question string
	choices  []choice
	selected int
	busy     string // what it says while it waits for the answer
	failure  string
}

type choice struct {
	label   string
	msg     tea.Msg
	working string // what the screen says once it is chosen; "" when no answer is waited for
}

func (s *choiceScreen) title() string { return s.heading }

func (s *choiceScreen) hint() string { return "↑↓ select  enter choose  Esc back" }

func (s *choiceScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case projectOpened:
		s.busy, s.failure = "", msg.err.Error()
	case tea.KeyPressMsg:
		if s.busy != "" {
			return nil
		}
		s.failure = ""
		switch key := msg.String(); {
		case key == "esc":
			return pop
		case slices.Contains(upKeys, key):
			s.selected = max(s.selected-1, 0)
		case slices.Contains(downKeys, key):
			s.selected = min(s.selected+1, len(s.choices)-1)
		case key == "enter":
			c := s.choices[s.selected]
			s.busy = c.working
			return send(c.msg)
		}
	}
	return nil
}

func (s *choiceScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	lines := append(strings.Split(wrap(s.question, area.Dx()), "\n"), "")
	for i, c := range s.choices {
		if i == s.selected {
			lines = append(lines, st.Focused("▸ ")+st.Name(c.label))
		} else {
			lines = append(lines, "  "+c.label)
		}
	}
	switch {
	case s.busy != "":
		lines = append(lines, "", st.Muted(wrap(s.busy, area.Dx())))
	case s.failure != "":
		lines = append(lines, "")
		for _, l := range strings.Split(wrap(s.failure, area.Dx()), "\n") {
			lines = append(lines, st.Failure(l))
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
