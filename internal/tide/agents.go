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

// The project pane lists the agents of the project shown as a tree, and the
// agent pane shows the active one (docs/TIDE_B4.md, docs/CHILD_AGENTS.md). The
// arrows move the cursor and fold; Enter makes the agent under it active.

// minAgentName is the room a name keeps before the detail beside it gives way,
// and before the guides do.
const minAgentName = 10

// The status line says these while the list takes the keys, with the fold keys
// when there is something to fold.
const (
	selectHint = "↑↓ select  Enter show  Alt+arrows move  C-q menu"
	treeHint   = "↑↓ select  ←→ fold  Enter show  Alt+arrows move  C-q menu"
)

// The fold marks of a parent's row. ▸ is taken by the cursor.
const (
	foldOpen   = "−"
	foldClosed = "+"
)

// agentList is what tide remembers of a project's Agents list for the run.
type agentList struct {
	cursor  string          // the id of the agent under the cursor
	row     int             // its row, which the next agent takes when it goes
	active  string          // the id of the agent the agent pane shows, or ""
	offset  int             // the rows scrolled past
	opened  bool            // the project came once, and its first running agent became active
	created string          // the id of an agent created here, active once the stream brings it
	closed  map[string]bool // the ids of the parents folded; what is open inside them stays so
}

// agentListRow is a row of the Agents list.
type agentListRow struct {
	agent api.Agent
	depth int
	guide string // the tree's lines before the fold mark
	fold  string // foldOpen, foldClosed or, in a tree, blank at the top level
	below int    // the agents under it
}

// list returns the Agents list of the project shown, nil until it is known.
func (m *Model) list() *agentList {
	f := m.following
	if f == nil || f.project == nil {
		return nil
	}
	l, ok := m.agentLists[f.project.Id]
	if !ok {
		l = &agentList{closed: map[string]bool{}}
		m.agentLists[f.project.Id] = l
	}
	return l
}

// topAgents returns the agents of the project shown with no parent, by name.
func (m *Model) topAgents() []api.Agent {
	if f := m.following; f != nil && f.project != nil && f.project.Agents != nil {
		return *f.project.Agents
	}
	return nil
}

// agents returns every agent of the project shown, each before its children.
func (m *Model) agents() []api.Agent {
	return api.FlattenAgents(m.topAgents())
}

func childrenOf(a api.Agent) []api.Agent {
	if a.Children == nil {
		return nil
	}
	return *a.Children
}

// listRows returns the rows the Agents list shows: those under a folded parent
// are left out. The fold column is there only when something has children, so
// that a flat list keeps the room.
func (m *Model) listRows() []agentListRow {
	l, top := m.list(), m.topAgents()
	if l == nil {
		return nil
	}
	tree := slices.ContainsFunc(top, func(a api.Agent) bool { return len(childrenOf(a)) > 0 })
	var rows []agentListRow
	var walk func(list []api.Agent, lead string, depth int)
	walk = func(list []api.Agent, lead string, depth int) {
		for i, a := range list {
			r := agentListRow{agent: a, depth: depth}
			children := childrenOf(a)
			next := "  "
			if depth > 0 {
				r.guide, next = lead+"├ ", lead+"│   "
				if i == len(list)-1 {
					r.guide, next = lead+"└ ", lead+"    "
				}
			}
			switch {
			case len(children) > 0 && l.closed[a.Id]:
				r.fold, r.below = foldClosed, len(api.FlattenAgents(children))
			case len(children) > 0:
				r.fold = foldOpen
			case depth == 0 && tree:
				r.fold = " "
			}
			rows = append(rows, r)
			if r.fold == foldOpen {
				walk(children, next, depth+1)
			}
		}
	}
	walk(top, "", 0)
	return rows
}

func agentIndex(agents []api.Agent, id string) int {
	return slices.IndexFunc(agents, func(a api.Agent) bool { return a.Id == id })
}

func rowIndex(rows []agentListRow, id string) int {
	return slices.IndexFunc(rows, func(r agentListRow) bool { return r.agent.Id == id })
}

// shownAs returns the row of the agent with id or, while it is under a folded
// parent, of its nearest parent shown; -1 when it is gone.
func shownAs(rows []agentListRow, agents []api.Agent, id string) int {
	for id != "" {
		if i := rowIndex(rows, id); i >= 0 {
			return i
		}
		i := agentIndex(agents, id)
		if i < 0 || agents[i].ParentAgentId == nil {
			return -1
		}
		id = *agents[i].ParentAgentId
	}
	return -1
}

// reveal opens the parents of the agent with id.
func (l *agentList) reveal(agents []api.Agent, id string) {
	for {
		i := agentIndex(agents, id)
		if i < 0 || agents[i].ParentAgentId == nil {
			return
		}
		id = *agents[i].ParentAgentId
		delete(l.closed, id)
	}
}

// agentsChanged keeps the cursor on its agent, or on the one that took its
// row; an active agent that is gone leaves the agent pane empty. The first
// time a project comes, its first running agent becomes active.
func (m *Model) agentsChanged() {
	l, agents := m.list(), m.agents()
	if i := agentIndex(agents, l.created); i >= 0 {
		l.active, l.cursor = l.created, l.created
		l.reveal(agents, l.created)
		l.created = ""
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
	rows := m.listRows()
	if i := shownAs(rows, agents, l.cursor); i >= 0 {
		l.row, l.cursor = i, rows[i].agent.Id
		return
	}
	l.row = max(min(l.row, len(rows)-1), 0)
	l.cursor = ""
	if len(rows) > 0 {
		l.cursor = rows[l.row].agent.Id
	}
}

// cursorAgent returns the agent under the cursor.
func (m *Model) cursorAgent() (api.Agent, bool) {
	l, rows := m.list(), m.listRows()
	if l == nil || l.row >= len(rows) {
		return api.Agent{}, false
	}
	return rows[l.row].agent, true
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
	l, rows := m.list(), m.listRows()
	if l == nil || i < 0 || i >= len(rows) {
		return
	}
	l.row, l.cursor = i, rows[i].agent.Id
}

// activate makes the agent under the cursor active.
func (m *Model) activate() {
	if a, ok := m.cursorAgent(); ok {
		m.list().active = a.Id
		m.log.Info("agent active", "name", a.Name, "reason", "chosen")
	}
}

// foldLeft closes the open parent under the cursor, and from any other row
// moves the cursor to its parent: it never closes a parent from below.
func (m *Model) foldLeft() {
	l, rows := m.list(), m.listRows()
	if l.row >= len(rows) {
		return
	}
	r := rows[l.row]
	if r.fold == foldOpen {
		l.closed[r.agent.Id] = true
		return
	}
	if r.agent.ParentAgentId != nil {
		m.moveCursor(rowIndex(rows, *r.agent.ParentAgentId))
	}
}

// foldRight opens the closed parent under the cursor.
func (m *Model) foldRight() {
	l, rows := m.list(), m.listRows()
	if l.row < len(rows) && rows[l.row].fold == foldClosed {
		delete(l.closed, rows[l.row].agent.Id)
	}
}

// toggleFold folds or unfolds the row i, from a click on its mark. The cursor
// comes out of what it closes.
func (m *Model) toggleFold(i int) {
	l, rows := m.list(), m.listRows()
	id := rows[i].agent.Id
	if rows[i].fold == foldClosed {
		delete(l.closed, id)
	} else {
		l.closed[id] = true
	}
	rows = m.listRows()
	l.row = max(shownAs(rows, m.agents(), l.cursor), 0)
	l.cursor = rows[l.row].agent.Id
}

// terminalOf is a's terminal, as the panes show one.
func terminalOf(a api.Agent) api.Terminal {
	return api.Terminal{
		Id: a.TerminalId, Name: a.Name, Dir: a.Dir, State: api.TerminalState(a.State),
		ExitCode: a.ExitCode, Error: a.Error,
	}
}

// agentCreated is the answer to C-q a or C-q c.
type agentCreated struct {
	projectID string
	agent     api.Agent
	err       error
}

// createAgent creates an agent in the project shown and focuses the agent
// pane; it becomes active once the answer comes.
func (m *Model) createAgent() tea.Cmd {
	dir, _ := m.checkoutDir()
	return m.sendCreateAgent(api.CreateAgent{Dir: dir})
}

// childParent returns the agent C-q c would create a child of: the one in
// focus, as for C-q x. A tombstone cannot have new children.
func (m *Model) childParent() (api.Agent, bool) {
	a, ok := m.focusedAgent()
	return a, ok && a.State != api.AgentStateRemoved
}

// createChild creates a child of parent, as createAgent does an agent.
func (m *Model) createChild(parent api.Agent) tea.Cmd {
	ref := "id:" + parent.Id
	return m.sendCreateAgent(api.CreateAgent{Parent: &ref, Dir: parent.Dir})
}

func (m *Model) sendCreateAgent(body api.CreateAgent) tea.Cmd {
	p := m.following.project
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("agent pane")
	m.log.Info("agent requested", "project", p.Name, "dir", body.Dir, "parent", body.Parent)
	ref, id := "id:"+p.Id, p.Id
	body.Project = &ref
	return func() tea.Msg {
		r, err := m.client.CreateAgent(m.ctx, body)
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
	top := s.m.topAgents()
	switch {
	case len(top) == 0:
		return defaultHint
	case slices.ContainsFunc(top, func(a api.Agent) bool { return len(childrenOf(a)) > 0 }):
		return treeHint
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
	case name == "left" || name == "h":
		s.m.foldLeft()
	case name == "right" || name == "l":
		s.m.foldRight()
	case name == "enter":
		s.m.activate()
	}
	return nil
}

// click takes a click at the column and row of the pane's area: on a row's fold
// mark it folds or unfolds, elsewhere on an agent's row it moves the cursor
// there and makes the agent active.
func (s *projectScreen) click(col, row, width int) {
	l, rows := s.m.list(), s.m.listRows()
	if l == nil || row < 1 || l.offset+row-1 >= len(rows) {
		return
	}
	i := l.offset + row - 1
	if r := rows[i]; r.fold == foldOpen || r.fold == foldClosed {
		guide, _ := rowGuide(r, width)
		if col == ansi.StringWidth("▸ ")+ansi.StringWidth(guide) {
			s.m.toggleFold(i)
			return
		}
	}
	s.m.moveCursor(i)
	s.m.activate()
}

func (s *projectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	m := s.m
	l, agents, rows := m.list(), m.agents(), m.listRows()
	width := area.Dx()
	live := len(agents) - countRemoved(agents)
	count := fmt.Sprint(live) + " "
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
		l.offset = max(min(l.offset, len(rows)-body), 0)
		// A parent folded over the active agent carries its mark.
		activeRow := shownAs(rows, agents, l.active)
		for i := l.offset; i < min(l.offset+body, len(rows)); i++ {
			cursor := i == l.row && m.layout.focus == project
			lines = append(lines, agentRow(st, rows[i], cursor, i == activeRow, width))
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

func countRemoved(agents []api.Agent) int {
	n := 0
	for _, a := range agents {
		if a.State == api.AgentStateRemoved {
			n++
		}
	}
	return n
}

// rowGuide returns the guide drawn on r in a pane width wide, and the cells
// it leaves for the mark, the name and the end. When the lines would leave a
// name too little room they give way to its level, as ⋯3.
func rowGuide(r agentListRow, width int) (guide string, room int) {
	base := width - ansi.StringWidth("▸ ● ")
	if r.fold != "" {
		base -= 2
	}
	guide = r.guide
	if r.depth > 0 && base-ansi.StringWidth(guide)-1 < minAgentName {
		connector := []rune(guide)
		guide = fmt.Sprintf("⋯%d %s", r.depth, string(connector[len(connector)-2:]))
	}
	return guide, base - ansi.StringWidth(guide)
}

// agentRow is r's row: the cursor when it is there, the tree's lines and fold
// mark, its state mark, its name, and at the row's end what its state does not
// tell and, when active, a mark that shows with color off too.
func agentRow(st style.Style, r agentListRow, cursor, active bool, width int) string {
	a := r.agent
	pointer, end, name := "  ", " ", a.Name
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if active {
		end = " " + st.Focused("◂") + " "
	}
	guide, room := rowGuide(r, width)
	room -= style.Width(end)
	detail := agentDetail(r)
	if room-len(detail)-1 < minAgentName {
		detail = ""
	}
	if detail != "" {
		room -= len(detail) + 1
	}
	name = ansi.Truncate(name, max(room, 0), "…")
	gap := strings.Repeat(" ", max(room-ansi.StringWidth(name), 0))
	switch {
	case cursor || active:
		name = st.Name(name)
	case a.State == api.AgentStateRemoved:
		name = st.Muted(name)
	}
	if detail != "" {
		detail = " " + st.Muted(detail)
	}
	if r.fold != "" {
		guide += r.fold + " "
	}
	row := pointer + st.Muted(guide) + agentMark(st, a) + " " + name + gap + detail + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// agentMark is a's state mark: the terminal tabs' or, for a tombstone, its own.
func agentMark(st style.Style, a api.Agent) string {
	if a.State == api.AgentStateRemoved {
		return st.Muted("†")
	}
	return stateMark(st, api.TerminalState(a.State))
}

// agentDetail is what the row says beside its mark: how many agents a folded
// parent hides, or what its state does not tell; running is the mark alone.
func agentDetail(r agentListRow) string {
	a := r.agent
	switch {
	case r.below > 0:
		return fmt.Sprintf("%d below", r.below)
	case a.State == api.AgentStateRunning:
		return ""
	case a.State == api.AgentStateRemoved:
		return string(a.State)
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
