package tide

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// retryKey lists the projects again after the daemon could not.
const retryKey = "r"

// projectsListed is the answer to listing the projects.
type projectsListed struct {
	projects []api.ProjectSummary
	err      error
}

// listProjects lists every project, in the order of their names.
func (m *Model) listProjects() tea.Cmd {
	m.projects.listing = true
	return func() tea.Msg {
		list, err := m.client.ListProjects(m.ctx, "")
		return projectsListed{projects: list.Projects, err: err}
	}
}

// listed takes the projects listed. The first list decides where tide
// starts: on the switcher when any project is open, on [Open Project]
// otherwise.
func (m *Model) listed(msg projectsListed) {
	ps := m.projects
	first := !ps.loaded
	ps.listing, ps.err = false, msg.err
	if msg.err != nil {
		m.log.Info("projects not listed", "err", msg.err)
		return
	}
	ps.projects, ps.loaded = msg.projects, true
	if !first {
		return
	}
	m.switcher = len(ps.open()) > 0
	start := "open project"
	if m.switcher {
		start = "switcher"
	}
	m.log.Info("tide starts", "on", start, "projects", len(ps.projects), "open", len(ps.open()))
}

// projectsPane is the project pane until it holds screens: the projects
// tide knows, or why it does not know them.
type projectsPane struct {
	projects []api.ProjectSummary // every project, by name, as last listed
	loaded   bool                 // listed at least once
	listing  bool
	err      error // why the last list failed
}

// open returns the names of the open projects.
func (ps *projectsPane) open() []string {
	return ps.names(api.ProjectStateOpen)
}

func (ps *projectsPane) names(state api.ProjectState) []string {
	var names []string
	for _, p := range ps.projects {
		if p.State == state {
			names = append(names, p.Name)
		}
	}
	return names
}

func (ps *projectsPane) input(string) {}

func (ps *projectsPane) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	var lines []string
	_, unavailable := errors.AsType[*client.UnavailableError](ps.err)
	switch {
	case ps.listing, !ps.loaded && ps.err == nil:
		lines = []string{st.Muted("loading…")}
	case unavailable:
		lines = []string{
			st.Failure("the daemon is not running"),
			"",
			"start it with " + st.Command(identity.Name+" daemon start"),
			"then press " + st.Command(retryKey) + " to try again",
		}
	case ps.err != nil:
		lines = []string{
			st.Failure("the projects could not be listed:"),
			ps.err.Error(),
			"",
			"press " + st.Command(retryKey) + " to try again",
		}
	case len(ps.projects) == 0:
		lines = []string{st.Muted("no projects yet")}
	default:
		lines = []string{
			st.Muted("open: ") + strings.Join(ps.open(), ", "),
			st.Muted("closed: ") + strings.Join(ps.names(api.ProjectStateClosed), ", "),
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
