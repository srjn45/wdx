package tide

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// menuItem is a line of the C-q menu: its key, what it does, and the word
// the status line uses for it.
type menuItem struct {
	key, label, short string
}

// menuItems are the menu's lines; the ones that cannot apply now are left
// out.
func (m *Model) menuItems() []menuItem {
	items := []menuItem{{"o", "open project", "open"}, {"p", "switch project", "switch"}}
	if _, ok := m.checkoutDir(); ok {
		items = append(items, menuItem{"t", "new terminal", "terminal"}, menuItem{"a", "new agent", "agent"})
	}
	if s, ok := m.startable(); ok {
		items = append(items, menuItem{startKey, "start " + s.name + " again", "start"})
	}
	if a, ok := m.childParent(); ok {
		items = append(items, menuItem{"c", "new child of " + a.Name, "child"})
	}
	if r, ok := m.restartable(); ok {
		items = append(items, menuItem{restartKey, "new session of " + r.name, "restart"})
	}
	if r, ok := m.removable(); ok {
		items = append(items, menuItem{"x", "remove " + r.name, "remove"})
	}
	if a, ok := m.infoTarget(); ok {
		items = append(items, menuItem{infoKey, "info of " + a.Name, "info"})
	}
	if m.settingsOffered() {
		items = append(items, menuItem{settingsKey, "settings", "settings"})
	}
	if m.list() != nil {
		items = append(items, menuItem{refreshKey, "refresh worktrees", "refresh"})
	}
	if r, ok := m.closableProject(); ok {
		items = append(items, menuItem{closeProjectKey, "close " + r.name, "close"})
	}
	if t, ok := m.closableTab(); ok {
		items = append(items, menuItem{closeTabKey, "close the tab of " + t.title(), "close tab"})
	}
	if m.programInFocus() {
		items = append(items,
			menuItem{`\`, `send C-\ to the program`, "send"},
			menuItem{"]", "send C-] to the program", "send"},
			menuItem{"C-q", "send C-q to the program", "send"})
	}
	if m.executable != "" && m.projects.loaded {
		items = append(items, menuItem{"r", "reload tide", "reload"})
	}
	return append(items, menuItem{"q", "quit", "quit"})
}

// menuOffers reports whether the menu has a line for the key now. With the
// project pane in focus such a key acts without the menu: nothing there takes
// typing, as the programs in the other panes do.
func (m *Model) menuOffers(name string) bool {
	return slices.ContainsFunc(m.menuItems(), func(it menuItem) bool { return it.key == name })
}

// checkoutDir is where C-q t creates a terminal and C-q a an agent: the first
// of the project shown's checkouts on disk, its main checkout unless it was
// removed.
func (m *Model) checkoutDir() (string, bool) {
	f := m.following
	if f == nil || f.project == nil {
		return "", false
	}
	for _, w := range f.project.Worktrees {
		if w.Present == nil || *w.Present {
			return w.Path, true
		}
	}
	return "", false
}

// terminalCreated is the answer to C-q t.
type terminalCreated struct {
	projectID string
	terminal  api.Terminal
	err       error
}

// createTerminal creates a terminal in the project shown and shows the
// terminal pane, focused; its tab is selected once the answer comes.
func (m *Model) createTerminal() tea.Cmd {
	dir, _ := m.checkoutDir()
	p := m.following.project
	m.focusTerminalPane()
	m.log.Info("terminal requested", "project", p.Name, "dir", dir)
	ref, id := "id:"+p.Id, p.Id
	return func() tea.Msg {
		r, err := m.client.CreateTerminal(m.ctx, api.CreateTerminal{Project: &ref, Dir: dir})
		return terminalCreated{projectID: id, terminal: r.Terminal, err: err}
	}
}

func (m *Model) terminalCreated(msg terminalCreated) {
	if msg.err != nil {
		m.log.Info("terminal not created", "err", msg.err)
		m.terminalNotice = msg.err.Error()
		return
	}
	m.log.Info("terminal created", "name", msg.terminal.Name)
	// Selected by id: the stream may have brought its tab already, or will.
	m.createdTab[msg.projectID] = msg.terminal.Id
	if f := m.following; f != nil && f.project != nil && f.project.Id == msg.projectID {
		m.terminalsChanged()
	}
}

// focusTerminalPane shows the terminal pane, focused, the zoom going with
// the focus.
func (m *Model) focusTerminalPane() {
	if !m.layout.shown(terminal) && m.layout.zoomed == none {
		m.layout.toggleTerminal()
	} else {
		m.layout.terminalOpen = true
		m.layout.setFocus(terminal)
		if m.layout.zoomed != none {
			m.layout.zoomed = terminal
		}
	}
	m.changed("terminal pane")
}

// closableTab returns the tab C-q - would close: only with the agent pane in
// focus, so that it cannot seem to close a terminal's tab.
func (m *Model) closableTab() (agentTab, bool) {
	l := m.list()
	if m.layout.focus != agent || l == nil {
		return agentTab{}, false
	}
	for _, t := range m.openTabs() {
		if t.tab == l.active {
			return t, true
		}
	}
	return agentTab{}, false
}

// removal is what C-q x would remove: the entity in focus.
type removal struct {
	kind, id, name string
	running        bool   // removing it stops what runs
	doing          string // what a restart would interrupt, if anything
	hasChildren    bool   // it stays as a tombstone
	worktree       *worktreeRemoval
}

// worktreeRemoval is what removing a worktree touches, for the confirmation to say.
type worktreeRemoval struct {
	missing   bool
	agents    []string
	terminals []string
}

// removable returns what C-q x would remove now.
func (m *Model) removable() (removal, bool) {
	if m.layout.focus == terminal {
		t, ok := m.selectedTerminal()
		if !ok {
			return removal{}, false
		}
		running := t.State == api.TerminalStateRunning || t.State == api.TerminalStateStarting
		return removal{kind: "terminal", id: t.Id, name: t.Name, running: running}, true
	}
	if r, ok := m.removableWorktree(); ok {
		return r, true
	}
	a, ok := m.focusedAgent()
	if !ok || a.State == api.AgentStateRemoved {
		return removal{}, false
	}
	running := a.State == api.AgentStateRunning || a.State == api.AgentStateStarting
	return removal{kind: "agent", id: a.Id, name: a.Name, running: running, hasChildren: len(childrenOf(a)) > 0}, true
}

// focusedAgent returns the agent in focus: the active one in the agent pane,
// the one under the cursor in the list.
func (m *Model) focusedAgent() (api.Agent, bool) {
	switch m.layout.focus {
	case agent:
		return m.activeAgent()
	case project:
		if _, ok := m.root.(*projectScreen); ok {
			return m.cursorAgent()
		}
	}
	return api.Agent{}, false
}

// confirmBox asks before C-q x removes, which cannot be undone, and before C-q w
// stops what runs in a project.
type confirmBox struct {
	r    removal
	busy bool // the removal was sent
}

// removed is the answer to a removal.
type removed struct {
	r       removal
	stopped *[]string
	err     error
	branch  string // the worktree's branch that stayed
	told    []string
}

// confirmKey takes a key while the confirmation is open: y removes, n or
// Esc keep.
func (m *Model) confirmKey(name string) tea.Cmd {
	c := m.confirm
	if c.busy {
		return nil
	}
	switch name {
	case "y":
		c.busy = true
		r := c.r
		if r.kind == projectKind {
			m.log.Info("closing confirmed", "name", r.name)
			return m.closeProject(r, true)
		}
		if r.kind == restartKind {
			return m.restart(r)
		}
		m.log.Info("removal confirmed", "kind", r.kind, "name", r.name)
		if r.worktree != nil {
			return m.removeWorktree(r)
		}
		return func() tea.Msg {
			var err error
			var stopped *[]string
			if r.kind == "agent" {
				var result api.AgentResult
				result, err = m.client.DeleteAgent(m.ctx, "id:"+r.id, "", "", true)
				stopped = result.StoppedSessions
			} else {
				_, err = m.client.DeleteTerminal(m.ctx, "id:"+r.id, "", "", true)
			}
			return removed{r: r, stopped: stopped, err: err}
		}
	case "n", "esc":
		m.log.Info("confirmation declined", "kind", c.r.kind, "name", c.r.name)
		m.confirm = nil
	}
	return nil
}

func (m *Model) removed(msg removed) {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("not removed", "kind", msg.r.kind, "name", msg.r.name, "err", msg.err)
		if msg.r.kind != "terminal" {
			m.statusNotice = msg.err.Error()
		} else {
			m.terminalNotice = msg.err.Error()
		}
		return
	}
	// Its tab or row goes with the stream's next event.
	m.log.Info("removed", "kind", msg.r.kind, "name", msg.r.name)
	m.noticeStopped(msg.stopped)
	if msg.r.worktree != nil {
		m.worktreeRemoved(msg)
	}
}

// confirmPopup is what the confirmation shows.
func (m *Model) confirmPopup() popup {
	c, st := m.confirm, m.st
	if c.r.kind == projectKind {
		return m.closePopup()
	}
	if c.r.kind == restartKind {
		return m.restartPopup()
	}
	body := []string{"Remove " + st.Name(c.r.name) + "?"}
	if c.r.worktree != nil {
		body = append(body, c.r.worktree.lines()...)
	}
	if c.r.running && c.r.kind == "agent" {
		body = append(body, "It is running: it is stopped.")
	} else if c.r.running {
		body = append(body, "It is running: its shell is stopped.")
	}
	if c.r.hasChildren {
		body = append(body, "It stays as removed until its children are gone.")
	}
	if c.busy {
		body = append(body, "", st.Muted("removing…"))
	}
	return popup{
		title:  "Remove " + c.r.kind,
		body:   body,
		footer: st.Command("y") + " remove  " + st.Command("n") + " or " + st.Command("Esc") + " keep",
	}
}

// menuHint is the status line's summary of the menu's keys.
func (m *Model) menuHint() string {
	var keys []string
	for _, it := range m.menuItems() {
		keys = append(keys, it.key+" "+it.short)
	}
	return "menu: " + strings.Join(keys, "  ") + "  Esc close"
}
