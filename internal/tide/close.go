package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// projectKind is the kind of a removal that closes a project instead: it asks
// the same way, and nothing else about it is the same.
const projectKind = "project"

// verb is what y does.
func (c *confirmBox) verb() string {
	if c.r.kind == projectKind {
		return "close"
	}
	return "remove"
}

// closableProject returns the project C-q w would close: the one shown, known
// from its stream and still open.
func (m *Model) closableProject() (removal, bool) {
	f := m.following
	if f == nil || f.project == nil || f.project.State != api.ProjectStateOpen {
		return removal{}, false
	}
	p := f.project
	editors := m.editorsIn("")
	return removal{kind: projectKind, id: p.Id, name: p.Name, running: m.projectRunning() || len(editors) > 0, editors: editors}, true
}

// projectRunning reports whether closing the project shown would stop anything.
func (m *Model) projectRunning() bool {
	for _, t := range m.terminals() {
		if t.State == api.TerminalStateRunning || t.State == api.TerminalStateStarting {
			return true
		}
	}
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning || a.State == api.AgentStateStarting {
			return true
		}
	}
	return false
}

// projectClosed is the answer to closing a project.
type projectClosed struct {
	name    string
	stopped *[]string
	err     error
}

// closeProject asks the daemon to close the project. Without stopRunning it
// refuses if something started since the stream last said, which the status line
// then tells.
func (m *Model) closeProject(r removal, stopRunning bool) tea.Cmd {
	m.log.Info("project closing", "name", r.name, "stop_running", stopRunning)
	return func() tea.Msg {
		result, err := m.client.UpdateProject(m.ctx, "id:"+r.id, api.ProjectStateClosed, stopRunning)
		return projectClosed{name: r.name, stopped: result.StoppedSessions, err: err}
	}
}

// closed takes the answer. It leaves the project for where tide goes at start,
// unless another was shown or a popup opened meanwhile.
func (m *Model) closed(msg projectClosed) tea.Cmd {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("project not closed", "name", msg.name, "err", msg.err)
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.log.Info("project closed", "name", msg.name)
	m.noticeStopped(msg.stopped)
	m.projects.markClosed(msg.name)
	if m.previous == msg.name {
		m.previous = ""
	}
	if m.shown != msg.name {
		return m.listProjects()
	}
	asked := m.popupOpen()
	switcher, opening := m.switcher, m.opening
	m.leaveProject()
	m.switcher, m.opening = switcher, opening
	switch {
	case asked:
	case len(m.projects.open()) > 0:
		m.switcher = newSwitcher(m.projects, m.previous)
	default:
		m.opening = []screen{&openProjectScreen{projects: m.projects, notice: "closed " + msg.name}}
	}
	return m.listProjects()
}

// markClosed says what the answer said, until the projects are listed again.
func (l *projectList) markClosed(name string) {
	for i := range l.projects {
		if l.projects[i].Name == name {
			l.projects[i].State = api.ProjectStateClosed
		}
	}
}

// closePopup is what the confirmation of closing a project shows.
func (m *Model) closePopup() popup {
	c, st := m.confirm, m.st
	body := []string{
		"Close " + st.Name(c.r.name) + "?",
		"Its running agents and terminals stop.",
		"Opening it again resumes its agents and starts fresh shells.",
	}
	if len(c.r.editors) > 0 {
		body = append(body, "Its editors stop, losing what they hold unsaved: "+strings.Join(c.r.editors, ", "))
	}
	if c.busy {
		body = append(body, "", st.Muted("closing…"))
	}
	return popup{
		title:  "Close project",
		body:   body,
		footer: st.Command("y") + " close  " + st.Command("n") + " or " + st.Command("Esc") + " keep",
	}
}
