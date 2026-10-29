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

// projectList is the projects tide knows, as last listed.
type projectList struct {
	projects []api.ProjectSummary // every project, by name
	loaded   bool                 // listed at least once
	listing  bool
	err      error // why the last list failed
}

// names returns the names of the projects in state, in order.
func (l *projectList) names(state api.ProjectState) []string {
	var names []string
	for _, p := range l.projects {
		if p.State == state {
			names = append(names, p.Name)
		}
	}
	return names
}

func (l *projectList) open() []string { return l.names(api.ProjectStateOpen) }

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
	l := m.projects
	first := !l.loaded
	l.listing, l.err = false, msg.err
	if msg.err != nil {
		m.log.Info("projects not listed", "err", msg.err)
		return
	}
	l.projects, l.loaded = msg.projects, true
	if !first {
		return
	}
	m.switcher = len(l.open()) > 0
	start := "open project"
	if m.switcher {
		start = "switcher"
	}
	m.log.Info("tide starts", "on", start, "projects", len(l.projects), "open", len(l.open()))
}

// reopenProject asks tide to open the closed project name.
type reopenProject struct{ name string }

// projectReopened is the answer to reopening a project.
type projectReopened struct {
	name string
	err  error
}

func (m *Model) reopen(name string) tea.Cmd {
	m.log.Info("project reopening", "name", name)
	return func() tea.Msg {
		_, err := m.client.UpdateProject(m.ctx, name, api.ProjectStateOpen, false)
		return projectReopened{name: name, err: err}
	}
}

// drawStatus draws why the projects are not known yet: they are being
// listed, or the daemon could not list them.
func (l *projectList) drawStatus(buf uv.Screen, area uv.Rectangle, st style.Style) {
	var lines []string
	_, unavailable := errors.AsType[*client.UnavailableError](l.err)
	switch {
	case l.listing || l.err == nil:
		lines = []string{st.Muted("loading…")}
	case unavailable:
		lines = []string{
			st.Failure("the daemon is not running"),
			"",
			"start it with " + st.Command(identity.Name+" daemon start"),
			"then press " + st.Command(retryKey) + " to try again",
		}
	default:
		lines = []string{
			st.Failure("the projects could not be listed:"),
			wrap(l.err.Error(), area.Dx()),
			"",
			"press " + st.Command(retryKey) + " to try again",
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
