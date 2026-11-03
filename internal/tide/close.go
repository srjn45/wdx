package tide

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// projectKind is the kind of a removal that closes a project instead: it asks
// the same way, and nothing else about it is the same. projectRemovalKind removes
// the project from wdx (docs/TIDE_MENU.md, step 3).
const (
	projectKind        = "project"
	projectRemovalKind = "project removal"
)

// verb is what y does.
func (c *confirmBox) verb() string {
	switch c.r.kind {
	case projectKind:
		return "close"
	case runPlanKind:
		return "run"
	}
	return "remove"
}

// closableProject returns the project C-q p c would close: the one shown, known
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
func (m *Model) projectRunning() bool { return len(m.runningInProject()) > 0 }

// runningInProject names the agents and terminals of the project shown that run,
// or are starting, as the stream last said.
func (m *Model) runningInProject() []string {
	var names []string
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning || a.State == api.AgentStateStarting {
			names = append(names, a.Name)
		}
	}
	for _, t := range m.terminals() {
		if t.State == api.TerminalStateRunning || t.State == api.TerminalStateStarting {
			names = append(names, t.Name)
		}
	}
	return names
}

// removableProject returns the project C-q p x would remove: the one shown, as
// close's, with what runs in it named.
func (m *Model) removableProject() (removal, bool) {
	r, ok := m.closableProject()
	if !ok {
		return removal{}, false
	}
	r.kind, r.stopping = projectRemovalKind, m.runningInProject()
	return r, true
}

// projectRemoved is the answer to removing a project.
type projectRemoved struct {
	name    string
	stopped *[]string
	err     error
}

// removeProject asks the daemon to remove the project from wdx, stopping what runs
// only when the confirmation said something does: one started since is refused,
// which the status line then tells.
func (m *Model) removeProject(r removal) tea.Cmd {
	m.log.Info("project removal confirmed", "name", r.name, "stop_running", r.running)
	return func() tea.Msg {
		result, err := m.client.DeleteProject(m.ctx, "id:"+r.id, r.running)
		return projectRemoved{name: r.name, stopped: result.StoppedSessions, err: err}
	}
}

// projectWasRemoved takes the answer, and leaves the project as closing does.
func (m *Model) projectWasRemoved(msg projectRemoved) tea.Cmd {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("project not removed", "name", msg.name, "err", msg.err)
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.log.Info("project removed", "name", msg.name)
	m.noticeStopped(msg.stopped)
	m.projects.drop(msg.name)
	return m.leaveGone(msg.name, "removed "+msg.name)
}

// removeProjectPopup is what the confirmation of removing a project shows.
func (m *Model) removeProjectPopup() popup {
	c, st := m.confirm, m.st
	body := []string{
		"Remove " + st.Name(c.r.name) + " from wdx?",
		"Its agents, terminals and their history are deleted from wdx. The files on disk stay.",
	}
	if len(c.r.stopping) > 0 {
		body = append(body, "Running now, they stop: "+strings.Join(c.r.stopping, ", "))
	}
	if len(c.r.editors) > 0 {
		body = append(body, "Its editors stop, losing what they hold unsaved: "+strings.Join(c.r.editors, ", "))
	}
	if c.busy {
		body = append(body, "", st.Muted("removing…"))
	}
	return popup{
		title:  "Remove project",
		body:   body,
		footer: st.Command("y") + " remove  " + st.Command("n") + " or " + st.Command("Esc") + " keep",
	}
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
	return m.leaveGone(msg.name, "closed "+msg.name)
}

// leaveGone leaves a project closed or removed for where tide goes at start,
// unless another was shown or a popup opened meanwhile; notice says what happened
// in Open Project.
func (m *Model) leaveGone(name, notice string) tea.Cmd {
	if m.previous == name {
		m.previous = ""
	}
	if m.shown != name {
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
		m.opening = []screen{&openProjectScreen{projects: m.projects, notice: notice}}
	}
	return m.listProjects()
}

// drop forgets the project removed, until the projects are listed again.
func (l *projectList) drop(name string) {
	l.projects = slices.DeleteFunc(l.projects, func(p api.ProjectSummary) bool { return p.Name == name })
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
