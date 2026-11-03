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
	selectHint = "↑↓ select  Enter show  i info  / filter  Alt+arrows move  C-q menu"
	treeHint   = "↑↓ select  ←→ fold  Enter show  i info  / filter  Alt+arrows move  C-q menu"
)

// The fold marks of a parent's row. ▸ is taken by the cursor.
const (
	foldOpen   = "−"
	foldClosed = "+"
)

// agentList is what tide remembers of a project's Agents list for the run.
type agentList struct {
	cursor  string   // the id of the agent under the cursor
	row     int      // its row, which the next agent takes when it goes
	active  string   // the current tab, whose agent the list marks; "" only with no tabs
	tabs    []string // the tabs open, in their order: an agent's id, or infoTab of it
	tabFrom int      // the first tab the strip shows
	// tabsRead is set once the project's stream brought the tabs the daemon kept;
	// tide sends none before, or it would put its empty list in their place.
	tabsRead   bool
	savedTabs  []string // the tabs as last sent or brought by the stream
	savingTabs bool     // a list is on its way to the daemon
	// tabsRevision is the revision of the tabs as tide last knew them: an event
	// before it was sent before tide's last change reached the daemon.
	tabsRevision int64
	sendingTabs  []string      // the list on its way
	tabsInFlight chan struct{} // closed once it has been answered
	viewRead     bool          // the daemon's view of the project was put in place, or there was none
	// missingTabs are the tabs whose file or worktree the last event said is gone,
	// removed outside wdx: each stays, marked, until it is closed.
	missingTabs map[string]bool
	offset      int    // the rows scrolled past
	opened      bool   // the project came once, and its first running agent became active
	created     string // the tab of an agent created here, current once the stream brings it
	// createdKeepsCursor says created is the tab the project's view kept as current,
	// whose cursor the view put elsewhere: the cursor stays there.
	createdKeepsCursor bool
	closed             map[string]bool // the ids of the parents folded; what is open inside them stays so

	section       section              // the section the cursor is in
	worktreeRow   int                  // its row in the Worktrees section
	worktreeKey   string               // what that row is, which the cursor stays on as rows come and go
	worktreesOpen map[string]bool      // the names of the worktrees unfolded: they start folded, as they can have many files
	landAsked     map[string]string    // the landing this tide started, by worktree, until it ends
	diffs         map[string]*diffView // what the diffs' tabs hold, by view: a worktree's tab has two
	showCommits   map[string]bool      // the worktrees whose tab Tab turned to the commits, with changes still not committed
	lastAsked     map[string]string    // the agent last sent to from each worktree, which is offered first

	code        codeSection
	docRow      int                  // the cursor's row in the Docs section
	docCursor   string               // the id of its doc, which the cursor stays on as docs come and go
	docNames    map[string]string    // the docs' names by id at the last event, to say which went
	planRow     int                  // the cursor's row in the Plans section
	planCursor  string               // the id of its plan, which the cursor stays on as plans come and go
	planNames   map[string]string    // the plans' names by id at the last event, to say which went
	plansFolded map[string]bool      // the ids of the plans of plans folded
	planViews   map[string]*planView // what the plans' tabs hold, by tab
	lastActive  string               // the tab current when last looked, to tell one coming to show
	// editorsSeen is set once the project came and its editors got their tabs.
	editorsSeen bool
	closing     map[string]bool // the editors' tabs told to quit, which close once they end
	filter      listFilter
}

// agentListRow is a row of the Agents list.
type agentListRow struct {
	agent api.Agent
	depth int
	guide string // the tree's lines before the fold mark
	fold  string // foldOpen, foldClosed or, in a tree, blank at the top level
	below int    // the agents under it
	// waiting is how many of them are waiting, which a folded parent must not hide.
	waiting int
	match   bool // it matches the filter
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
// are left out, and with a filter those that neither match nor hold a match, the
// rest open. The fold column is there only when something has children, so that a
// flat list keeps the room.
func (m *Model) listRows() []agentListRow {
	l, top := m.list(), m.topAgents()
	if l == nil {
		return nil
	}
	tree := slices.ContainsFunc(top, func(a api.Agent) bool { return len(childrenOf(a)) > 0 })
	words := l.filter.words()
	var kept map[string]bool
	if words != nil {
		kept = keptAgents(top, words)
	}
	shown := func(list []api.Agent) []api.Agent {
		if kept == nil {
			return list
		}
		return slices.DeleteFunc(slices.Clone(list), func(a api.Agent) bool { return !kept[a.Id] })
	}
	var rows []agentListRow
	var walk func(list []api.Agent, lead string, depth int)
	walk = func(list []api.Agent, lead string, depth int) {
		for i, a := range list {
			r := agentListRow{agent: a, depth: depth, match: words != nil && matches(a.Name, words)}
			children := shown(childrenOf(a))
			var next string
			r.guide, next = treeGuides(lead, depth, i == len(list)-1)
			switch {
			case len(children) > 0 && kept == nil && l.closed[a.Id]:
				below := api.FlattenAgents(children)
				r.fold, r.below = foldClosed, len(below)
				r.waiting = waitingAgents(below)
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
	walk(shown(top), "", 0)
	return rows
}

// treeGuides returns the lines of a tree's row at depth, under the lines lead, and
// the lead of its children.
func treeGuides(lead string, depth int, last bool) (guide, next string) {
	switch {
	case depth == 0:
		return "", "  "
	case last:
		return lead + "└ ", lead + "    "
	}
	return lead + "├ ", lead + "│   "
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
// row. The first time a project comes with no tabs kept, its first running
// agent gets a tab.
func (m *Model) agentsChanged() {
	l, agents := m.list(), m.agents()
	if l.created == settingsTab || isTextTab(l.created) {
		l.openTab(l.created)
		l.created, l.createdKeepsCursor = "", false
	}
	if i := agentIndex(agents, tabAgent(l.created)); i >= 0 {
		l.openTab(l.created)
		if !l.createdKeepsCursor {
			l.cursor = agents[i].Id
			l.reveal(agents, agents[i].Id)
		}
		l.created, l.createdKeepsCursor = "", false
	}
	if !l.opened {
		l.opened = true
		if i := slices.IndexFunc(agents, func(a api.Agent) bool { return a.State == api.AgentStateRunning }); i >= 0 {
			l.openTab(agents[i].Id)
			l.cursor = agents[i].Id
			m.log.Info("agent active", "name", agents[i].Name, "reason", "first running")
		}
	}
	m.keepAgentCursor()
}

// keepAgentCursor keeps the cursor on its agent, or its nearest parent shown, or
// on the row it was on.
func (m *Model) keepAgentCursor() {
	l, agents, rows := m.list(), m.agents(), m.listRows()
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
	if l == nil || l.section != sectionAgents || l.row >= len(rows) {
		return api.Agent{}, false
	}
	return rows[l.row].agent, true
}

// activeAgent returns the agent of the current tab.
func (m *Model) activeAgent() (api.Agent, bool) {
	l, agents := m.list(), m.agents()
	if l == nil {
		return api.Agent{}, false
	}
	if i := agentIndex(agents, tabAgent(l.active)); i >= 0 {
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
	l.row, l.cursor, l.section = i, rows[i].agent.Id, sectionAgents
}

// activate opens the tab of the agent under the cursor, or goes to it, the focus
// following as showOpened says.
func (m *Model) activate(goToTab bool) {
	if a, ok := m.cursorAgent(); ok {
		m.list().openTab(a.Id)
		m.showOpened(goToTab)
		m.log.Info("agent active", "name", a.Name, "reason", "chosen")
	}
}

// showOpened moves the focus to the tab just opened in the agent pane when
// goToTab is set, or when the list is zoomed over it, where it could not be seen;
// otherwise it stays in the list, to go on through it (docs/TIDE_ENTER.md).
func (m *Model) showOpened(goToTab bool) {
	if !goToTab && m.layout.zoomed == none {
		return
	}
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
}

// goEnter is the key that opens a row's tab and goes to it, where the terminal
// tells it from Enter; o does the same everywhere.
const goEnter = "shift+enter"

func isEnter(name string) bool {
	return name == "enter" || name == goEnter
}

// rowOpener is what opens the tab of the left pane's row under the cursor, going to
// it or not, when the row has one: a Code directory only folds.
func (m *Model) rowOpener() (func(goToTab bool) tea.Cmd, bool) {
	l := m.list()
	if m.layout.focus != project || l == nil {
		return nil, false
	}
	do := func(open func(bool)) func(bool) tea.Cmd {
		return func(goToTab bool) tea.Cmd { open(goToTab); return nil }
	}
	switch l.section {
	case sectionAgents:
		_, ok := m.cursorAgent()
		return do(m.activate), ok
	case sectionWorktrees:
		return m.openWorktreeRow, l.worktreeRow < len(m.worktreeRows())
	case sectionCode:
		r, ok := m.codeCursorRow()
		if !ok || r.node == nil || r.node.dir {
			return nil, false
		}
		return do(func(goToTab bool) { m.openCodeFile(r.node.path, goToTab) }), true
	case sectionDocs:
		return do(m.openDoc), l.docRow < len(m.docRows())
	case sectionPlans:
		_, ok := m.cursorPlan()
		return do(m.planEnter), ok
	}
	return nil, false
}

// rowTab is the tab Enter on the row under the cursor in the left pane opens.
func (m *Model) rowTab() (string, bool) {
	l := m.list()
	if m.layout.focus != project || l == nil {
		return "", false
	}
	switch l.section {
	case sectionAgents:
		if a, ok := m.cursorAgent(); ok {
			return a.Id, true
		}
	case sectionWorktrees:
		if rows := m.worktreeRows(); l.worktreeRow < len(rows) {
			return rows[l.worktreeRow].tab(), true
		}
	case sectionCode:
		if r, ok := m.codeCursorRow(); ok && r.node != nil && !r.node.dir {
			return fileTab(l.code.worktree, r.node.path), true
		}
	case sectionDocs:
		if rows := m.docRows(); l.docRow < len(rows) {
			return docTab(rows[l.docRow].doc.Id), true
		}
	case sectionPlans:
		if p, ok := m.cursorPlan(); ok {
			return planTab(p.Id), true
		}
	}
	return "", false
}

// openRowTab is the row's tab, when it is open.
func (m *Model) openRowTab() (string, bool) {
	tab, ok := m.rowTab()
	return tab, ok && slices.Contains(m.list().tabs, tab)
}

// foldLeft closes the open parent under the cursor, and from any other row
// moves the cursor to its parent: it never closes a parent from below.
func (m *Model) foldLeft() {
	l, rows := m.list(), m.listRows()
	if l.row >= len(rows) {
		return
	}
	r := rows[l.row]
	if r.fold == foldOpen && !m.filtering() {
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
	if l.row < len(rows) && rows[l.row].fold == foldClosed && !m.filtering() {
		delete(l.closed, rows[l.row].agent.Id)
	}
}

// toggleFold folds or unfolds the row i, from a click on its mark. The cursor
// comes out of what it closes.
func (m *Model) toggleFold(i int) {
	l, rows := m.list(), m.listRows()
	if m.filtering() {
		return // what matches stays open
	}
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

// childBox asks C-q c for the worktree of a new child: empty, it is the
// parent's.
type childBox struct {
	parent   api.Agent
	worktree string
}

// childKey takes a key while the box is open.
func (m *Model) childKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.child
	switch name := msg.String(); {
	case name == "esc":
		m.child = nil
	case name == "enter":
		m.child = nil
		return m.createChild(c.parent, strings.TrimSpace(c.worktree))
	case name == "backspace" && c.worktree != "":
		r := []rune(c.worktree)
		c.worktree = string(r[:len(r)-1])
	case typed(msg):
		c.worktree += msg.Text
	}
	return nil
}

// childPopup is what the box shows.
func (m *Model) childPopup() popup {
	c, st := m.child, m.st
	in := "its worktree"
	if c.parent.Worktree != nil {
		in = *c.parent.Worktree
	}
	return popup{
		title: "New child of " + c.parent.Name,
		body: []string{
			"Worktree, created if it is new:", "",
			"> " + c.worktree + "_", "",
			st.Muted("Empty: the parent's, " + in),
		},
		footer: st.Command("Enter") + " create  " + st.Command("Esc") + " cancel",
	}
}

// createChild creates a child of parent in worktree, or in parent's dir when
// it is "", as createAgent does an agent.
func (m *Model) createChild(parent api.Agent, worktree string) tea.Cmd {
	ref := "id:" + parent.Id
	body := api.CreateAgent{Parent: &ref, Dir: parent.Dir}
	if worktree != "" {
		body.Worktree = &worktree
	}
	return m.sendCreateAgent(body)
}

func (m *Model) sendCreateAgent(body api.CreateAgent) tea.Cmd {
	p := m.following.project
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("agent pane")
	m.log.Info("agent requested", "project", p.Name, "dir", body.Dir, "parent", body.Parent, "worktree", body.Worktree)
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
	l.created, l.createdKeepsCursor = msg.agent.Id, false
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
	l := s.m.list()
	switch {
	case l != nil && l.filter.typing:
		return filterTypingHint
	case l != nil && l.filter.on():
		return filterOnHint + s.sectionHint()
	}
	return s.sectionHint()
}

func (s *projectScreen) sectionHint() string {
	top := s.m.topAgents()
	if l := s.m.list(); l != nil {
		switch {
		case l.section == sectionWorktrees:
			return worktreeHint
		case l.section == sectionCode && l.code.row == 0:
			return codeSelectorHint
		case l.section == sectionCode:
			return codeHint
		case l.section == sectionDocs:
			return docHint
		case l.section == sectionPlans:
			return s.m.planHintFor()
		}
	}
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
	name := key.String()
	switch {
	case name == "esc" && l.filter.on():
		s.m.clearFilter()
	case slices.Contains(upKeys, name):
		s.m.moveUp()
	case slices.Contains(downKeys, name):
		s.m.moveDown()
	case l.section == sectionWorktrees:
		return s.updateWorktrees(name)
	case l.section == sectionCode:
		return s.updateCode(name)
	case l.section == sectionDocs && isEnter(name):
		s.m.openDoc(name == goEnter)
	case l.section == sectionPlans && isEnter(name):
		s.m.planEnter(name == goEnter)
	case l.section == sectionPlans && (name == "left" || name == "h"):
		s.m.planFold(true)
	case l.section == sectionPlans && (name == "right" || name == "l"):
		s.m.planFold(false)
	case l.section == sectionAgents:
		s.updateAgents(name)
	}
	return nil
}

func (s *projectScreen) updateAgents(name string) {
	switch name {
	case "left", "h":
		s.m.foldLeft()
	case "right", "l":
		s.m.foldRight()
	case "enter", goEnter:
		s.m.activate(name == goEnter)
	}
}

func (s *projectScreen) updateWorktrees(name string) tea.Cmd {
	switch name {
	case "left", "h":
		s.m.worktreeFoldLeft()
	case "right", "l":
		s.m.worktreeFoldRight()
	case "enter", goEnter:
		return s.m.openWorktreeRow(name == goEnter)
	}
	return nil
}

// section is a part of the left pane's list.
type section int

const (
	sectionAgents section = iota
	sectionWorktrees
	sectionCode
	sectionDocs
	sectionPlans
)

// sectionLetters name the sections in the tide.section_order setting.
var sectionLetters = map[rune]section{'a': sectionAgents, 'w': sectionWorktrees, 'd': sectionDocs, 'p': sectionPlans, 'c': sectionCode}

// defaultSectionOrder is the setting's default, which tide.section_order in
// internal/config repeats.
const defaultSectionOrder = "awdpc"

// sectionSettingKey is the setting that orders the sections.
const sectionSettingKey = "tide.section_order"

// sectionOrder returns the sections the setting's letters name, in their order:
// one left out is hidden. A letter it does not know, which the setting refuses, is
// passed over, and letters naming none of the sections give the default order, so
// that the pane is never empty.
func sectionOrder(letters string) []section {
	var order []section
	for _, r := range letters {
		if s, ok := sectionLetters[r]; ok && !slices.Contains(order, s) {
			order = append(order, s)
		}
	}
	if len(order) == 0 && letters != defaultSectionOrder {
		return sectionOrder(defaultSectionOrder)
	}
	return order
}

// letter is the section's letter in the setting.
func (s section) letter() string {
	for r, named := range sectionLetters {
		if named == s {
			return string(r)
		}
	}
	return ""
}

// cursorRow is the cursor's row in its section.
func (l *agentList) cursorRow() int {
	switch l.section {
	case sectionWorktrees:
		return l.worktreeRow
	case sectionCode:
		return l.code.row
	case sectionDocs:
		return l.docRow
	case sectionPlans:
		return l.planRow
	}
	return l.row
}

// paneItem is a line of the one list the sections make, below the heading kept at
// the pane's top.
type paneItem struct {
	section section
	row     int // its row in the section, when it is neither of the next two
	heading bool
	empty   bool // it stands for the rows the section has none of
}

// paneItems are the lines of the list: each section in the order set, with its
// heading but the first's, which the pane's top keeps, and its rows or what stands for
// them. The agents' rows, the worktrees and their files, the Code selector and tree.
func (m *Model) paneItems() []paneItem {
	var items []paneItem
	for i, s := range m.sectionsInOrder() {
		if i > 0 {
			items = append(items, paneItem{section: s, heading: true})
		}
		var n int
		var empty bool
		switch s {
		case sectionAgents:
			n = len(m.listRows())
			empty = n == 0
		case sectionWorktrees:
			n = len(m.worktreeRows())
			empty = n == 0
		case sectionCode:
			n = len(m.codeRowsShown())
			empty = m.codeNote() != ""
		case sectionDocs:
			n = len(m.docRows())
			empty = n == 0
		case sectionPlans:
			n = len(m.planRows())
			empty = n == 0
		}
		for row := range n {
			items = append(items, paneItem{section: s, row: row})
		}
		if empty {
			items = append(items, paneItem{section: s, empty: true})
		}
	}
	return items
}

// sectionsInOrder returns the sections shown, in the order set.
func (m *Model) sectionsInOrder() []section {
	if len(m.sections) == 0 {
		return sectionOrder(defaultSectionOrder)
	}
	return m.sections
}

// keepCursorShown moves a cursor that is in a hidden section to the first row
// shown, or to the first section when none has rows.
func (m *Model) keepCursorShown() {
	l, sections := m.list(), m.sectionsInOrder()
	if l == nil || slices.Contains(sections, l.section) {
		return
	}
	hidden := l.section
	if i := slices.IndexFunc(m.paneItems(), func(it paneItem) bool { return !it.heading && !it.empty }); i >= 0 {
		m.moveToItem(m.paneItems()[i])
	} else {
		l.section = sections[0]
	}
	m.log.Debug("cursor left a hidden section", "hidden", hidden.letter(), "section", l.section.letter())
}

// moveBy moves the cursor by step rows up the one list the sections make, past
// headings and what stands for rows; at either end it stays.
func (m *Model) moveBy(step int) {
	l, items := m.list(), m.paneItems()
	i := slices.IndexFunc(items, func(it paneItem) bool {
		return it.section == l.section && it.row == l.cursorRow() && !it.heading && !it.empty
	})
	if i < 0 && step < 0 {
		return
	}
	for j := i + step; j >= 0 && j < len(items); j += step {
		if it := items[j]; !it.heading && !it.empty {
			m.moveToItem(it)
			return
		}
	}
}

// moveToItem puts the cursor on the row it is.
func (m *Model) moveToItem(it paneItem) {
	switch it.section {
	case sectionAgents:
		m.moveCursor(it.row)
	case sectionWorktrees:
		m.moveToWorktree(it.row)
	case sectionCode:
		m.moveToCode(it.row)
	case sectionDocs:
		m.moveToDoc(it.row)
	case sectionPlans:
		m.moveToPlan(it.row)
	}
}

// setSectionOrder orders the sections by the setting's value, as saved in the
// settings tab.
func (m *Model) setSectionOrder(letters string) {
	m.sections = sectionOrder(letters)
	m.log.Info("sections ordered", "order", letters)
}

// visibleItems scrolls the list so the cursor stays in view in a pane of height
// rows, and returns what shows below the heading at its top, and the section first
// in view, whose heading that is.
func (m *Model) visibleItems(height int) (shown []paneItem, top section) {
	l, items := m.list(), m.paneItems()
	body := max(height-1, 1)
	cursor := slices.IndexFunc(items, func(it paneItem) bool {
		return it.section == l.section && it.row == l.cursorRow() && !it.heading && !it.empty
	})
	cursor = max(cursor, 0)
	l.offset = max(min(l.offset, cursor), cursor-body+1)
	l.offset = max(min(l.offset, len(items)-body), 0)
	first := l.offset
	top = items[first].section
	if items[first].heading {
		first++ // the heading at the top says it
	}
	return items[first:min(first+body, len(items))], top
}

// click takes a click at the column and row of the pane's area, of the given size:
// on a row's fold mark it folds or unfolds, elsewhere on a row it moves the cursor
// there and shows what the row is.
func (s *projectScreen) click(col, row, width, height int) tea.Cmd {
	l := s.m.list()
	if l == nil || row < 1 {
		return nil
	}
	if l.filter.on() && row == height-1 {
		s.m.openFilter()
		return nil
	}
	shown, _ := s.m.visibleItems(s.m.listHeight(height))
	if row-1 >= len(shown) {
		return nil
	}
	it := shown[row-1]
	switch {
	case it.heading || it.empty:
		return nil
	case it.section == sectionWorktrees:
		return s.clickWorktree(it.row, col)
	case it.section == sectionCode:
		s.clickCode(it.row, col, width)
		return nil
	case it.section == sectionDocs:
		s.m.moveToDoc(it.row)
		s.m.openDoc(false)
		return nil
	case it.section == sectionPlans:
		s.m.clickPlan(it.row, col)
		return nil
	}
	rows := s.m.listRows()
	i := it.row
	if r := rows[i]; r.fold == foldOpen || r.fold == foldClosed {
		guide, _ := rowGuide(r, width)
		if col == ansi.StringWidth("▸ ")+ansi.StringWidth(guide) {
			s.m.toggleFold(i)
			return nil
		}
	}
	s.m.moveCursor(i)
	s.m.activate(false)
	return nil
}

// clickWorktree takes a click on the row i of the Worktrees section.
func (s *projectScreen) clickWorktree(i, col int) tea.Cmd {
	rows := s.m.worktreeRows()
	r := rows[i]
	if r.file == nil && len(r.worktree.Files) > 0 && col == ansi.StringWidth("▸ ") {
		s.m.foldWorktree(r.worktree.Name, s.m.list().worktreesOpen[r.worktree.Name])
		s.m.worktreesChanged()
		return nil
	}
	s.m.moveToWorktree(i)
	return s.m.openWorktreeRow(false)
}

func (s *projectScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	m := s.m
	l, width := m.list(), area.Dx()
	if l == nil {
		lines := []string{sectionHeading(st, " Agents", "", width), st.Muted(" loading…")}
		uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
		return
	}
	agents, rows, worktrees, code, marks, docs, plans := m.agents(), m.listRows(), m.worktreeRows(), m.codeRowsShown(), m.codeMarks(), m.docRows(), m.planRows()
	agentCount := fmt.Sprint(len(agents)-countRemoved(agents)) + " "
	worktreeCount := ""
	if m.worktreeStatuses() != nil {
		worktreeCount = fmt.Sprint(len(m.worktreeStatuses())) + " "
	}
	headings := map[section]string{
		sectionAgents:    sectionHeading(st, " Agents", agentCount, width),
		sectionWorktrees: sectionHeading(st, " Worktrees", worktreeCount, width),
		sectionCode:      sectionHeading(st, " Code", "", width),
		sectionDocs:      sectionHeading(st, " Docs", fmt.Sprint(len(m.docs()))+" ", width),
		sectionPlans:     sectionHeading(st, " Plans", fmt.Sprint(len(m.plans()))+" ", width),
	}

	shown, top := m.visibleItems(m.listHeight(area.Dy()))
	lines := []string{headings[top]}
	// A parent folded over the active agent carries its mark.
	activeRow := shownAs(rows, agents, tabAgent(l.active))
	for _, it := range shown {
		cursor := it.section == l.section && it.row == l.cursorRow() && m.layout.focus == project
		switch {
		case it.heading:
			lines = append(lines, headings[it.section])
		case it.empty && it.section == sectionCode:
			lines = append(lines, st.Muted(" "+m.codeNote()))
		case it.empty && m.filtering():
			lines = append(lines, st.Muted(" no match"))
		case it.empty && it.section == sectionAgents:
			lines = append(lines, st.Muted(" no agents: ")+st.Command("C-q a")+st.Muted(" creates one"))
		case it.empty && it.section == sectionDocs:
			lines = append(lines, st.Muted(" no docs: see ")+st.Command("wdx doc create"))
		case it.empty && it.section == sectionPlans:
			lines = append(lines, st.Muted(" no plans: see ")+st.Command("wdx help plans"))
		case it.empty && m.worktreeStatuses() == nil:
			lines = append(lines, st.Muted(" loading…"))
		case it.empty:
			lines = append(lines, st.Muted(" no worktrees"))
		case it.section == sectionAgents:
			lines = append(lines, agentRow(st, rows[it.row], cursor, it.row == activeRow, width))
		case it.section == sectionWorktrees:
			r := worktrees[it.row]
			lines = append(lines, m.worktreeLine(r, cursor, m.worktreeRowActive(r), width))
		case it.section == sectionCode:
			lines = append(lines, m.codeLine(code[it.row], marks, cursor, width))
		case it.section == sectionDocs:
			lines = append(lines, docLine(st, docs[it.row], cursor, m.docRowActive(docs[it.row]), width))
		case it.section == sectionPlans:
			r := plans[it.row]
			lines = append(lines, planLine(st, r, l.plansFolded[r.plan.Id], cursor, m.planRowActive(r), width))
		}
	}
	if l.filter.on() {
		lines = append(lines, slices.Repeat([]string{""}, max(m.listHeight(area.Dy())-len(lines), 0))...)
		lines = append(lines, m.filterLine(st, width))
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// listHeight is the rows the list has in a pane height rows high: the filter's
// line takes the last.
func (m *Model) listHeight(height int) int {
	if l := m.list(); l != nil && l.filter.on() {
		return max(height-1, 1)
	}
	return height
}

// sectionHeading is a section's band, with its count at the right end.
func sectionHeading(st style.Style, title, count string, width int) string {
	gap := max(width-len(title)-len(count), 0)
	return st.Section(ansi.Truncate(title+strings.Repeat(" ", gap)+count, width, ""))
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
	return fitGuide(r.guide, r.depth, base)
}

// fitGuide returns guide, or its level when the lines would leave too little of the
// room, and what is left of it.
func fitGuide(guide string, depth, room int) (fitted string, left int) {
	if depth > 0 && room-ansi.StringWidth(guide)-1 < minAgentName {
		connector := []rune(guide)
		guide = fmt.Sprintf("⋯%d %s", depth, string(connector[len(connector)-2:]))
	}
	return guide, room - ansi.StringWidth(guide)
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
		detail = " " + agentDetailStyle(st, r, detail)
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
	if agentWaiting(a) {
		return st.Warning("?")
	}
	return stateMark(st, api.TerminalState(a.State))
}

func agentWaiting(a api.Agent) bool {
	return a.Activity != nil && *a.Activity == api.AgentActivityWaiting
}

func waitingAgents(list []api.Agent) int {
	n := 0
	for _, a := range list {
		if agentWaiting(a) {
			n++
		}
	}
	return n
}

// agentDetailStyle is detail, what r says beside its mark, as what it says calls
// for: an agent waiting is what a human looks for.
func agentDetailStyle(st style.Style, r agentListRow, detail string) string {
	if r.waiting > 0 || (r.below == 0 && agentWaiting(r.agent)) {
		return st.Warning(detail)
	}
	return st.Muted(detail)
}

// agentDetail is what the row says beside its mark: how many agents a folded
// parent hides, or how many of them are waiting, or what its state does not tell:
// of a running agent, that it is working or waiting.
func agentDetail(r agentListRow) string {
	a := r.agent
	switch {
	case r.waiting > 0:
		return fmt.Sprintf("%d waiting", r.waiting)
	case r.below > 0:
		return fmt.Sprintf("%d below", r.below)
	case a.State == api.AgentStateRunning && a.Held > 0:
		return fmt.Sprintf("%d held", a.Held)
	case a.State == api.AgentStateRunning:
		if a.Activity != nil && (*a.Activity == api.AgentActivityWorking || *a.Activity == api.AgentActivityWaiting) {
			return string(*a.Activity)
		}
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
	case m.settingsShown():
		m.drawSettings(buf, area, st)
		return
	case m.editShown():
		e, _ := m.tabEditor(m.list().active)
		m.drawShown(buf, area, st, agent, editorTerminal(e, m.diffTitle(m.list().active)), nil)
		return
	case m.missingShown():
		lines = m.missingLines(st)
	case m.textShown():
		m.drawDiff(buf, area, st)
		return
	case m.planShown():
		m.values.draw(buf, area, m.planTabRows(), m.layout.focus == agent, st)
		return
	case ok && m.infoShown():
		m.drawInfo(buf, area, st, active)
		return
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
