package tide

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The Plans section of the left pane, a plan of plans with its plans under it, and
// a plan's tab, which plantab.go draws. tide never changes a
// plan: the CLI does (docs/PLANS.md).

const (
	planTabPrefix = "plan:"
	planTabLabel  = "plan: "
	planHint      = "↑↓ select  Enter open  m move  / filter  Alt+arrows move  C-q menu"
	parentHint    = "↑↓ select  ←→ fold  Enter open  m move  / filter  Alt+arrows move  C-q menu"
	planGroupHint = "↑↓ select  ←→ fold  Enter open  / filter  Alt+arrows move  C-q menu" // a plan's row among agents or worktrees
	plansViewHint = "↑↓ select  ←→ view  / filter  Alt+arrows move  C-q menu"
	// planSelectorKey names the selector's row, the section's first, as an id names a plan's.
	planSelectorKey = "selector"
)

// plansView is which plans the section shows, by their state, as its selector
// chooses. The zero value is Draft & in progress, which a project starts on.
type plansView string

const (
	plansGoingOn   plansView = "" // a project view without plans_view means it
	plansAll       plansView = "all"
	plansCompleted plansView = "completed"
)

// plansViews are the views in the order the selector goes through them.
var plansViews = []plansView{plansAll, plansGoingOn, plansCompleted}

// plansViewOf is the view the project's view kept; one tide does not know is
// Draft & in progress.
func plansViewOf(key string) plansView {
	if v := plansView(key); slices.Contains(plansViews, v) {
		return v
	}
	return plansGoingOn
}

// title is what the selector shows of the view.
func (v plansView) title() string {
	switch v {
	case plansAll:
		return "All"
	case plansCompleted:
		return "Completed"
	}
	return "Draft & in progress"
}

// shows reports whether the view shows a plan in state.
func (v plansView) shows(state api.PlanState) bool {
	over := state == api.PlanStateDone || state == api.PlanStateCancelled
	switch v {
	case plansAll:
		return true
	case plansCompleted:
		return over
	}
	return !over
}

// planTab is the tab of the plan with id, which a rename keeps.
func planTab(id string) string { return planTabPrefix + id }

func isPlanTab(tab string) bool { return strings.HasPrefix(tab, planTabPrefix) }

// plans returns the plans of the project shown, by name, as the stream sends them.
func (m *Model) plans() []api.Plan {
	if f := m.following; f != nil && f.project != nil && f.project.Plans != nil {
		return *f.project.Plans
	}
	return nil
}

// planOf returns the plan of the project shown whose id is id.
func (m *Model) planOf(id string) (api.Plan, bool) {
	plans := m.plans()
	if i := slices.IndexFunc(plans, func(p api.Plan) bool { return p.Id == id }); i >= 0 {
		return plans[i], true
	}
	return api.Plan{}, false
}

// planNamed returns the plan of the project shown named name, ignoring case, as a
// plan of plans names its plans.
func (m *Model) planNamed(name string) (api.Plan, bool) {
	plans := m.plans()
	if i := slices.IndexFunc(plans, func(p api.Plan) bool { return strings.EqualFold(p.Name, name) }); i >= 0 {
		return plans[i], true
	}
	return api.Plan{}, false
}

// isParent reports whether p is a plan of plans.
func isParent(p api.Plan) bool { return p.Plans != nil }

// planRow is a row of the Plans section: a plan, or a folder when place says so.
type planRow struct {
	selector bool // it is the selector of the plans shown, the section's first row
	plan     api.Plan
	place    treePlace
	child    bool // it is listed under its plan of plans
	match    bool // it matches the filter
	needsYou bool // a task of it failed or did not land, or its agent waits for input; a folded folder's, of a plan in it
}

// key names the row across events: its plan's id, or its folder.
func (r planRow) key() string {
	if r.selector {
		return planSelectorKey
	}
	if r.place.folder != "" {
		return r.place.key()
	}
	return r.plan.Id
}

// planGroup is a plan no other lists, with the rows of the plans it lists.
type planGroup struct {
	plan     planRow
	children []planRow
}

// planRows are the rows of the Plans section: the selector, with any plan, then
// the plans in their folders: each plan no other lists that is in the view
// chosen, and under a plan of plans not folded the plans it lists, whatever their
// state, in its DAG order, which share its folder. With a filter, the plans of
// every view whose name holds its words, and a plan of plans with one of its plans
// that does, folded or not, under their folders unfolded.
func (m *Model) planRows() []planRow {
	l := m.list()
	if l == nil || len(m.plans()) == 0 {
		return nil
	}
	words := l.filter.words()
	// The archived are out of the section, but for the filter, which is for finding.
	hidden := func(p api.Plan) bool { return words == nil && isArchived(p) }
	var groups []planGroup
	for _, p := range m.plans() {
		if p.Parent != nil || hidden(p) || words == nil && !l.plansView.shows(p.State) {
			continue
		}
		var children []planRow
		if isParent(p) && (words != nil || !l.plansFolded[p.Id]) {
			for _, name := range *p.Plans {
				c, ok := m.planNamed(name)
				if !ok || hidden(c) {
					continue
				}
				match := words != nil && matches(c.Name, words)
				if words == nil || match || matches(p.Name, words) {
					children = append(children, planRow{plan: c, child: true, match: match, needsYou: m.planNeedsYou(c)})
				}
			}
		}
		match := words != nil && matches(p.Name, words)
		if words != nil && !match && !slices.ContainsFunc(children, func(r planRow) bool { return r.match }) {
			continue
		}
		groups = append(groups, planGroup{plan: planRow{plan: p, match: match, needsYou: m.planNeedsYou(p)}, children: children})
	}
	folded := l.planFolders
	if words != nil {
		folded = nil
	}
	rows := []planRow{{selector: true}}
	folderTree(groups, func(g planGroup) string { return g.plan.plan.Folder }, folded, func(p treePlace, g *planGroup) {
		if g == nil {
			rows = append(rows, planRow{place: p, needsYou: p.fold == foldClosed && m.folderNeedsYou(p.folder)})
			return
		}
		g.plan.place = p
		rows = append(rows, g.plan)
		for i, c := range g.children {
			c.place = treePlace{depth: p.depth + 1}
			c.place.guide, c.place.next = treeGuides(p.next, c.place.depth, i == len(g.children)-1)
			rows = append(rows, c)
		}
	})
	return rows
}

// folderNeedsYou reports whether a plan the view shows in the folder at path, or
// under it, waits for the person.
func (m *Model) folderNeedsYou(path string) bool {
	return slices.ContainsFunc(m.plans(), func(p api.Plan) bool {
		return inFolder(p.Folder, path) && m.inPlansView(p) && m.planNeedsYou(p)
	})
}

// isArchived reports whether p is archived, which leaves it out of the section.
func isArchived(p api.Plan) bool { return p.ArchivedAt != nil }

// inPlansView reports whether the view chosen shows p, the filter aside: not
// archived, and in the view by its own state, or a plan of plans' that lists it.
func (m *Model) inPlansView(p api.Plan) bool {
	top := p
	if p.Parent != nil {
		if parent, ok := m.planNamed(*p.Parent); ok {
			top = parent
		}
	}
	return !isArchived(p) && !isArchived(top) && m.list().plansView.shows(top.State)
}

// shownPlans counts the plans the section's heading counts: those the view shows.
func (m *Model) shownPlans() int {
	n := 0
	for _, p := range m.plans() {
		if m.inPlansView(p) {
			n++
		}
	}
	return n
}

// choosePlansView goes by places through the views, going round at the ends, and
// puts the cursor on the selector.
func (m *Model) choosePlansView(by int) {
	l := m.list()
	i := slices.Index(plansViews, l.plansView)
	l.plansView = plansViews[(i+by+len(plansViews))%len(plansViews)]
	m.moveToPlan(0)
	m.log.Info("plans view chosen", "view", l.plansView.title())
}

// planNeedsYou reports whether the plan waits for the person: a task of it failed
// or did not land, or the agent of one waits for input; a plan its parent lists
// waits for their answer, as the parent's plans_waiting says; a plan of plans has
// one of those.
func (m *Model) planNeedsYou(p api.Plan) bool {
	if isArchived(p) {
		return false // it is over
	}
	if p.TasksFailed != nil && *p.TasksFailed > 0 {
		return true
	}
	if p.Parent != nil {
		if parent, ok := m.planNamed(*p.Parent); ok && parent.PlansWaiting != nil && slices.ContainsFunc(*parent.PlansWaiting, func(n string) bool { return strings.EqualFold(n, p.Name) }) {
			return true
		}
	}
	if isParent(p) {
		return slices.ContainsFunc(*p.Plans, func(name string) bool {
			c, ok := m.planNamed(name)
			return ok && !isParent(c) && m.planNeedsYou(c)
		})
	}
	return slices.ContainsFunc(m.agents(), func(a api.Agent) bool {
		return a.Task != nil && strings.EqualFold(a.Task.Plan, p.Name) && agentNeedsYou(a)
	})
}

// plansChanged keeps the cursor on its plan, and closes the tabs of the plans gone.
// A project from a daemon that sends no plans says nothing of them, and closes none.
func (m *Model) plansChanged() {
	l := m.list()
	if l == nil {
		return
	}
	if f := m.following; f.project.Plans != nil {
		for _, tab := range slices.Clone(l.tabs) {
			id := strings.TrimPrefix(tab, planTabPrefix)
			if _, ok := m.planOf(id); !isPlanTab(tab) || ok {
				continue
			}
			name := l.planNames[id]
			if name == "" {
				name = id
			}
			m.closeTab(tab, "plan removed")
			delete(l.planViews, tab)
			m.statusNotice = "plan " + name + " removed"
		}
		l.planNames = map[string]string{}
		for _, p := range m.plans() {
			l.planNames[p.Id] = p.Name
		}
	}
	m.keepPlanCursor()
	m.keepWorktreeCursor() // a plan's slots are under its row
}

// keepPlanCursor keeps the cursor on its plan, or on the one that took its row;
// with none left it goes back to the agents.
func (m *Model) keepPlanCursor() {
	l, rows := m.list(), m.planRows()
	if i := slices.IndexFunc(rows, func(r planRow) bool { return r.key() == l.planCursor }); i >= 0 {
		l.planRow = i
		return
	}
	if len(rows) == 0 {
		if l.section == sectionPlans {
			l.section = sectionAgents
		}
		l.planRow, l.planCursor = 0, ""
		return
	}
	l.planRow = max(min(l.planRow, len(rows)-1), 0)
	l.planCursor = rows[l.planRow].key()
}

// moveToPlan puts the cursor on the row i of the section.
func (m *Model) moveToPlan(i int) {
	l, rows := m.list(), m.planRows()
	if i < 0 || i >= len(rows) {
		return
	}
	l.section, l.planRow, l.planCursor, l.onHeading = sectionPlans, i, rows[i].key(), false
}

// cursorPlanRow returns the row under the cursor in the Plans section.
func (m *Model) cursorPlanRow() (planRow, bool) {
	l, rows := m.list(), m.planRows()
	if l == nil || l.planRow >= len(rows) {
		return planRow{}, false
	}
	return rows[l.planRow], true
}

// cursorPlan returns the plan under the cursor, none on a folder or the selector.
func (m *Model) cursorPlan() (api.Plan, bool) {
	r, ok := m.cursorPlanRow()
	if !ok || r.selector || r.place.folder != "" {
		return api.Plan{}, false
	}
	return r.plan, true
}

// planEnter opens the tab of the plan under the cursor; on a folder it folds or
// unfolds it.
func (m *Model) planEnter(goToTab bool) {
	if r, ok := m.cursorPlanRow(); ok && r.place.folder != "" {
		m.foldPlanFolder(r.place.folder, r.place.fold == foldOpen)
		return
	}
	if p, ok := m.cursorPlan(); ok {
		m.openPlan(p, goToTab)
	}
}

// foldPlanFolder folds the folder at path, or unfolds it, the cursor going to it.
func (m *Model) foldPlanFolder(path string, fold bool) {
	l := m.list()
	if setFolded(&l.planFolders, path, fold) {
		m.log.Info("plan folder folded", "folder", path, "folded", fold)
		m.changed("plan folder")
	}
	l.planCursor = folderRowKey + path
	m.keepPlanCursor()
}

// planFold folds the plans of the plan of plans under the cursor, or unfolds them,
// as ← and → do in the Agents section. ← on one of its plans folds it, the cursor
// going to it. On a folder it folds or unfolds the folder, and ← on a plan in a
// folder, not a plan of plans unfolded, folds that folder. On the selector ← goes
// to the view before and → to the one after.
func (m *Model) planFold(fold bool) {
	if r, ok := m.cursorPlanRow(); ok && r.selector {
		if fold {
			m.choosePlansView(-1)
		} else {
			m.choosePlansView(1)
		}
		return
	}
	if r, ok := m.cursorPlanRow(); ok && r.place.folder != "" {
		m.foldPlanFolder(r.place.folder, fold)
		return
	}
	p, ok := m.cursorPlan()
	if ok && fold && !atTop(p.Folder) && p.Parent == nil && (!isParent(p) || m.list().plansFolded[p.Id]) {
		m.foldPlanFolder(p.Folder, true)
		return
	}
	if ok && fold && p.Parent != nil {
		if parent, found := m.planNamed(*p.Parent); found {
			p = parent
		}
	}
	if !ok || !isParent(p) {
		return
	}
	m.setPlanFolded(p, fold)
	m.list().planCursor = p.Id
	m.keepPlanCursor()
}

// setPlanFolded folds the plans of the plan of plans p, or unfolds them.
func (m *Model) setPlanFolded(p api.Plan, fold bool) {
	l := m.list()
	if l.plansFolded == nil {
		l.plansFolded = map[string]bool{}
	}
	if l.plansFolded[p.Id] == fold {
		return
	}
	l.plansFolded[p.Id] = fold
	m.log.Info("plan folded", "plan_id", p.Id, "folded", fold)
}

// clickPlan takes a click on the row i of the Plans section, in a pane width wide:
// on the selector's ends it goes through the views, on a plan of plans' fold mark it
// folds or unfolds, elsewhere it opens the row's plan.
func (m *Model) clickPlan(i, col, width int) {
	rows := m.planRows()
	m.moveToPlan(i)
	r := rows[i]
	if r.selector {
		pointer := ansi.StringWidth("▸ ")
		switch col {
		case pointer:
			m.choosePlansView(-1)
		case pointer + ansi.StringWidth("< "+selectorName(m.list().plansView.title(), width)+" "):
			m.choosePlansView(1)
		}
		return
	}
	if r.place.folder != "" {
		m.foldPlanFolder(r.place.folder, r.place.fold == foldOpen)
		return
	}
	if isParent(r.plan) && !r.child && col == ansi.StringWidth("▸ ")+ansi.StringWidth(r.place.guide) {
		m.setPlanFolded(r.plan, !m.list().plansFolded[r.plan.Id])
		m.keepPlanCursor()
		return
	}
	m.planEnter(false)
}

// openPlan opens p's tab, or goes to it, the focus following as showOpened says.
func (m *Model) openPlan(p api.Plan, goToTab bool) {
	m.list().openTab(planTab(p.Id))
	m.showOpened(goToTab)
	m.changed("plan")
	m.log.Info("plan opened", "plan_id", p.Id, "name", p.Name)
}

// planHintFor is the bar's hint in the section: a plan of plans and a folder fold.
func (m *Model) planHintFor() string {
	r, ok := m.cursorPlanRow()
	if ok && r.selector {
		return plansViewHint
	}
	if ok && r.place.folder != "" {
		return folderHint
	}
	if p, ok := m.cursorPlan(); ok && isParent(p) {
		return parentHint
	}
	return planHint
}

// planRowShows reports whether tab is the row's plan's, or the row is a folded
// folder holding its plan.
func (m *Model) planRowShows(r planRow, tab string) bool {
	if r.place.folder != "" {
		p, ok := m.planOf(strings.TrimPrefix(tab, planTabPrefix))
		return r.place.fold == foldClosed && isPlanTab(tab) && ok && inFolder(p.Folder, r.place.folder)
	}
	return tab == planTab(r.plan.Id)
}

// planSize is how many tasks a plan has, or plans for a plan of plans.
func planSize(p api.Plan) string {
	if isParent(p) {
		return plural(len(*p.Plans), "plan")
	}
	if p.Tasks != nil {
		return plural(int(*p.Tasks), "task")
	}
	return ""
}

// planProgress is how far a plan's run is, as 2/4, or its size until it runs.
func planProgress(p api.Plan) string {
	if p.PlansDone != nil && p.Plans != nil {
		return fmt.Sprintf("%d/%s", *p.PlansDone, plural(len(*p.Plans), "plan"))
	}
	if p.TasksDone != nil && p.Tasks != nil {
		return fmt.Sprintf("%d/%d", *p.TasksDone, *p.Tasks)
	}
	return planSize(p)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// planLine is the row r of the section, width wide: the selector with the view
// chosen, or the tree's lines, then a folder's fold mark and name, or a plan of
// plans' fold mark, a plan's indent under it, the name, its state and its size, the
// state giving way first.
func planLine(st style.Style, r planRow, view plansView, folded bool, look rowLook, width int) string {
	pointer, end := look.lead(st), " "
	if r.selector {
		return look.paint(st, padRow(pointer+st.Muted("<")+" "+st.Name(selectorName(view.title(), width))+" "+st.Muted(">"), width))
	}
	lead := treeLead(r.place)
	switch {
	case r.place.folder != "" || r.child:
	case isParent(r.plan) && folded:
		lead = r.place.guide + foldClosed + " "
	case isParent(r.plan):
		lead = r.place.guide + foldOpen + " "
	}
	room := max(width-style.Width(pointer)-style.Width(end)-ansi.StringWidth(lead), 0)
	if r.place.folder != "" {
		return folderLine(st, r.place, r.needsYou, pointer, end, look, room)
	}
	name := ansi.Truncate(r.plan.Name, room, "…")
	room -= ansi.StringWidth(name)
	size, state, need := " "+planProgress(r.plan), " "+string(r.plan.State), ""
	if isArchived(r.plan) {
		state += ", archived"
	}
	if r.needsYou {
		need = " needs you"
	}
	if ansi.StringWidth(need) > room {
		need = "" // a word cut short would say another
	}
	room -= ansi.StringWidth(need)
	if ansi.StringWidth(size) > room {
		size = ""
	}
	room -= ansi.StringWidth(size)
	if ansi.StringWidth(state) > room {
		state = ""
	}
	room -= ansi.StringWidth(state)
	gap := strings.Repeat(" ", max(room, 0))
	if look.bold() {
		name = st.Name(name)
	}
	if r.plan.State == api.PlanStateDraft || isArchived(r.plan) {
		state = st.Muted(state)
	}
	return look.paint(st, pointer+st.Muted(lead)+name+state+size+st.Warning(need)+gap+end)
}

// planView is what a plan's tab holds: the revision last read, and how its list
// is shown.
type planView struct {
	content *api.PlanContent
	err     string
	asked   bool      // it was asked for
	loading bool      // an answer is on its way
	seen    time.Time // the plan's updated_at it was asked at

	cursor   string // the task under the cursor, by id
	offset   int    // the list's rows scrolled past
	reveal   bool   // the next draw scrolls the cursor into view
	unfolded bool   // the constraints are shown, and the goal when it folds with them
	timing   bool   // a planMinute is on its way

	reading    string // the task whose whole prompt is shown, by id; "" for the list
	readOffset int    // its lines scrolled past

	goals map[string]*childGoal // a plan of plans' plans' goals, by name
	// movedOn are the stacked plans a plan of plans lists whose worktree's base has
	// moved on, by name, as the worktrees were at the last draw.
	movedOn map[string]bool

	// rail is the rail laid out for railRevision; railDrawn is false when it has
	// too many lanes, and is a row per task.
	rail         []railRow
	railDrawn    bool
	railRevision int64
}

// planLoaded is the answer to reading a plan's tab.
type planLoaded struct {
	projectID, tab string
	content        api.PlanContent
	err            error
}

// planShown reports whether the agent pane shows a plan.
func (m *Model) planShown() bool {
	l := m.list()
	return l != nil && isPlanTab(l.active)
}

// planTitle is what the tab strip calls a plan's tab: its name and revision, or its
// id until the stream brings it.
func (m *Model) planTitle(tab string) string {
	id := strings.TrimPrefix(tab, planTabPrefix)
	p, ok := m.planOf(id)
	if !ok {
		return planTabLabel + id
	}
	return fmt.Sprintf("%s%s r%d", planTabLabel, p.Name, p.Revision)
}

// planSync reads the plan of the tab shown when it has not been, or the stream
// brought a change to it: a revision, or a step of its run, which moves its
// updated_at on. A tab not shown is read when it comes to show.
func (m *Model) planSync() tea.Cmd {
	read := m.planRead()
	return tea.Batch(read, m.planGoalRead(), m.planTimer())
}

// planRead reads the plan of the tab shown when planSync says so.
func (m *Model) planRead() tea.Cmd {
	l := m.list()
	if l == nil || !isPlanTab(l.active) {
		return nil
	}
	if l.planViews == nil {
		l.planViews = map[string]*planView{}
	}
	v, ok := l.planViews[l.active]
	if !ok {
		v = &planView{reveal: true}
		l.planViews[l.active] = v
	}
	p, ok := m.planOf(strings.TrimPrefix(l.active, planTabPrefix))
	if !ok || v.loading || (v.asked && v.seen.Equal(p.UpdatedAt)) {
		return nil
	}
	v.asked, v.loading, v.seen = true, true, p.UpdatedAt
	return m.readPlan(l.active)
}

// readPlan asks the daemon for the plan of tab.
func (m *Model) readPlan(tab string) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	projectID := f.project.Id
	return func() tea.Msg {
		c, err := m.client.GetPlan(m.ctx, "id:"+strings.TrimPrefix(tab, planTabPrefix), "id:"+projectID, "", 0)
		return planLoaded{projectID: projectID, tab: tab, content: c, err: err}
	}
}

func (m *Model) planLoadedMsg(msg planLoaded) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.planViews[msg.tab] == nil {
		return
	}
	v := l.planViews[msg.tab]
	v.loading = false
	if msg.err != nil {
		m.log.Info("plan not read", "tab", msg.tab, "err", msg.err)
		v.err = msg.err.Error()
		return
	}
	v.err, v.content = "", &msg.content
}

// planTabRows are the rows of the plan's tab shown while it is not read: that
// it is loading, or why it could not be read. A read plan is drawn by
// layoutPlanTab.
func (m *Model) planTabRows() []valueRow {
	l := m.list()
	if v := l.planViews[l.active]; v != nil && v.content == nil && v.err != "" {
		return []valueRow{{key: "error", values: []string{v.err}}}
	}
	return []valueRow{{key: "plan", values: []string{"loading…"}}}
}

// openChild is what opens the tab of the plan named name, which a plan of plans
// lists.
func (m *Model) openChild(name string) func() tea.Cmd {
	return func() tea.Cmd {
		if p, ok := m.planNamed(name); ok {
			m.list().openTab(planTab(p.Id))
			m.changed("plan")
			m.log.Info("plan opened", "plan_id", p.Id, "name", p.Name, "from", "its parent")
		}
		return nil
	}
}

// taskLine is a task's state as its row says it, with its agent: a pending task
// waits for its afters or is ready, a failed one says why.
func taskLine(t api.PlanTask) string {
	var s string
	switch t.State {
	case api.PlanTaskStatePending:
		s = "ready"
		if len(t.WaitingFor) > 0 {
			s = "waiting for " + strings.Join(t.WaitingFor, ", ")
		}
	case api.PlanTaskStateLandFailed:
		s = "land failed"
	default:
		s = string(t.State)
	}
	if t.Failure != nil {
		s += ": " + strings.ReplaceAll(string(*t.Failure), "_", " ")
	}
	if t.CheckTerminal != nil {
		s += " (terminal " + *t.CheckTerminal + ")"
	}
	if t.Agent != nil {
		s += " · " + t.Agent.Name
	}
	return s
}

// goToTaskAgent is what opens the tab of the task's agent with id.
func (m *Model) goToTaskAgent(id string) func() tea.Cmd {
	return func() tea.Cmd {
		m.list().openTab(id)
		m.changed("agent")
		m.log.Info("agent active", "agent_id", id, "reason", "its plan task")
		return nil
	}
}

// runPlanKind is the kind of a confirmation that runs a plan: it asks as a removal
// does, as it starts agents.
const runPlanKind = "plan run"

// planKey takes a key the plan's tab has, and reports whether it took it: r runs
// a draft, once confirmed; p pauses a running plan and resumes a paused one; c
// cancels one going on, once confirmed; a archives a done or cancelled one, or
// unarchives it; y copies it, in any state.
func (m *Model) planKey(msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || m.values.edit != nil {
		return nil, false
	}
	l := m.list()
	v := l.planViews[l.active]
	if v == nil || v.content == nil {
		return nil, false
	}
	p, run := v.content.Plan, v.content.Run
	switch k.String() {
	case "r":
		if child, ok := m.waitingChildRow(v.content); ok {
			return m.answerChild(child), true
		}
		if p.State != api.PlanStateDraft || p.Parent != nil {
			return nil, false
		}
		m.confirm = &confirmBox{r: removal{kind: runPlanKind, id: p.Id, name: p.Name}}
		m.log.Info("plan run asked", "plan_id", p.Id, "name", p.Name)
		return nil, true
	case "p":
		closed := run != nil && run.PausedReason != nil && *run.PausedReason == api.PlanRunPausedReasonProjectClosed
		if p.State == api.PlanStateRunning {
			return m.pausePlan(p, true), true
		}
		if p.State == api.PlanStatePaused && !closed {
			return m.pausePlan(p, false), true
		}
		return nil, false
	case "c":
		if !goingOn(p.State) {
			return nil, false
		}
		m.confirm = &confirmBox{r: removal{kind: cancelPlanKind, id: p.Id, name: p.Name}}
		m.log.Info("plan cancel asked", "plan_id", p.Id, "name", p.Name)
		return nil, true
	case archiveKey:
		if p.State != api.PlanStateDone && p.State != api.PlanStateCancelled {
			return nil, false
		}
		return m.archivePlan(p, !isArchived(p)), true
	case copyKey:
		m.askCopyPlan(p)
		return nil, true
	}
	return nil, false
}

// goingOn reports whether a plan in state has a run to pause, cancel or answer.
func goingOn(state api.PlanState) bool {
	return state == api.PlanStateRunning || state == api.PlanStatePaused
}

// planRan is the answer to running a plan; answer says it was the person's answer
// to a plan its parent lists.
type planRan struct {
	r      removal
	err    error
	answer bool
}

// runPlan asks the daemon to run the plan r names. Its state comes with the stream.
func (m *Model) runPlan(r removal) tea.Cmd {
	m.log.Info("plan run confirmed", "plan_id", r.id, "name", r.name)
	return func() tea.Msg {
		_, err := m.client.RunPlan(m.ctx, "id:"+r.id, "", "", 0)
		return planRan{r: r, err: err}
	}
}

func (m *Model) planRanMsg(msg planRan) {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("plan not run", "plan_id", msg.r.id, "err", msg.err)
		m.statusNotice = msg.err.Error()
		return
	}
	m.log.Info("plan running", "plan_id", msg.r.id, "name", msg.r.name)
	if msg.answer {
		m.statusNotice = "plan " + msg.r.name + " started"
	}
}

// waitingChildRow returns the plan under the cursor of the tab of the plan of plans
// c, when it waits for the person's answer.
func (m *Model) waitingChildRow(c *api.PlanContent) (api.PlanChild, bool) {
	v, ok := m.planTabView()
	if !ok || v.content != c || c.Definition.Plans == nil || m.layout.focus != agent {
		return api.PlanChild{}, false
	}
	if it, ok := v.cursorItem(); ok && it.child != nil && it.child.Waiting {
		return *it.child, true
	}
	return api.PlanChild{}, false
}

// answerChild asks the daemon to start the plan child, which waits for the person:
// it starts it, or says why not, as plan run does.
func (m *Model) answerChild(child api.PlanChild) tea.Cmd {
	p, ok := m.planNamed(child.Id)
	if !ok {
		return nil
	}
	r := removal{kind: runPlanKind, id: p.Id, name: p.Name}
	m.log.Info("plan answer asked", "plan_id", p.Id, "name", p.Name)
	return func() tea.Msg {
		_, err := m.client.RunPlan(m.ctx, "id:"+r.id, "", "", 0)
		return planRan{r: r, err: err, answer: true}
	}
}

// runPlanPopup is the confirmation of running a plan.
func (m *Model) runPlanPopup() popup {
	c, st := m.confirm, m.st
	body := []string{
		"Run " + st.Name(c.r.name) + "?",
		"Its tasks' agents start, each in a worktree of the plan's, up to",
		"plan.parallel at once, and land on the branch plan/" + c.r.name + ".",
		"A plan runs once, and cannot be changed after.",
	}
	if p, ok := m.planOf(c.r.id); ok && isParent(p) {
		body = []string{
			"Run " + st.Name(c.r.name) + "?",
			"Each plan it lists runs from the default branch once the plans",
			"before it have landed there; one it cannot see landed waits for you.",
			"A plan runs once, and cannot be changed after.",
		}
	}
	if c.busy {
		body = append(body, "", st.Muted("starting…"))
	}
	return popup{
		title:  "Run plan",
		body:   body,
		footer: st.Command("y") + " run  " + st.Command("n") + " or " + st.Command("Esc") + " keep",
	}
}
