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
	// headingHint is a collapsed section's, with the cursor on its heading.
	headingHint = "↑↓ select  z expand  / filter  Alt+arrows move  C-q menu"
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
	onHeading     bool                 // the cursor is on its section's heading, which only a collapsed one allows
	collapsed     map[string]bool      // the letters of the sections collapsed to their heading (docs/TIDE_COLLAPSE.md)
	worktreeRow   int                  // its row in the Worktrees section
	worktreeKey   string               // what that row is, which the cursor stays on as rows come and go
	worktreesOpen map[string]bool      // the names of the worktrees unfolded: they start folded, as they can have many files
	slotsFolded   map[string]bool      // the ids of the plans whose slots are folded
	landAsked     map[string]string    // the landing this tide started, by worktree, until it ends
	diffs         map[string]*diffView // what the diffs' tabs hold, by view: a worktree's tab has two
	showCommits   map[string]bool      // the worktrees whose tab Tab turned to the commits, with changes still not committed
	lastAsked     map[string]string    // the agent last sent to from each worktree, which is offered first

	code        codeSection
	docRow      int                  // the cursor's row in the Docs section
	docCursor   string               // the id of its doc, which the cursor stays on as docs come and go
	docNames    map[string]string    // the docs' names by id at the last event, to say which went
	docFolders  map[string]bool      // the folders of the Docs section folded, by path (docs/FOLDERS.md)
	planRow     int                  // the cursor's row in the Plans section
	planCursor  string               // the id of its plan, which the cursor stays on as plans come and go
	planNames   map[string]string    // the plans' names by id at the last event, to say which went
	plansFolded map[string]bool      // the ids of the plans of plans folded
	plansView   plansView            // the plans the section shows, as its selector chose
	planFolders map[string]bool      // the folders of the Plans section folded, by path
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
	plan  string // a plan's row: the plan's name, which its agents' task names; empty on an agent's
	depth int
	guide string // the tree's lines before the fold mark
	fold  string // foldOpen, foldClosed or, in a tree, blank at the top level
	below int    // the agents under it
	// waiting is how many of them are waiting, which a folded parent must not hide.
	waiting int
	match   bool // it matches the filter
}

// key names the row across events and in the folds: its agent's id, or its plan's
// name, which a plan that has run cannot change.
func (r agentListRow) key() string {
	if r.plan != "" {
		return planGroupKey(r.plan)
	}
	return r.agent.Id
}

// planGroupKey is the key of the row of the plan named name in the Agents section.
func planGroupKey(name string) string { return planTabPrefix + strings.ToLower(name) }

// groupOf is the key of the row a's row is under at the top: its plan's, when it
// does a plan's task, as an agent with no parent; empty for none.
func groupOf(a api.Agent) string {
	if a.ParentAgentId == nil && a.Task != nil {
		return planGroupKey(a.Task.Plan)
	}
	return ""
}

// agentEntry is what the top of the Agents section lists: an agent, or a plan with
// its agents, at the place of its first.
type agentEntry struct {
	agent  api.Agent
	plan   string
	agents []api.Agent
}

// agentEntries groups the agents in top that do a plan's task under their plan.
func agentEntries(top []api.Agent) []agentEntry {
	var entries []agentEntry
	at := map[string]int{}
	for _, a := range top {
		key := groupOf(a)
		if key == "" {
			entries = append(entries, agentEntry{agent: a})
			continue
		}
		i, ok := at[key]
		if !ok {
			i = len(entries)
			at[key] = i
			entries = append(entries, agentEntry{plan: a.Task.Plan})
		}
		entries[i].agents = append(entries[i].agents, a)
	}
	return entries
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
// rest open. The agents doing a plan's tasks are under a row for the plan
// (docs/PLANS.md, step 5). The fold column is there only when something has
// children, so that a flat list keeps the room.
func (m *Model) listRows() []agentListRow {
	l, top := m.list(), m.topAgents()
	if l == nil {
		return nil
	}
	entries := agentEntries(top)
	tree := slices.ContainsFunc(entries, func(e agentEntry) bool { return e.plan != "" || len(childrenOf(e.agent)) > 0 })
	words := l.filter.words()
	var kept map[string]bool
	if words != nil {
		kept = keptAgents(top, words)
		for _, e := range entries {
			if e.plan != "" && matches(e.plan, words) {
				keepAll(kept, e.agents) // a plan matching shows all its agents, as a parent does
			}
		}
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
	for _, e := range entries {
		if e.plan == "" {
			if kept == nil || kept[e.agent.Id] {
				walk([]api.Agent{e.agent}, "", 0)
			}
			continue
		}
		agents := shown(e.agents)
		if len(agents) == 0 {
			continue
		}
		r := agentListRow{plan: e.plan, fold: foldOpen, match: words != nil && matches(e.plan, words)}
		if kept == nil && l.closed[r.key()] {
			below := api.FlattenAgents(agents)
			r.fold, r.below, r.waiting = foldClosed, len(below), waitingAgents(below)
		}
		rows = append(rows, r)
		if r.fold == foldOpen {
			_, next := treeGuides("", 0, false)
			walk(agents, next, 1)
		}
	}
	return rows
}

// keepAll keeps agents and all under them.
func keepAll(kept map[string]bool, agents []api.Agent) {
	for _, a := range api.FlattenAgents(agents) {
		kept[a.Id] = true
	}
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
	return slices.IndexFunc(rows, func(r agentListRow) bool { return r.key() == id })
}

// shownAs returns the row of the agent with id or, while it is under a folded
// parent, of its nearest parent shown; -1 when it is gone.
func shownAs(rows []agentListRow, agents []api.Agent, id string) int {
	for id != "" {
		if i := rowIndex(rows, id); i >= 0 {
			return i
		}
		i := agentIndex(agents, id)
		if i < 0 {
			return -1
		}
		if group := groupOf(agents[i]); group != "" {
			return rowIndex(rows, group)
		}
		if agents[i].ParentAgentId == nil {
			return -1
		}
		id = *agents[i].ParentAgentId
	}
	return -1
}

// reveal opens the parents of the agent with id, and the plan it does a task of.
func (l *agentList) reveal(agents []api.Agent, id string) {
	for {
		i := agentIndex(agents, id)
		if i < 0 {
			return
		}
		if group := groupOf(agents[i]); group != "" {
			delete(l.closed, group)
		}
		if agents[i].ParentAgentId == nil {
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
		l.row, l.cursor = i, rows[i].key()
		return
	}
	l.row = max(min(l.row, len(rows)-1), 0)
	l.cursor = ""
	if len(rows) > 0 {
		l.cursor = rows[l.row].key()
	}
}

// cursorAgent returns the agent under the cursor.
func (m *Model) cursorAgent() (api.Agent, bool) {
	l, rows := m.list(), m.listRows()
	if l == nil || l.section != sectionAgents || l.row >= len(rows) || rows[l.row].plan != "" {
		return api.Agent{}, false
	}
	return rows[l.row].agent, true
}

// cursorPlanGroup returns the plan whose row in the Agents section is under the
// cursor.
func (m *Model) cursorPlanGroup() (api.Plan, bool) {
	l, rows := m.list(), m.listRows()
	if l == nil || l.section != sectionAgents || l.row >= len(rows) || rows[l.row].plan == "" {
		return api.Plan{}, false
	}
	return m.planNamed(rows[l.row].plan)
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
	l.row, l.cursor, l.section, l.onHeading = i, rows[i].key(), sectionAgents, false
}

// activate opens the tab of the agent under the cursor, or goes to it, the focus
// following as showOpened says; on a plan's row, the plan's tab.
func (m *Model) activate(goToTab bool) {
	if p, ok := m.cursorPlanGroup(); ok {
		m.openPlan(p, goToTab)
		return
	}
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
		r, ok := m.cursorDocRow()
		return do(m.openDoc), ok && r.place.folder == ""
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
		if r, ok := m.cursorDocRow(); ok && r.place.folder == "" {
			return docTab(r.doc.Id), true
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
		l.closed[r.key()] = true
		return
	}
	switch {
	case r.plan != "":
	case r.agent.ParentAgentId != nil:
		m.moveCursor(rowIndex(rows, *r.agent.ParentAgentId))
	case groupOf(r.agent) != "":
		m.moveCursor(rowIndex(rows, groupOf(r.agent)))
	}
}

// foldRight opens the closed parent under the cursor.
func (m *Model) foldRight() {
	l, rows := m.list(), m.listRows()
	if l.row < len(rows) && rows[l.row].fold == foldClosed && !m.filtering() {
		delete(l.closed, rows[l.row].key())
	}
}

// toggleFold folds or unfolds the row i, from a click on its mark. The cursor
// comes out of what it closes.
func (m *Model) toggleFold(i int) {
	l, rows := m.list(), m.listRows()
	if m.filtering() {
		return // what matches stays open
	}
	id := rows[i].key()
	if rows[i].fold == foldClosed {
		delete(l.closed, id)
	} else {
		l.closed[id] = true
	}
	rows = m.listRows()
	l.row = max(shownAs(rows, m.agents(), l.cursor), 0)
	l.cursor = rows[l.row].key()
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

// childParent returns the agent C-q c would create a child of: the one in
// focus, as for C-q x. A tombstone cannot have new children.
func (m *Model) childParent() (api.Agent, bool) {
	a, ok := m.focusedAgent()
	return a, ok && a.State != api.AgentStateRemoved
}

func (m *Model) sendCreateAgent(body api.CreateAgent) tea.Cmd {
	p := m.following.project
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("agent pane")
	m.log.Info("agent requested", "project", p.Name, "dir", body.Dir, "parent", body.Parent, "worktree", body.Worktree, "name", body.Name,
		"model", body.Model, "permissions", body.Permissions, "prompt", body.Prompt != nil)
	ref, id := "id:"+p.Id, p.Id
	body.Project = &ref
	return func() tea.Msg {
		r, err := m.client.CreateAgent(m.ctx, body)
		return agentCreated{projectID: id, agent: r.Agent, err: err}
	}
}

func (m *Model) agentCreated(msg agentCreated) {
	formWaited := m.agentForm != nil && m.agentForm.busy
	if msg.err != nil && formWaited {
		m.log.Info("agent not created", "err", msg.err)
		m.agentFormFailed(msg.err)
		return
	}
	if formWaited {
		m.agentForm = nil
	}
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
		case l.onHeading:
			return headingHint
		case l.section == sectionWorktrees && s.m.cursorOnSlots():
			return planGroupHint
		case l.section == sectionWorktrees:
			return worktreeHint
		case l.section == sectionCode && l.code.row == 0:
			return codeSelectorHint
		case l.section == sectionCode:
			return codeHint
		case l.section == sectionDocs:
			if r, ok := s.m.cursorDocRow(); ok && r.place.folder != "" {
				return folderHint
			}
			return docHint
		case l.section == sectionPlans:
			return s.m.planHintFor()
		}
		if _, ok := s.m.cursorPlanGroup(); ok {
			return planGroupHint
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
	case l.onHeading:
		// a collapsed heading is no row: only moving off it and z act there
	case l.section == sectionWorktrees:
		return s.updateWorktrees(name)
	case l.section == sectionCode:
		return s.updateCode(name)
	case l.section == sectionDocs && isEnter(name):
		s.m.openDoc(name == goEnter)
	case l.section == sectionDocs && (name == "left" || name == "h"):
		s.m.docFold(true)
	case l.section == sectionDocs && (name == "right" || name == "l"):
		s.m.docFold(false)
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

// title is the section's name, as its heading shows it.
func (s section) title() string {
	return map[section]string{sectionAgents: "Agents", sectionWorktrees: "Worktrees", sectionCode: "Code", sectionDocs: "Docs", sectionPlans: "Plans"}[s]
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
	// collapsed marks the heading of a section showing no rows as it is collapsed:
	// the cursor can stop there, and on no other heading.
	collapsed bool
}

// stop reports whether the cursor can be on the item.
func (it paneItem) stop() bool { return it.collapsed || !it.heading && !it.empty }

// isCursor reports whether the cursor is on the item.
func (l *agentList) isCursor(it paneItem) bool {
	if l.onHeading {
		return it.collapsed && it.section == l.section
	}
	return it.section == l.section && it.row == l.cursorRow() && !it.heading && !it.empty
}

// paneItems are the lines of the list: each section in the order set, with its
// heading but the first's, which the pane's top keeps, and its rows or what stands for
// them. The agents' rows, the worktrees and their files, the Code selector and tree.
func (m *Model) paneItems() []paneItem {
	var items []paneItem
	for i, s := range m.sectionsInOrder() {
		n, empty := m.sectionRows(s)
		collapsed := m.collapsedShown(s, n)
		if i > 0 || collapsed {
			items = append(items, paneItem{section: s, heading: true, collapsed: collapsed})
		}
		if collapsed {
			continue
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

// sectionRows returns how many rows the section has, and whether a line stands
// for the rows it has none of.
func (m *Model) sectionRows(s section) (n int, empty bool) {
	switch s {
	case sectionAgents:
		n = len(m.listRows())
		return n, n == 0
	case sectionWorktrees:
		n = len(m.worktreeRows())
		return n, n == 0
	case sectionCode:
		return len(m.codeRowsShown()), m.codeNote() != ""
	case sectionDocs:
		n = len(m.docRows())
		return n, n == 0
	case sectionPlans:
		n = len(m.planRows())
		return n, n <= 1 // the selector alone shows no plan
	}
	return 0, true
}

// collapsedShown reports whether the section, with its n rows, shows only its
// heading: it is collapsed, and the filter, which looks in collapsed sections too,
// matches none of its rows.
func (m *Model) collapsedShown(s section, n int) bool {
	l := m.list()
	if l == nil || !l.collapsed[s.letter()] {
		return false
	}
	matches := n
	if s == sectionCode {
		matches-- // the worktree's selector is no match
	}
	return !m.filtering() || matches <= 0
}

// sectionCollapsedShown is collapsedShown with the section's rows as they are.
func (m *Model) sectionCollapsedShown(s section) bool {
	n, _ := m.sectionRows(s)
	return m.collapsedShown(s, n)
}

// toggleSection collapses the section to its heading, or expands it. A cursor
// in a section collapsed goes to its heading; one on the heading of a section
// expanded goes to its first row, or the nearest row when it has none.
func (m *Model) toggleSection(s section, reason string) {
	l := m.list()
	if l == nil {
		return
	}
	if l.collapsed == nil {
		l.collapsed = map[string]bool{}
	}
	collapse := !l.collapsed[s.letter()]
	if collapse {
		l.collapsed[s.letter()] = true
		m.log.Info("section collapsed", "section", s.letter(), "reason", reason)
	} else {
		delete(l.collapsed, s.letter())
		m.log.Info("section expanded", "section", s.letter(), "reason", reason)
	}
	switch {
	case l.section != s:
	case collapse:
		l.onHeading = m.sectionCollapsedShown(s)
	case l.onHeading:
		l.onHeading = false
		m.cursorFrom(s)
	}
}

// cursorFrom puts the cursor on the first row from the section's start down, or
// the nearest above when there is none.
func (m *Model) cursorFrom(s section) {
	items := m.paneItems()
	at := slices.IndexFunc(items, func(it paneItem) bool { return it.section == s })
	if at < 0 {
		return
	}
	for j := at; j < len(items); j++ {
		if items[j].stop() {
			m.moveToItem(items[j])
			return
		}
	}
	for j := at - 1; j >= 0; j-- {
		if items[j].stop() {
			m.moveToItem(items[j])
			return
		}
	}
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
	if l == nil {
		return
	}
	if slices.Contains(sections, l.section) {
		m.keepCursorOffHidden()
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

// keepCursorOffHidden keeps the cursor off the rows a collapsed section hides,
// on its heading, and off the heading of a section showing its rows.
func (m *Model) keepCursorOffHidden() {
	l := m.list()
	switch shown := m.sectionCollapsedShown(l.section); {
	case shown && !l.onHeading:
		l.onHeading = true
		m.log.Debug("cursor on a collapsed section's heading", "section", l.section.letter())
	case !shown && l.onHeading:
		l.onHeading = false
		m.log.Debug("cursor off the heading of a section shown", "section", l.section.letter())
	}
}

// moveBy moves the cursor by step rows up the one list the sections make, past
// headings and what stands for rows; at either end it stays.
func (m *Model) moveBy(step int) {
	l, items := m.list(), m.paneItems()
	i := slices.IndexFunc(items, l.isCursor)
	if i < 0 && step < 0 {
		return
	}
	for j := i + step; j >= 0 && j < len(items); j += step {
		if it := items[j]; it.stop() {
			m.moveToItem(it)
			return
		}
	}
}

// moveToItem puts the cursor on the row it is, or the collapsed heading.
func (m *Model) moveToItem(it paneItem) {
	if it.heading {
		l := m.list()
		l.section, l.onHeading = it.section, true
		return
	}
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
	cursor := max(slices.IndexFunc(items, l.isCursor), 0)
	l.offset = max(min(l.offset, cursor), cursor-body+1)
	l.offset = max(min(l.offset, len(items)-body), 0)
	first := l.offset
	top = items[first].section
	if items[first].heading {
		first++ // the heading at the top says it
	}
	return items[first:min(first+body, len(items))], top
}

// wheel scrolls the list by a turn of the wheel in a pane of height rows, the
// cursor staying in view: when the scroll takes it out, it moves to the row
// nearest to where it was that the cursor can stop on.
func (s *projectScreen) wheel(msg tea.MouseWheelMsg, height int) {
	m := s.m
	l := m.list()
	if l == nil {
		return
	}
	items := m.paneItems()
	body := max(m.listHeight(height)-1, 1)
	from := l.offset
	switch msg.Button {
	case tea.MouseWheelUp:
		l.offset = max(l.offset-valueWheelStep, 0)
	case tea.MouseWheelDown:
		l.offset = max(min(l.offset+valueWheelStep, len(items)-body), 0)
	default:
		return
	}
	last := min(l.offset+body, len(items)) - 1
	cursor := slices.IndexFunc(items, l.isCursor)
	if cursor >= l.offset && cursor <= last {
		return
	}
	step, at := 1, l.offset // the cursor went off the top: the first stop below
	if cursor > last {
		step, at = -1, last
	}
	for j := at; j >= l.offset && j <= last; j += step {
		if items[j].stop() {
			m.moveToItem(items[j])
			m.log.Debug("cursor moved by the wheel", "from_offset", from, "offset", l.offset, "section", l.section.letter())
			return
		}
	}
}

// click takes a click at the column and row of the pane's area, of the given size:
// on a row's fold mark it folds or unfolds, elsewhere on a row it moves the cursor
// there and shows what the row is.
func (s *projectScreen) click(col, row, width, height int) tea.Cmd {
	l := s.m.list()
	if l == nil || row < 0 {
		return nil
	}
	if l.filter.on() && row == height-1 {
		s.m.openFilter()
		return nil
	}
	shown, top := s.m.visibleItems(s.m.listHeight(height))
	if row == 0 {
		s.m.toggleSection(top, "heading clicked")
		return nil
	}
	if row-1 >= len(shown) {
		return nil
	}
	it := shown[row-1]
	switch {
	case it.heading:
		s.m.toggleSection(it.section, "heading clicked")
		return nil
	case it.empty:
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
		s.m.clickPlan(it.row, col, width)
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
	switch {
	case r.isPlan() && col == ansi.StringWidth("▸ "):
		s.m.foldSlots(r.plan.Id, !s.m.list().slotsFolded[r.plan.Id])
		s.m.worktreesChanged()
		return nil
	case !r.isPlan() && r.file == nil && len(r.worktree.Files) > 0 && col == ansi.StringWidth("▸ ")+r.indent():
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
		lines := []string{sectionHeading(st, headingLead(false, false)+sectionAgents.title(), "", width), st.Muted(" loading…")}
		uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
		return
	}
	agents, rows, worktrees, code, marks, docs, plans := m.agents(), m.listRows(), m.worktreeRows(), m.codeRowsShown(), m.codeMarks(), m.docRows(), m.planRows()
	agentCount := fmt.Sprint(len(agents)-countRemoved(agents)) + " "
	worktreeCount := ""
	if m.worktreeStatuses() != nil {
		worktreeCount = fmt.Sprint(len(m.worktreeStatuses())) + " "
	}
	counts := map[section]string{
		sectionAgents:    agentCount,
		sectionWorktrees: worktreeCount,
		sectionDocs:      fmt.Sprint(len(m.docs())) + " ",
		sectionPlans:     fmt.Sprint(m.shownPlans()) + " ",
	}
	heading := func(s section) string {
		it := paneItem{section: s, heading: true, collapsed: m.sectionCollapsedShown(s)}
		cursor := l.isCursor(it) && m.layout.focus == project
		return sectionHeading(st, headingLead(l.collapsed[s.letter()], cursor)+s.title(), counts[s], width)
	}

	shown, top := m.visibleItems(m.listHeight(area.Dy()))
	lines := []string{heading(top)}
	// A parent folded over the active agent carries its mark.
	activeRow := shownAs(rows, agents, tabAgent(l.active))
	for _, it := range shown {
		cursor := l.isCursor(it) && m.layout.focus == project
		switch {
		case it.heading:
			lines = append(lines, heading(it.section))
		case it.empty && it.section == sectionCode:
			lines = append(lines, st.Muted(" "+m.codeNote()))
		case it.empty && m.filtering():
			lines = append(lines, st.Muted(" no match"))
		case it.empty && it.section == sectionAgents:
			lines = append(lines, st.Muted(" no agents: ")+st.Command("C-q a")+st.Muted(" creates one"))
		case it.empty && it.section == sectionDocs:
			lines = append(lines, st.Muted(" no docs: see ")+st.Command("wdx doc create"))
		case it.empty && it.section == sectionPlans && len(plans) > 0:
			lines = append(lines, st.Muted(" no plans in this view"))
		case it.empty && it.section == sectionPlans:
			lines = append(lines, st.Muted(" no plans: see ")+st.Command("wdx help plans"))
		case it.empty && m.worktreeStatuses() == nil:
			lines = append(lines, st.Muted(" loading…"))
		case it.empty:
			lines = append(lines, st.Muted(" no worktrees"))
		case it.section == sectionAgents:
			if r := rows[it.row]; r.plan != "" {
				lines = append(lines, m.planGroupLine(r, cursor, width))
			} else {
				lines = append(lines, agentRow(st, m.thresholds, r, cursor, it.row == activeRow, width))
			}
		case it.section == sectionWorktrees:
			r := worktrees[it.row]
			lines = append(lines, m.worktreeLine(r, cursor, m.worktreeRowActive(r), width))
		case it.section == sectionCode:
			lines = append(lines, m.codeLine(code[it.row], marks, cursor, width))
		case it.section == sectionDocs:
			lines = append(lines, docLine(st, docs[it.row], cursor, m.docRowActive(docs[it.row]), width))
		case it.section == sectionPlans:
			r := plans[it.row]
			lines = append(lines, planLine(st, r, l.plansView, l.plansFolded[r.plan.Id], cursor, m.planRowActive(r), width))
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

// headingLead is what a section's heading starts with: the cursor's pointer when
// it is there, and the mark of a section collapsed or open.
func headingLead(collapsed, cursor bool) string {
	lead := " "
	if cursor {
		lead = "▸"
	}
	if collapsed {
		return lead + foldClosed + " "
	}
	return lead + foldOpen + " "
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
// tell, its context size and, when active, a mark that shows with color off too.
// planGroupLine is the row of a plan in the Agents section, width wide: its fold
// mark, its name, its state and progress as the Plans section has them, and, folded,
// how many agents it hides or are waiting, and whether it needs the person. Its
// tab's being the current one gives it the active mark.
func (m *Model) planGroupLine(r agentListRow, cursor bool, width int) string {
	st := m.st
	p, known := m.planNamed(r.plan)
	l := m.list()
	active := known && l != nil && l.active == planTab(p.Id)
	pointer, end := "  ", " "
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if active {
		end = " " + st.Focused("◂") + " "
	}
	label := r.plan
	if known {
		label = p.Name // an agent's task may name it in another case
	}
	room := max(width-style.Width(pointer)-style.Width(end)-ansi.StringWidth(r.fold+" plan "), 0)
	name := ansi.Truncate(label, room, "…")
	room -= ansi.StringWidth(name)
	var parts []string
	if known {
		parts = append(parts, string(p.State)+" "+planProgress(p))
	}
	if detail := agentDetail(r); r.fold == foldClosed && detail != "" {
		parts = append(parts, detail)
	}
	if known && m.planNeedsYou(p) && r.fold == foldClosed {
		parts = append(parts, "needs you")
	}
	tail := ""
	for _, part := range parts {
		if ansi.StringWidth(" "+part) > room {
			break // a word cut short would say another
		}
		room -= ansi.StringWidth(" " + part)
		if part == "needs you" || strings.HasSuffix(part, " waiting") {
			tail += " " + st.Warning(part)
		} else {
			tail += st.Muted(" " + part)
		}
	}
	if cursor || active {
		name = st.Name(name)
	}
	row := pointer + r.fold + " " + st.Muted("plan ") + name + tail + strings.Repeat(" ", max(room, 0)) + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

func agentRow(st style.Style, thresholds contextThresholds, r agentListRow, cursor, active bool, width int) string {
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
	detail, size := agentDetail(r), contextSize(a)
	// The size gives way first, then the detail, so that the name keeps its room.
	if room-endWidth(detail)-endWidth(size) < minAgentName {
		size = ""
	}
	if room-endWidth(detail) < minAgentName {
		detail = ""
	}
	room -= endWidth(detail) + endWidth(size)
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
	if size != "" {
		size = " " + st.Context(a.Context.Tokens, thresholds.warning, thresholds.critical, size)
	}
	if r.fold != "" {
		guide += r.fold + " "
	}
	row := pointer + st.Muted(guide) + agentMark(st, a) + " " + name + gap + detail + size + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// endWidth is the cells text takes at a row's end, after a space: none for none.
func endWidth(text string) int {
	if text == "" {
		return 0
	}
	return ansi.StringWidth(text) + 1
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
		if v, ok := m.planTabView(); ok {
			m.drawPlanTab(buf, area, v, st)
			return
		}
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
