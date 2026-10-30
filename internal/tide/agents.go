package tide

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The project pane lists the agents of the project shown, and the agent pane
// shows the active one (docs/TIDE_B4.md). The arrows move the cursor; Enter
// makes the agent under it active.

// minAgentName is the room a name keeps before the detail beside it gives way.
const minAgentName = 10

// selectHint is what the status line says while the list takes the keys.
const selectHint = "↑↓ select  Enter show  Alt+arrows move  C-q menu"

// agentList is what tide remembers of a project's Agents list for the run.
type agentList struct {
	cursor  string // the id of the agent under the cursor
	row     int    // its row, which the next agent takes when it goes
	active  string // the id of the agent the agent pane shows, or ""
	offset  int    // the rows scrolled past
	opened  bool   // the project came once, and its first running agent became active
	created string // the id of an agent created here, active once the stream brings it
}

// list returns the Agents list of the project shown, nil until it is known.
func (m *Model) list() *agentList {
	f := m.following
	if f == nil || f.project == nil {
		return nil
	}
	l, ok := m.agentLists[f.project.Id]
	if !ok {
		l = &agentList{}
		m.agentLists[f.project.Id] = l
	}
	return l
}

// agents returns the agents of the project shown, by name.
func (m *Model) agents() []api.Agent {
	if f := m.following; f != nil && f.project != nil && f.project.Agents != nil {
		return *f.project.Agents
	}
	return nil
}

func agentIndex(agents []api.Agent, id string) int {
	return slices.IndexFunc(agents, func(a api.Agent) bool { return a.Id == id })
}

// agentsChanged keeps the cursor on its agent, or on the one that took its
// row; an active agent that is gone leaves the agent pane empty. The first
// time a project comes, its first running agent becomes active.
func (m *Model) agentsChanged() {
	l, agents := m.list(), m.agents()
	if i := agentIndex(agents, l.created); i >= 0 {
		l.active, l.cursor, l.created = l.created, l.created, ""
	}
	if !l.opened {
		l.opened = true
		if i := slices.IndexFunc(agents, func(a api.Agent) bool { return a.State == api.AgentStateRunning }); i >= 0 {
			l.active, l.cursor = agents[i].Id, agents[i].Id
			m.log.Info("agent active", "name", agents[i].Name, "reason", "first running")
		}
	}
	if agentIndex(agents, l.active) < 0 {
		l.active = ""
	}
	if i := agentIndex(agents, l.cursor); i >= 0 {
		l.row = i
		return
	}
	l.row = max(min(l.row, len(agents)-1), 0)
	l.cursor = ""
	if len(agents) > 0 {
		l.cursor = agents[l.row].Id
	}
}

// cursorAgent returns the agent under the cursor.
func (m *Model) cursorAgent() (api.Agent, bool) {
	l, agents := m.list(), m.agents()
	if l == nil || l.row >= len(agents) {
		return api.Agent{}, false
	}
	return agents[l.row], true
}

// activeAgent returns the agent the agent pane shows.
func (m *Model) activeAgent() (api.Agent, bool) {
	l, agents := m.list(), m.agents()
	if l == nil {
		return api.Agent{}, false
	}
	if i := agentIndex(agents, l.active); i >= 0 {
		return agents[i], true
	}
	return api.Agent{}, false
}

// moveCursor puts the cursor on the row i.
func (m *Model) moveCursor(i int) {
	l, agents := m.list(), m.agents()
	if l == nil || i < 0 || i >= len(agents) {
		return
	}
	l.row, l.cursor = i, agents[i].Id
}

// activate makes the agent under the cursor active.
func (m *Model) activate() {
	if a, ok := m.cursorAgent(); ok {
		m.list().active = a.Id
		m.log.Info("agent active", "name", a.Name, "reason", "chosen")
	}
}

// terminalOf is a's terminal, as the panes show one.
func terminalOf(a api.Agent) api.Terminal {
	return api.Terminal{
		Id: a.TerminalId, Name: a.Name, Dir: a.Dir, State: api.TerminalState(a.State),
		ExitCode: a.ExitCode, Error: a.Error,
	}
}

// agentCreated is the answer to C-q a.
type agentCreated struct {
	projectID string
	agent     api.Agent
	err       error
}

// createAgent creates an agent in the project shown and focuses the agent
// pane; it becomes active once the answer comes.
func (m *Model) createAgent() tea.Cmd {
	dir, _ := m.checkoutDir()
	p := m.following.project
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("agent pane")
	m.log.Info("agent requested", "project", p.Name, "dir", dir)
	ref, id := "id:"+p.Id, p.Id
	return func() tea.Msg {
		r, err := m.client.CreateAgent(m.ctx, api.CreateAgent{Project: &ref, Dir: dir})
		return agentCreated{projectID: id, agent: r.Agent, err: err}
	}
}

func (m *Model) agentCreated(msg agentCreated) {
	if msg.err != nil {
		m.log.Info("agent not created", "err", msg.err)
		m.statusNotice = msg.err.Error()
		return
	}
	m.log.Info("agent created", "name", msg.agent.Name)
	l, ok := m.agentLists[msg.projectID]
	if !ok {
		return
	}
	// Made active by id: the stream may have brought it already, or will.
	l.created = msg.agent.Id
	if f := m.following; f != nil && f.project != nil && f.project.Id == msg.projectID {
		m.agentsChanged()
	}
}

// projectScreen is the project shown, [Project: <name>]: for now its Agents
// list; the project detail screen comes later.
type projectScreen struct {
	m    *Model
	name string
}

func (s *projectScreen) title() string { return "Project: " + s.name }

func (s *projectScreen) hint() string {
	if len(s.m.agents()) == 0 {
		return defaultHint
	}
	return selectHint
}

func (s *projectScreen) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	l := s.m.list()
	if !ok || l == nil {
		return nil
	}
	switch name := key.String(); {
	case slices.Contains(upKeys, name):
		s.m.moveCursor(l.row - 1)
	case slices.Contains(downKeys, name):
		s.m.moveCursor(l.row + 1)
	case name == "enter":
		s.m.activate()
	}
	return nil
}

// click takes a click on the row of the pane's area: on an agent's, it moves
// the cursor there and makes the agent active.
func (s *projectScreen) click(row int) {
	l := s.m.list()
	if l == nil || row < 1 || l.offset+row-1 >= len(s.m.agents()) {
		return
	}
	s.m.moveCursor(l.offset + row - 1)
	s.m.activate()
}

func (s *projectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	m := s.m
	l, agents := m.list(), m.agents()
	width := area.Dx()
	count := fmt.Sprint(len(agents)) + " "
	if l == nil {
		count = ""
	}
	gap := max(width-len(" Agents")-len(count), 0)
	lines := []string{st.Section(ansi.Truncate(" Agents"+strings.Repeat(" ", gap)+count, width, ""))}
	switch {
	case l == nil:
		lines = append(lines, st.Muted(" loading…"))
	case len(agents) == 0:
		lines = append(lines, st.Muted(" no agents: ")+st.Command("C-q a")+st.Muted(" creates one"))
	default:
		// Scrolled so the cursor stays in view, under the heading.
		body := max(area.Dy()-1, 1)
		l.offset = max(min(l.offset, l.row), l.row-body+1)
		l.offset = max(min(l.offset, len(agents)-body), 0)
		for i := l.offset; i < min(l.offset+body, len(agents)); i++ {
			cursor := i == l.row && m.layout.focus == project
			lines = append(lines, agentRow(st, agents[i], cursor, agents[i].Id == l.active, width))
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// agentRow is a's row: the cursor when it is there, its state mark, its name,
// and at the row's end what its state does not tell and, when active, a mark
// that shows with color off too.
func agentRow(st style.Style, a api.Agent, cursor, active bool, width int) string {
	pointer, end, name := "  ", " ", a.Name
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if active {
		end = " " + st.Focused("◂") + " "
	}
	room := width - ansi.StringWidth("▸ ● ") - style.Width(end)
	detail := agentDetail(a)
	if room-len(detail)-1 < minAgentName {
		detail = ""
	}
	if detail != "" {
		room -= len(detail) + 1
	}
	name = ansi.Truncate(name, max(room, 0), "…")
	gap := strings.Repeat(" ", max(room-ansi.StringWidth(name), 0))
	if cursor || active {
		name = st.Name(name)
	}
	if detail != "" {
		detail = " " + st.Muted(detail)
	}
	row := pointer + stateMark(st, api.TerminalState(a.State)) + " " + name + gap + detail + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// agentDetail is what the row says of a's state beside its mark; running is
// the mark alone.
func agentDetail(a api.Agent) string {
	switch {
	case a.State == api.AgentStateRunning:
		return ""
	case a.ExitCode != nil:
		return fmt.Sprintf("%s %d", a.State, *a.ExitCode)
	}
	return string(a.State)
}

// agentPane is what the agent pane shows: the active agent, or why there is
// none.
type agentPane struct{ m *Model }

func (p agentPane) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	m := p.m
	lines, following := m.followState(st, area.Dx())
	active, ok := m.activeAgent()
	switch {
	case !following:
	case ok:
		m.drawShown(buf, area, st, agent, terminalOf(active), nil)
		return
	case len(m.agents()) == 0:
		lines = []string{st.Muted("no agents: ") + st.Command("C-q a") + st.Muted(" creates one")}
	default:
		lines = []string{st.Muted("no agent shown: ") + st.Command("Enter") + st.Muted(" on one in the list shows it")}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}
