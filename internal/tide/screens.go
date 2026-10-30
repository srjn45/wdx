package tide

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// screen is one level of the project pane's stack (docs/TIDE.md): it gets
// the keys while it is on top, and draws only inside the area it is given.
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

// popScreen takes the top screen off the stack, unless it is the root.
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

// Keys that move a screen's selection, besides the arrows.
var (
	upKeys   = []string{"up", "k"}
	downKeys = []string{"down", "j"}
)

// wrap breaks text into lines of at most width cells, between words where
// it can.
func wrap(text string, width int) string { return ansi.Wrap(text, max(width, 1), "") }

// projectScreen is the project shown, [Project: <name>]: for now only its
// name; the project detail screen comes later.
type projectScreen struct{ name string }

func (s *projectScreen) title() string { return "Project: " + s.name }

func (s *projectScreen) update(tea.Msg) tea.Cmd { return nil }

func (s *projectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	uv.NewStyledString(st.Name(s.name)+"\n"+st.Muted("its detail comes later")).Draw(buf, area)
}

// openProjectScreen is [Open Project]: the ways to open a project, then
// the closed projects to reopen.
type openProjectScreen struct {
	projects *projectList
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

// entries are what can be selected: the sources, then the closed projects.
func (s *openProjectScreen) entries() []string {
	return append(append([]string{}, sources...), s.projects.names(api.ProjectStateClosed)...)
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
		case slices.Contains(upKeys, key):
			s.selected = max(s.selected-1, 0)
		case slices.Contains(downKeys, key):
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
		}
	}
	return nil
}

func (s *openProjectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
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
	switch {
	case s.opening != "":
		lines = append(lines, "", st.Muted(wrap("opening "+s.opening+"…", area.Dx())))
	case s.failure != "":
		lines = append(lines, "", st.Failure(wrap(s.failure, area.Dx())))
	case s.notice != "":
		lines = append(lines, "", st.Muted(wrap(s.notice, area.Dx())))
	}
	// Scrolled so the selection stays in view.
	if over := selectedLine - area.Dy() + 1; over > 0 {
		lines = lines[over:]
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
