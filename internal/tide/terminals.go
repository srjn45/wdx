package tide

import (
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
	if r, ok := m.removable(); ok {
		items = append(items, menuItem{"x", "remove " + r.name, "remove"})
	}
	return append(items, menuItem{"q", "quit", "quit"})
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
	m.selectedTab[msg.projectID] = msg.terminal.Id
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

// removal is what C-q x would remove: the entity in focus.
type removal struct {
	kind, id, name string
	running        bool // removing it stops what runs
}

// removable returns what C-q x would remove now.
func (m *Model) removable() (removal, bool) {
	switch m.layout.focus {
	case terminal:
		t, ok := m.selectedTerminal()
		if !ok {
			return removal{}, false
		}
		running := t.State == api.TerminalStateRunning || t.State == api.TerminalStateStarting
		return removal{kind: "terminal", id: t.Id, name: t.Name, running: running}, true
	case agent:
		a, ok := m.activeAgent()
		return agentRemoval(a), ok
	case project:
		if _, ok := m.root.(*projectScreen); ok {
			a, ok := m.cursorAgent()
			return agentRemoval(a), ok
		}
	}
	return removal{}, false
}

func agentRemoval(a api.Agent) removal {
	running := a.State == api.AgentStateRunning || a.State == api.AgentStateStarting
	return removal{kind: "agent", id: a.Id, name: a.Name, running: running}
}

// confirmBox asks before C-q x removes: removing cannot be undone.
type confirmBox struct {
	r    removal
	busy bool // the removal was sent
}

// removed is the answer to a removal.
type removed struct {
	r   removal
	err error
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
		m.log.Info("removal confirmed", "kind", r.kind, "name", r.name)
		return func() tea.Msg {
			var err error
			if r.kind == "agent" {
				_, err = m.client.DeleteAgent(m.ctx, "id:"+r.id, "", "", true)
			} else {
				_, err = m.client.DeleteTerminal(m.ctx, "id:"+r.id, "", "", true)
			}
			return removed{r: r, err: err}
		}
	case "n", "esc":
		m.log.Info("removal kept", "kind", c.r.kind, "name", c.r.name)
		m.confirm = nil
	}
	return nil
}

func (m *Model) removed(msg removed) {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("not removed", "kind", msg.r.kind, "name", msg.r.name, "err", msg.err)
		if msg.r.kind == "agent" {
			m.statusNotice = msg.err.Error()
		} else {
			m.terminalNotice = msg.err.Error()
		}
		return
	}
	// Its tab or row goes with the stream's next event.
	m.log.Info("removed", "kind", msg.r.kind, "name", msg.r.name)
}

// confirmPopup is what the confirmation shows.
func (m *Model) confirmPopup() popup {
	c, st := m.confirm, m.st
	body := []string{"Remove " + st.Name(c.r.name) + "?"}
	if c.r.running && c.r.kind == "agent" {
		body = append(body, "It is running: it is stopped.")
	} else if c.r.running {
		body = append(body, "It is running: its shell is stopped.")
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
