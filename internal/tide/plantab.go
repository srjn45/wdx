package tide

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// A plan's tab, drawn from its content: a header with its state and progress,
// what it is about, its tasks with the rail of its DAG at their left, the panel
// of the task under the cursor, and the keys its state takes (docs/PLANS.md,
// Step 8). A plan of plans is drawn the same, its plans the rows. A plan not
// read yet is the value pane of planTabRows.

const (
	// planPanelFrom is the pane's width from which the task's panel is at the
	// right of the list, not under it.
	planPanelFrom = 90
	// planPanelColumns is the panel's inside at the right; planPanelLines its
	// inside under the list.
	planPanelColumns = 35
	planPanelLines   = 4
	// planGoalFrom is the pane's height from which the goal is shown; below it,
	// it folds with the constraints.
	planGoalFrom = 20
	// planBarMost and planBarLeast are the progress bar's widest and narrowest;
	// narrower than the least, it is left out.
	planBarMost  = 40
	planBarLeast = 10
	// planMarkColumns is the room kept at a task's row's end for its mark, the
	// widest being "▼ unblocks".
	planMarkColumns = 10
	// planRailGap is between the rail and a task's id.
	planRailGap = 2

	planMarkNeeds    = "▲ needs"
	planMarkUnblocks = "▼ unblocks"
	planMarkNeedsYou = "needs you"
)

// planTabView returns the view of the plan's tab shown, once it is read.
func (m *Model) planTabView() (*planView, bool) {
	l := m.list()
	if l == nil || !isPlanTab(l.active) {
		return nil, false
	}
	v := l.planViews[l.active]
	if v == nil || v.content == nil {
		return nil, false
	}
	return v, true
}

// planTasks returns the tasks of c, in DAG order.
func planTasks(c *api.PlanContent) []api.PlanTask {
	if c.Definition.Tasks == nil {
		return nil
	}
	return *c.Definition.Tasks
}

// planItem is a row of a plan's list: one of its tasks, or one of the plans a
// plan of plans lists.
type planItem struct {
	id    string
	after []string
	task  *api.PlanTask  // nil for a plan
	child *api.PlanChild // nil for a task
	// before are the plans before a plan that are not done yet.
	before []string
}

// planItems returns the rows of c's list, in DAG order: its tasks, or the plans
// it lists.
func planItems(c *api.PlanContent) []planItem {
	if ps := c.Definition.Plans; ps != nil {
		items := make([]planItem, len(*ps))
		for i := range *ps {
			child := &(*ps)[i]
			var before []string
			for _, a := range child.After {
				j := slices.IndexFunc(*ps, func(o api.PlanChild) bool { return o.Id == a })
				if j < 0 || (*ps)[j].State != api.PlanStateDone {
					before = append(before, a)
				}
			}
			items[i] = planItem{id: child.Id, after: child.After, child: child, before: before}
		}
		return items
	}
	tasks := planTasks(c)
	items := make([]planItem, len(tasks))
	for i := range tasks {
		items[i] = planItem{id: tasks[i].Id, after: tasks[i].After, task: &tasks[i]}
	}
	return items
}

// railOf returns the rail of v's plan, laid out again only on a new revision;
// false when it has too many lanes to draw.
func (v *planView) railOf() ([]railRow, bool) {
	if v.rail == nil || v.railRevision != v.content.Revision {
		items := planItems(v.content)
		in := make([]railTask, len(items))
		for i, it := range items {
			in[i] = railTask{id: it.id, after: it.after}
		}
		rows, ok := layoutRail(in)
		if !ok {
			rows = make([]railRow, len(items))
			for i, it := range items {
				rows[i] = railRow{task: it.id}
			}
		}
		v.rail, v.railDrawn, v.railRevision = rows, ok, v.content.Revision
	}
	return v.rail, v.railDrawn
}

// cursorItem returns the row under v's cursor: the one it names, or the first
// when that one is gone.
func (v *planView) cursorItem() (planItem, bool) {
	items := planItems(v.content)
	if len(items) == 0 {
		return planItem{}, false
	}
	if i := slices.IndexFunc(items, func(it planItem) bool { return it.id == v.cursor }); i >= 0 {
		return items[i], true
	}
	v.cursor = items[0].id
	return items[0], true
}

// planDrawn is a plan's tab laid out: its lines, and the row of the list each
// line shows, by id.
type planDrawn struct {
	lines []string
	items map[int]string
}

// drawPlanTab draws the plan's tab of v in area.
func (m *Model) drawPlanTab(buf uv.Screen, area uv.Rectangle, v *planView, st style.Style) {
	d := layoutPlanTab(v, area.Dx(), area.Dy(), m.now(), st)
	uv.NewStyledString(strings.Join(d.lines, "\n")).Draw(buf, area)
}

// layoutPlanTab lays out the tab of v width × height: the header, what it is
// about, the box of the list and the panel, and the keys on the last line.
func layoutPlanTab(v *planView, width, height int, now time.Time, st style.Style) planDrawn {
	c := v.content
	if d, ok := v.layoutReading(width, height, now, st); ok {
		return d
	}
	cursor, _ := v.cursorItem()
	top := planHeader(c, width, now, st)
	top = append(top, planAbout(c.Definition, v.unfolded, height < planGoalFrom, width, st)...)

	wide := width >= planPanelFrom
	listColumns, listLines := width-2, height-len(top)-1-2 // the keys, the box's top and bottom
	if wide {
		listColumns = width - 3 - planPanelColumns
	} else {
		listLines -= 1 + planPanelLines
	}
	listColumns, listLines = max(listColumns, 1), max(listLines, 1)
	rows, shown := v.listLines(cursor, listColumns, listLines, now, st)
	listTitle := "Tasks"
	if c.Definition.Plans != nil {
		listTitle = "Plans"
	}

	border := st.Muted
	title := func(name string, columns int) string {
		head := "─ " + name + " "
		if name == "" {
			head = ""
		}
		return border(head) + border(strings.Repeat("─", max(columns-ansi.StringWidth(head), 0)))
	}
	pad := func(s string, columns int) string {
		s = ansi.Truncate(s, columns, "")
		return s + strings.Repeat(" ", max(columns-ansi.StringWidth(s), 0))
	}
	d := planDrawn{items: map[int]string{}}
	lines := top
	if wide {
		panel := v.panel(cursor, true, planPanelColumns-2, len(rows), now, st)
		lines = append(lines, border("┌")+title(listTitle, listColumns)+border("┬")+title(cursor.id, planPanelColumns)+border("┐"))
		for i, r := range rows {
			if id, ok := shown[i]; ok {
				d.items[len(lines)] = id
			}
			side := ""
			if i < len(panel) {
				side = " " + panel[i]
			}
			lines = append(lines, border("│")+pad(r, listColumns)+border("│")+pad(side, planPanelColumns)+border("│"))
		}
		lines = append(lines, border("└"+strings.Repeat("─", listColumns)+"┴"+strings.Repeat("─", planPanelColumns)+"┘"))
	} else {
		panel := v.panel(cursor, false, listColumns-2, planPanelLines, now, st)
		lines = append(lines, border("┌")+title(listTitle, listColumns)+border("┐"))
		for i, r := range rows {
			if id, ok := shown[i]; ok {
				d.items[len(lines)] = id
			}
			lines = append(lines, border("│")+pad(r, listColumns)+border("│"))
		}
		lines = append(lines, border("├")+title(cursor.id, listColumns)+border("┤"))
		for i := range planPanelLines {
			side := ""
			if i < len(panel) {
				side = " " + panel[i]
			}
			lines = append(lines, border("│")+pad(side, listColumns)+border("│"))
		}
		lines = append(lines, border("└"+strings.Repeat("─", listColumns)+"┘"))
	}

	lines = lines[:min(len(lines), max(height-1, 0))]
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	if height > 0 {
		lines = append(lines, ansi.Truncate(planKeys(c, cursor, v.folds(height), v.unfolded, st), width, ""))
	}
	for i := range d.items {
		if i >= len(lines)-1 {
			delete(d.items, i)
		}
	}
	d.lines = lines
	return d
}

// planHeader is the tab's first two lines: the plan's name, its state and
// revision; then its progress, or how many tasks a draft has.
func planHeader(c *api.PlanContent, width int, now time.Time, st style.Style) []string {
	p := c.Plan
	right := planBadge(c, st) + "   rev " + fmt.Sprint(c.Revision)
	name := ansi.Truncate(p.Name, max(width-2-ansi.StringWidth(right)-1, 1), "…")
	gap := strings.Repeat(" ", max(width-1-ansi.StringWidth(name)-ansi.StringWidth(right)-1, 1))
	first := " " + st.Name(name) + gap + right

	items, noun := planItems(c), "task"
	if c.Definition.Plans != nil {
		noun = "plan"
	}
	if c.Run == nil {
		return []string{first, " " + plural(len(items), noun) + ", not run yet"}
	}
	done, failed := 0, 0
	for _, it := range items {
		switch {
		case it.child != nil && it.child.State == api.PlanStateDone:
			done++
		case it.task == nil:
		case it.task.State == api.PlanTaskStateDone:
			done++
		case it.task.State == api.PlanTaskStateFailed, it.task.State == api.PlanTaskStateLandFailed:
			failed++
		}
	}
	end := now
	if c.Run.FinishedAt != nil {
		end = *c.Run.FinishedAt
	}
	ran := ""
	if !c.Run.StartedAt.IsZero() {
		ran = " · " + minutes(end.Sub(c.Run.StartedAt))
	}
	failures := ""
	if failed > 0 {
		failures = fmt.Sprintf(" · %d failed", failed)
	}
	branch := ""
	if c.Run.Branch != "" { // a plan of plans' plans each have their own
		branch = " · on " + c.Run.Branch
	}
	long := fmt.Sprintf("%d/%d %ss%s%s%s", done, len(items), noun, failures, branch, ran)
	short := fmt.Sprintf("%d/%d%s%s", done, len(items), failures, ran)
	text := long
	room := width - 1 - 2 - ansi.StringWidth(long) - 1
	if room < planBarLeast {
		text = short
		room = width - 1 - 2 - ansi.StringWidth(short) - 1
	}
	if room < planBarLeast {
		return []string{first, " " + text}
	}
	return []string{first, " " + progressBar(done, failed, len(items), min(room, planBarMost), st) + "  " + text}
}

// planBadge is the plan's state as the header shows it.
func planBadge(c *api.PlanContent, st style.Style) string {
	p := c.Plan
	var badge string
	switch p.State {
	case api.PlanStateDraft:
		badge = st.Muted("○ draft")
	case api.PlanStateRunning:
		badge = st.Success("● running")
	case api.PlanStatePaused:
		s := "⏸ paused"
		if pausedClosed(c.Run) {
			s += " (project closed)"
		}
		badge = st.Warning(s)
	case api.PlanStateDone:
		badge = st.Success("✓ done")
	case api.PlanStateCancelled:
		badge = st.Failure("✗ cancelled")
	default:
		badge = string(p.State)
	}
	if isArchived(p) {
		badge += " " + st.Muted("archived")
	}
	return badge
}

// pausedClosed reports whether run is paused because its project closed.
func pausedClosed(run *api.PlanRun) bool {
	return run != nil && run.PausedReason != nil && *run.PausedReason == api.PlanRunPausedReasonProjectClosed
}

// progressBar is width cells of a run's tasks: those done, those failed, the
// rest; each its own glyph, so that it reads the same without color.
func progressBar(done, failed, total, width int, st style.Style) string {
	if total == 0 {
		return st.Muted(strings.Repeat("─", width))
	}
	d := done * width / total
	f := (done+failed)*width/total - d
	return st.Success(strings.Repeat("━", d)) + st.Failure(strings.Repeat("▬", f)) + st.Muted(strings.Repeat("─", width-d-f))
}

// minutes says d in minutes, as 6m or 1h 4m.
func minutes(d time.Duration) string {
	n := max(int(d/time.Minute), 0)
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh %dm", n/60, n%60)
}

// planAbout is the plan's goal, wrapped, and its constraints folded or not; the
// goal folds with them when goalFolded.
func planAbout(d api.PlanDefinition, unfolded, goalFolded bool, width int, st style.Style) []string {
	goal := strings.Join(strings.Fields(d.Goal), " ")
	var goalLines []string
	if goal != "" {
		const label = " Goal  "
		lead := st.Muted(label)
		for _, l := range wrapValue(goal, max(width-len(label), 1)) {
			goalLines = append(goalLines, lead+l)
			lead = strings.Repeat(" ", len(label))
		}
	}
	var lines []string
	if !goalFolded {
		lines = goalLines
	}
	what := ""
	switch n := len(d.Constraints); {
	case goalFolded && goal != "" && n > 0:
		what = "goal and " + plural(n, "constraint")
	case goalFolded && goal != "":
		what = "goal"
	case n > 0:
		what = plural(n, "constraint")
	}
	if what == "" {
		return lines
	}
	if !unfolded {
		return append(lines, " "+st.Muted("▸ ")+what)
	}
	lines = append(lines, " "+st.Muted("▾ ")+what)
	if goalFolded {
		lines = append(lines, goalLines...)
	}
	for _, c := range d.Constraints {
		lead := "   - "
		for _, l := range wrapValue(c, max(width-len(lead), 1)) {
			lines = append(lines, lead+l)
			lead = "     "
		}
	}
	return lines
}

// folds is what → and ← fold in the tab at height: the constraints, or the goal
// alone when there are none; "" for nothing.
func (v *planView) folds(height int) string {
	d := v.content.Definition
	switch {
	case len(d.Constraints) > 0:
		return "constraints"
	case height < planGoalFrom && strings.TrimSpace(d.Goal) != "":
		return "goal"
	}
	return ""
}

// planKeys is the tab's last line: the keys the plan's state takes, then the
// list's and what Enter does on the task under the cursor.
func planKeys(c *api.PlanContent, cursor planItem, folds string, unfolded bool, st style.Style) string {
	p := c.Plan
	k := func(key, what string) string { return st.Command(key) + " " + what }
	var keys []string
	switch {
	case p.State == api.PlanStateDraft && p.Parent != nil:
		keys = []string{st.Muted("runs with " + *p.Parent), k(copyKey, "copy")}
	case p.State == api.PlanStateDraft:
		keys = []string{k("r", "run"), k(copyKey, "copy")}
	case p.State == api.PlanStateRunning:
		keys = []string{k("p", "pause"), k("c", "cancel"), k(copyKey, "copy")}
	case p.State == api.PlanStatePaused && pausedClosed(c.Run):
		keys = []string{k("c", "cancel")}
	case p.State == api.PlanStatePaused:
		keys = []string{k("p", "resume"), k("c", "cancel")}
	case isArchived(p):
		keys = []string{k(archiveKey, "unarchive"), k(copyKey, "copy")}
	default:
		keys = []string{k(archiveKey, "archive"), k(copyKey, "copy")}
	}
	if len(planItems(c)) > 0 {
		keys = append(keys, k("↑↓", "select"), k("Enter", cursor.enterVerb(p)))
	}
	switch {
	case folds != "" && unfolded:
		keys = append(keys, k("←", folds))
	case folds != "":
		keys = append(keys, k("→", folds))
	}
	return " " + strings.Join(keys, "  ")
}

const (
	enterAgent  = "go to its agent"
	enterAnswer = "answer it"
	enterPrompt = "read it"
	enterOpen   = "open it"
)

// enterVerb is what Enter does on it in plan p: open a plan's tab; go to the
// agent of a task running or landing, answer one that failed in a plan going
// on, and show the whole prompt of the others.
func (it planItem) enterVerb(p api.Plan) string {
	t := it.task
	switch {
	case t == nil:
		return enterOpen
	case (t.State == api.PlanTaskStateRunning || t.State == api.PlanTaskStateLanding) && t.Agent != nil:
		return enterAgent
	case taskNeedsYou(p, *t):
		return enterAnswer
	}
	return enterPrompt
}

// needsYou reports whether it waits for the person in plan p: a task failed in
// a plan going on, or a plan waiting for their answer.
func (it planItem) needsYou(p api.Plan) bool {
	if it.child != nil {
		return it.child.Waiting
	}
	return taskNeedsYou(p, *it.task)
}

// taskNeedsYou reports whether t waits for the person: it failed or did not
// land, in a plan going on.
func taskNeedsYou(p api.Plan, t api.PlanTask) bool {
	return goingOn(p.State) && (t.State == api.PlanTaskStateFailed || t.State == api.PlanTaskStateLandFailed)
}

// glyph is it's glyph on the rail, and the style of its state.
func (it planItem) glyph(st style.Style) (string, func(string) string) {
	if it.child != nil {
		return childGlyph(it, st)
	}
	return taskGlyph(*it.task, st)
}

// childGlyph is a plan's glyph in its parent's list, its state's as its badge
// has it: a draft Normal when ready or waiting for the person, Muted while the
// plans before it are not done.
func childGlyph(it planItem, st style.Style) (string, func(string) string) {
	plain := func(s string) string { return s }
	switch it.child.State {
	case api.PlanStateRunning:
		return "●", st.Success
	case api.PlanStatePaused:
		return "⏸", st.Warning
	case api.PlanStateDone:
		return "✓", st.Success
	case api.PlanStateCancelled:
		return "✗", st.Failure
	case api.PlanStateDraft:
		if len(it.before) > 0 && !it.child.Waiting {
			return "○", st.Muted
		}
		return "○", plain
	}
	return "?", plain
}

func taskGlyph(t api.PlanTask, st style.Style) (string, func(string) string) {
	plain := func(s string) string { return s }
	switch t.State {
	case api.PlanTaskStateDone:
		return "✓", st.Success
	case api.PlanTaskStateRunning:
		return "●", st.Success
	case api.PlanTaskStateLanding:
		return "◐", st.Success
	case api.PlanTaskStateFailed:
		return "✗", st.Failure
	case api.PlanTaskStateLandFailed:
		return "⚠", st.Failure
	case api.PlanTaskStateSkipped:
		return "⊘", st.Muted
	case api.PlanTaskStatePending:
		if len(t.WaitingFor) > 0 {
			return "○", st.Muted
		}
		return "○", plain
	}
	return "?", plain
}

// words is it's state in words, a task's with why it failed when reason says
// so: a plan waits for the person, or for the plans before it.
func (it planItem) words(reason bool) string {
	if c := it.child; c != nil {
		switch {
		case c.Waiting:
			return "waiting for you"
		case c.State == api.PlanStateDraft && len(it.before) > 0:
			return "waiting"
		case c.State == api.PlanStateDraft:
			return "ready"
		}
		return string(c.State)
	}
	return taskWords(*it.task, reason)
}

func taskWords(t api.PlanTask, reason bool) string {
	var s string
	switch t.State {
	case api.PlanTaskStatePending:
		s = "ready"
		if len(t.WaitingFor) > 0 {
			s = "waiting"
		}
	case api.PlanTaskStateLandFailed:
		s = "land failed"
	default:
		s = string(t.State)
	}
	if reason && t.Failure != nil {
		s += ": " + strings.ReplaceAll(string(*t.Failure), "_", " ")
	}
	return s
}

// taskTime is how long t ran, or has run so far, in minutes; — before it starts.
func taskTime(t api.PlanTask, now time.Time) string {
	if t.StartedAt == nil {
		return valueEmpty
	}
	end := now
	if t.FinishedAt != nil {
		end = *t.FinishedAt
	}
	return minutes(end.Sub(*t.StartedAt))
}

// time is a task's time, or how far a plan is in place of it: 2/4, or how
// many tasks it has before it runs.
func (it planItem) time(now time.Time) string {
	if c := it.child; c != nil {
		if c.TasksDone != nil {
			return fmt.Sprintf("%d/%d", *c.TasksDone, c.Tasks)
		}
		return plural(int(c.Tasks), "task")
	}
	return taskTime(*it.task, now)
}

// agent is a task's agent's name; a plan has none.
func (it planItem) agent() string {
	if it.task == nil {
		return ""
	}
	return taskAgent(*it.task)
}

// taskColumns are the columns of the list a width keeps, dropped from the
// right as it narrows: the agent, then the time, then the failure's reason. A
// plan not run has neither agents nor times; a plan of plans has no agents,
// and its plans' progress in place of the time.
type taskColumns struct {
	id, words, agent, time int // their widths; 0 for one dropped
	reason                 bool
}

func fitColumns(items []planItem, ran bool, room int, now time.Time) taskColumns {
	var c taskColumns
	short := 0
	plans := false
	for _, it := range items {
		c.id = max(c.id, ansi.StringWidth(it.id))
		c.words = max(c.words, ansi.StringWidth(it.words(true)))
		short = max(short, ansi.StringWidth(it.words(false)))
		c.agent = max(c.agent, ansi.StringWidth(it.agent()))
		c.time = max(c.time, ansi.StringWidth(it.time(now)))
		plans = plans || it.child != nil
	}
	c.reason = true
	if !ran && !plans {
		c.agent, c.time = 0, 0
	}
	need := func() int {
		n := c.id + 1 + c.words
		if c.agent > 0 {
			n += 1 + c.agent
		}
		if c.time > 0 {
			n += 1 + c.time
		}
		return n
	}
	if need() > room {
		c.agent = 0
	}
	if need() > room {
		c.time = 0
	}
	if need() > room {
		c.words, c.reason = short, false
	}
	return c
}

func taskAgent(t api.PlanTask) string {
	if t.Agent == nil {
		return valueEmpty
	}
	return t.Agent.Name
}

// itemMark is what the row of it says at its end while cursor is under the
// cursor: that cursor needs it or it waits for cursor, or else that it needs
// the person.
func itemMark(p api.Plan, it, cursor planItem, st style.Style) string {
	switch {
	case it.id == cursor.id:
	case slices.Contains(cursor.after, it.id):
		return st.Focused(planMarkNeeds)
	case slices.Contains(it.after, cursor.id):
		return st.Focused(planMarkUnblocks)
	}
	if it.needsYou(p) {
		return st.Warning(planMarkNeedsYou)
	}
	return ""
}

// listLines are the lines of the list, columns × at most lines: the rows of the
// rail scrolled to keep the cursor in view when asked, with what is out of
// sight said above and below; and the task on each line.
func (v *planView) listLines(cursor planItem, columns, lines int, now time.Time, st style.Style) ([]string, map[int]string) {
	c := v.content
	items := planItems(c)
	if len(items) == 0 {
		none := "no tasks"
		if c.Definition.Plans != nil {
			none = "no plans"
		}
		return []string{" " + st.Muted(none)}, nil
	}
	rail, drawn := v.railOf()
	byID := map[string]planItem{}
	for _, it := range items {
		byID[it.id] = it
	}
	railColumns := 2 // a plain list's glyph and a space
	if drawn {
		railColumns = len(rail[0].cells)
	}
	room := columns - 1 - railColumns - planRailGap - 1 - planMarkColumns - 1 // the edge, the rail, the mark and a cell after it
	cols := fitColumns(items, c.Run != nil, room, now)

	at := slices.IndexFunc(rail, func(r railRow) bool { return r.task == cursor.id })
	above, below, body := v.scroll(rail, at, lines)

	var out []string
	shown := map[int]string{}
	if above > 0 {
		out = append(out, " "+st.Muted(fmt.Sprintf("▴ %d more", above)))
	}
	for _, r := range rail[v.offset : v.offset+body] {
		if r.task == "" {
			out = append(out, " "+railCells(r, cursor.id, "", st))
			continue
		}
		it := byID[r.task]
		glyph, paint := it.glyph(st)
		row := paint(glyph) + " "
		if drawn {
			row = railCells(r, cursor.id, paint(glyph), st)
		}
		row += strings.Repeat(" ", planRailGap)
		cells := []string{padTo(it.id, cols.id), paint(padTo(it.words(cols.reason), cols.words))}
		if cols.agent > 0 {
			cells = append(cells, padTo(it.agent(), cols.agent))
		}
		if cols.time > 0 {
			cells = append(cells, padTo(it.time(now), cols.time))
		}
		text := ansi.Truncate(strings.Join(cells, " "), max(room, 0), "…")
		row += text + strings.Repeat(" ", max(room-ansi.StringWidth(text), 0)) + " "
		mark := itemMark(c.Plan, it, cursor, st)
		row += strings.Repeat(" ", max(planMarkColumns-ansi.StringWidth(mark), 0)) + mark
		row = ansi.Truncate(row+" ", columns-1, "")
		edge := " "
		if it.id == cursor.id {
			edge = st.Focused("▌")
			row = st.Selected(row + strings.Repeat(" ", max(columns-1-ansi.StringWidth(row), 0)))
		}
		shown[len(out)] = it.id
		out = append(out, edge+row)
	}
	if below > 0 {
		out = append(out, " "+st.Muted(fmt.Sprintf("▾ %d more", below)))
	}
	for len(out) < lines {
		out = append(out, "")
	}
	return out, shown
}

// scroll keeps v's offset in range for the rail in lines, and the cursor's row
// at in view when the cursor moved; it returns how many tasks are out of sight
// above and below, and how many rows show.
func (v *planView) scroll(rail []railRow, at, lines int) (above, below, body int) {
	tasksIn := func(rows []railRow) int {
		n := 0
		for _, r := range rows {
			if r.task != "" {
				n++
			}
		}
		return n
	}
	// A line saying what is out of sight takes a row's room: settle the two
	// together, as each depends on the other.
	for range 3 {
		body = lines
		if v.offset > 0 {
			body--
		}
		if v.offset+body < len(rail) {
			body--
		}
		body = max(body, 1)
		if v.reveal && at >= 0 {
			v.offset = min(v.offset, at)
			v.offset = max(v.offset, at-body+1)
		}
		v.offset = max(min(v.offset, len(rail)-body), 0)
	}
	v.reveal = false
	end := min(v.offset+body, len(rail))
	body = end - v.offset
	return tasksIn(rail[:v.offset]), tasksIn(rail[end:]), body
}

// railCells draws the rail of r: the glyph in the node's cell, the edges of the
// task under the cursor in the accent style and the others muted.
func railCells(r railRow, cursor, glyph string, st style.Style) string {
	var b strings.Builder
	for _, c := range r.cells {
		switch {
		case c.node:
			b.WriteString(glyph)
		case c.r == ' ':
			b.WriteByte(' ')
		case slices.ContainsFunc(c.edges, func(e railEdge) bool { return e.from == cursor || e.to == cursor }):
			b.WriteString(st.Focused(string(c.r)))
		default:
			b.WriteString(st.Muted(string(c.r)))
		}
	}
	return b.String()
}

func padTo(s string, width int) string {
	s = ansi.Truncate(s, width, "…")
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// planTabKey takes a key of the tab of v: ↑ ↓ move to the row before or
// after, → ← unfold and fold the constraints, Enter goes to a running task's
// agent, answers a failed one, shows the whole prompt of the others, or opens
// a plan's tab. While a prompt is shown whole, its keys are the reading's.
func (m *Model) planTabKey(v *planView, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if v.reading != "" {
		m.readingKey(v, k.String())
		return nil
	}
	switch name := k.String(); {
	case slices.Contains(upKeys, name):
		v.step(-1)
	case slices.Contains(downKeys, name):
		v.step(1)
	case name == "right" || name == "left":
		if unfold := name == "right"; v.unfolded != unfold {
			v.unfolded = unfold
			m.log.Info("plan constraints folded", "tab", m.list().active, "folded", !unfold)
		}
	case name == "enter":
		it, ok := v.cursorItem()
		if !ok {
			return nil
		}
		switch it.enterVerb(v.content.Plan) {
		case enterOpen:
			return m.openChild(it.id)()
		case enterAgent:
			return m.goToTaskAgent(it.task.Agent.Id)()
		case enterAnswer:
			return m.askTask(v.content.Plan, *it.task)()
		case enterPrompt:
			v.reading, v.readOffset = it.id, 0
			m.log.Info("plan task prompt shown", "tab", m.list().active, "task", it.id)
		}
	}
	return nil
}

// step moves the cursor to the row by rows on, past the rail's connectors.
func (v *planView) step(by int) {
	items := planItems(v.content)
	cur, ok := v.cursorItem()
	if !ok {
		return
	}
	i := slices.IndexFunc(items, func(it planItem) bool { return it.id == cur.id }) + by
	if i >= 0 && i < len(items) {
		v.cursor, v.reveal = items[i].id, true
	}
}

// planTabClick takes a click on the row of the tab's area: on a row of the
// list the cursor goes to it, doing nothing more.
func (m *Model) planTabClick(v *planView, row int, area uv.Rectangle) {
	d := layoutPlanTab(v, area.Dx(), area.Dy(), m.now(), m.st)
	if id, ok := d.items[row]; ok {
		v.cursor = id
	}
}

// planTabWheel scrolls the list, or the prompt shown whole, by a turn of the
// wheel; the next draw keeps it in range.
func planTabWheel(v *planView, msg tea.MouseWheelMsg) {
	if v.reading != "" {
		switch msg.Button {
		case tea.MouseWheelUp:
			v.readOffset = max(v.readOffset-valueWheelStep, 0)
		case tea.MouseWheelDown:
			v.readOffset += valueWheelStep
		}
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		v.offset = max(v.offset-valueWheelStep, 0)
	case tea.MouseWheelDown:
		v.offset += valueWheelStep
	}
}

// planMinute redraws the times of a plan's running tasks, which show minutes.
type planMinute struct{ projectID, tab string }

// planTimer asks for a planMinute while the tab shown has a task running or
// landing, one at a time.
func (m *Model) planTimer() tea.Cmd {
	v, ok := m.planTabView()
	if !ok || v.timing || !slices.ContainsFunc(planTasks(v.content), func(t api.PlanTask) bool {
		return t.State == api.PlanTaskStateRunning || t.State == api.PlanTaskStateLanding
	}) {
		return nil
	}
	v.timing = true
	msg := planMinute{projectID: m.following.project.Id, tab: m.list().active}
	return m.tick(time.Minute, func(time.Time) tea.Msg { return msg })
}

func (m *Model) planMinuteMsg(msg planMinute) {
	if l, ok := m.agentLists[msg.projectID]; ok && l.planViews[msg.tab] != nil {
		l.planViews[msg.tab].timing = false
	}
}
