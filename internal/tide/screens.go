package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// screen is what the project pane shows, or one level of the [Open Project]
// popup's stack (docs/TIDE.md): it gets the keys while it is on top, and draws
// only inside the area it is given.
type screen interface {
	title() string
	// update handles a key, or the answer to a request the screen asked
	// for. To change the stack it returns a command whose message does.
	update(msg tea.Msg) tea.Cmd
	draw(buf uv.Screen, area uv.Rectangle, st style.Style)
}

// browse pushes the directory browser: to open a local repository, or to
// create a project in a directory when create.
type browse struct{ create bool }

// pushScreen pushes s on the stack.
type pushScreen struct{ s screen }

// popScreen takes the top screen off the stack; the last closes the popup.
type popScreen struct{}

// cancelOpen leaves the screens above [Open Project] for it.
type cancelOpen struct{}

// hinter is a screen that says what its keys do on the status line.
type hinter interface{ hint() string }

// Titles of [Open Project] and the screens above it, which are paths so it
// is clear where Esc goes.
const (
	openProjectTitle = "Open Project"
	localTitle       = openProjectTitle + " › Local"
	cloneTitle       = openProjectTitle + " › Clone"
	createTitle      = openProjectTitle + " › Create"
)

func pop() tea.Msg { return popScreen{} }

// send returns a command whose message is msg.
func send(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// Keys that move the selection of a screen no typing goes to, besides the
// arrows.
var (
	upKeys   = []string{"up", "k"}
	downKeys = []string{"down", "j"}
)

// wrap breaks text into lines of at most width cells, between words where
// it can.
func wrap(text string, width int) string { return ansi.Wrap(text, max(width, 1), "") }

// noProjectScreen is the project pane while no project is shown.
type noProjectScreen struct{}

func (noProjectScreen) title() string { return "No Project" }

func (noProjectScreen) update(tea.Msg) tea.Cmd { return nil }

func (noProjectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	lines := []string{
		st.Muted("no project open"),
		"",
		st.Command("C-q p") + " switch project",
		st.Command("C-q o") + " open project",
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// openProjectScreen is [Open Project]: the ways to open a project, then
// the closed projects to reopen, found by typing part of their names.
type openProjectScreen struct {
	projects *projectList
	filter   string
	selected int    // an index into entries
	opening  string // the project being reopened, while it is
	notice   string // what happened last, a project removed
	failure  string // why the last reopen failed
}

// cloneField asks for the URL of a repository to clone.
func cloneField() *fieldScreen {
	return &fieldScreen{
		heading: cloneTitle,
		prompt:  "URL of the git repository to clone:",
		empty:   "type a URL",
		working: "cloning…",
		submit:  func(url string) tea.Msg { return createProject{req: gitRequest(url)} },
	}
}

// sources are the entries that open a project not known yet.
var sources = []string{"Open a local repository", "Clone a git repository", "Create a new project"}

func (s *openProjectScreen) title() string { return openProjectTitle }

func (s *openProjectScreen) hint() string { return "↑↓ select  enter choose  Esc close" }

// closed returns the closed projects whose names match the filter.
func (s *openProjectScreen) closed() []string {
	var closed []string
	for _, name := range s.projects.names(api.ProjectStateClosed) {
		if fuzzy(s.filter, name) {
			closed = append(closed, name)
		}
	}
	return closed
}

// entries are what can be selected: the sources, then the closed projects
// the filter matches.
func (s *openProjectScreen) entries() []string {
	return append(append([]string{}, sources...), s.closed()...)
}

// filtered selects the first closed project matching the filter, once it
// changed, or the first source when none does or the filter is empty.
func (s *openProjectScreen) filtered() {
	s.selected = 0
	if s.filter != "" && len(s.closed()) > 0 {
		s.selected = len(sources)
	}
}

func (s *openProjectScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case projectOpened: // only a failure reaches the screen
		s.opening, s.failure = "", msg.err.Error()
	case tea.KeyPressMsg:
		if s.opening != "" {
			return nil
		}
		entries := s.entries()
		s.selected = min(s.selected, len(entries)-1)
		s.notice, s.failure = "", ""
		switch key := msg.String(); {
		case key == "esc":
			return pop
		case key == "up":
			s.selected = max(s.selected-1, 0)
		case key == "down":
			s.selected = min(s.selected+1, len(entries)-1)
		case key == "enter" && s.selected == 0:
			return send(browse{})
		case key == "enter" && s.selected == 1:
			return send(pushScreen{cloneField()})
		case key == "enter" && s.selected == 2:
			return send(browse{create: true})
		case key == "enter":
			s.opening = entries[s.selected]
			return send(reopenProject{name: s.opening})
		case key == "backspace" && s.filter != "":
			r := []rune(s.filter)
			s.filter = string(r[:len(r)-1])
			s.filtered()
		case typed(msg) && len(s.projects.names(api.ProjectStateClosed)) > 0:
			s.filter += msg.Text
			s.filtered()
		}
	}
	return nil
}

func (s *openProjectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	// The filter and what happened stay in view; only the list between scrolls.
	var head, foot []string
	someClosed := len(s.projects.names(api.ProjectStateClosed)) > 0
	if someClosed {
		head = []string{"> " + s.filter + "_", ""}
	}
	switch {
	case s.opening != "":
		foot = []string{"", st.Muted(wrap("opening "+s.opening+"…", area.Dx()))}
	case s.failure != "":
		foot = []string{"", st.Failure(wrap(s.failure, area.Dx()))}
	case s.notice != "":
		foot = []string{"", st.Muted(wrap(s.notice, area.Dx()))}
	}
	entries := s.entries()
	selected := min(s.selected, len(entries)-1)
	var lines []string
	selectedLine := 0
	for i, e := range entries {
		if i == len(sources) {
			lines = append(lines, "", st.Muted("closed projects"))
		}
		mark, text := "  ", e
		if i == selected {
			mark, text, selectedLine = st.Focused("▸ "), st.Name(e), len(lines)
		}
		lines = append(lines, mark+text)
	}
	if someClosed && len(entries) == len(sources) {
		lines = append(lines, "", st.Muted("no closed project matches"))
	}
	footLines := strings.Count(strings.Join(foot, "\n"), "\n") + min(len(foot), 1)
	if over := selectedLine - (area.Dy() - len(head) - footLines) + 1; over > 0 {
		lines = lines[over:]
	}
	if rows := max(area.Dy()-len(head)-footLines, 0); len(lines) > rows {
		lines = lines[:rows]
	}
	uv.NewStyledString(strings.Join(append(append(head, lines...), foot...), "\n")).Draw(buf, area)
}
