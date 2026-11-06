package tide

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The panel of a plan's tab, saying all of the row under the cursor; and a
// task's whole prompt and summary, which Enter shows in place of the tab
// (docs/PLANS.md, Step 8).

// panelBlock is a text of the panel that takes the lines left: a task's prompt
// and summary, a plan's goal.
type panelBlock struct {
	title string
	text  string
}

// panelParts are what the panel says of a row: its state and facts, the rows
// it needs and unblocks, its texts, and what is said at its end.
type panelParts struct {
	status   string
	facts    []string
	needs    []string
	unblocks []unblocked
	blocks   []panelBlock
	why      string // why a plan waits
	enter    string
}

// unblocked is a row waiting for the panel's, and the others it waits for.
type unblocked struct {
	name, also string
}

// panel is the panel of the row it, columns × at most lines: at the right of
// the list when wide, a section a line, or under it in a line each.
func (v *planView) panel(it planItem, wide bool, columns, lines int, now time.Time, st style.Style) []string {
	if it.id == "" || columns < 1 || lines < 1 {
		return nil
	}
	p := v.parts(it, now, st)
	if wide {
		return panelWide(p, columns, lines, st)
	}
	return panelNarrow(p, columns, lines, st)
}

// parts are what the panel of it says.
func (v *planView) parts(it planItem, now time.Time, st style.Style) panelParts {
	c := v.content
	items := planItemsMoved(c, v.movedOn)
	byID := map[string]planItem{}
	for _, o := range items {
		byID[o.id] = o
	}
	named := func(o planItem) string {
		glyph, paint := o.glyph(st)
		return paint(glyph) + " " + o.id
	}
	glyph, paint := it.glyph(st)
	var p panelParts
	p.status = paint(glyph + " " + it.words(true))
	if t := it.task; t != nil && t.StartedAt != nil {
		p.status += " · " + taskTime(*t, now)
	}
	if ch := it.child; ch != nil {
		p.status += " · " + it.time(now)
	}
	for _, a := range it.after {
		if o, ok := byID[a]; ok {
			p.needs = append(p.needs, named(o))
		}
	}
	for _, o := range items {
		if !slices.Contains(o.after, it.id) {
			continue
		}
		waits := o.before
		if o.task != nil {
			waits = o.task.WaitingFor
		}
		u := unblocked{name: named(o)}
		if others := slices.DeleteFunc(slices.Clone(waits), func(id string) bool { return id == it.id }); len(others) > 0 {
			u.also = "also after " + strings.Join(others, ", ")
		}
		p.unblocks = append(p.unblocks, u)
	}
	p.enter = st.Command("Enter") + " " + it.enterVerb(c.Plan)

	if t := it.task; t != nil {
		if t.Agent != nil {
			p.facts = append(p.facts, t.Agent.Name)
		}
		if t.SlotPath != nil {
			p.facts = append(p.facts, "slot "+filepath.Base(*t.SlotPath))
		}
		if t.CheckTerminal != nil {
			p.facts = append(p.facts, "terminal "+*t.CheckTerminal)
		}
		if t.Conflicts != nil && len(*t.Conflicts) > 0 {
			p.facts = append(p.facts, "conflicts "+strings.Join(*t.Conflicts, ", "))
		}
		if strings.TrimSpace(t.Prompt) != "" {
			p.blocks = append(p.blocks, panelBlock{title: "Prompt", text: t.Prompt})
		}
		if t.Summary != nil && strings.TrimSpace(*t.Summary) != "" {
			p.blocks = append(p.blocks, panelBlock{title: "Summary", text: *t.Summary})
		}
		return p
	}
	goal := "loading…"
	if g := v.goals[it.id]; g != nil && g.err != "" {
		goal = g.err
	} else if g != nil && !g.loading {
		goal = g.goal
	}
	p.blocks = append(p.blocks, panelBlock{title: "Goal", text: goal})
	switch ch := it.child; {
	case ch.Waiting && len(ch.After) > 0:
		p.why = st.Command("r") + " starts it, once " + andList(ch.After) + " " + isAre(len(ch.After)) + " in the default branch"
	case ch.Waiting:
		p.why = st.Command("r") + " starts it"
	case ch.State == api.PlanStateDraft && len(it.before) > 0 && goingOn(c.Plan.State):
		p.why = st.Muted("it starts once " + andList(it.before) + " " + isAre(len(it.before)) + " done")
	}
	return p
}

// andList says names as a, b and c.
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// panelWide lays out p a section after another, its texts sharing the lines
// the rest leaves and what Enter does on the last line.
func panelWide(p panelParts, columns, lines int, st style.Style) []string {
	head := []string{p.status}
	if len(p.facts) > 0 {
		head = append(head, wrapWords(strings.Join(p.facts, " · "), columns)...)
	}
	if len(p.needs) > 0 || len(p.unblocks) > 0 {
		head = append(head, "")
	}
	if len(p.needs) > 0 {
		head = append(head, st.Muted("Needs"))
		for _, n := range p.needs {
			head = append(head, "  "+n)
		}
	}
	if len(p.unblocks) > 0 {
		head = append(head, st.Muted("Unblocks"))
		for _, u := range p.unblocks {
			if u.also != "" {
				u.name += st.Muted(", " + u.also)
			}
			head = append(head, "  "+u.name)
		}
	}
	tail := []string{""}
	if p.why != "" {
		tail = append(tail, wrapWords(p.why, columns)...)
	}
	tail = append(tail, p.enter)

	// Each text shown takes a blank line and its title besides its own.
	blocks := p.blocks
	var shown [][]string
	for len(blocks) > 0 {
		room := lines - len(head) - len(tail) - 2*len(blocks)
		if room >= len(blocks) {
			shown = fitBlocks(blocks, columns-2, room)
			break
		}
		blocks = blocks[:len(blocks)-1]
	}
	out := head
	for i, b := range shown {
		out = append(out, "", st.Muted(blocks[i].title))
		for _, l := range b {
			out = append(out, "  "+l)
		}
	}
	return keepTail(append(out, tail...), len(tail), lines)
}

// panelNarrow lays out p a line each: its state and facts, its dependencies,
// its texts in the lines left, and what Enter does.
func panelNarrow(p panelParts, columns, lines int, st style.Style) []string {
	head := []string{strings.Join(append([]string{p.status}, p.facts...), " · ")}
	var deps []string
	if len(p.needs) > 0 {
		deps = append(deps, st.Muted("Needs ")+strings.Join(p.needs, ", "))
	}
	if len(p.unblocks) > 0 {
		var names []string
		for _, u := range p.unblocks {
			if u.also != "" {
				u.name += st.Muted(" (" + u.also + ")")
			}
			names = append(names, u.name)
		}
		deps = append(deps, st.Muted("Unblocks ")+strings.Join(names, ", "))
	}
	if len(deps) > 0 {
		head = append(head, strings.Join(deps, " · "))
	}
	var tail []string
	if p.why != "" {
		tail = append(tail, p.why)
	}
	tail = append(tail, p.enter)
	blocks := slices.Clone(p.blocks)
	for i := range blocks {
		if i > 0 || blocks[i].title != "Prompt" { // the prompt goes without its title, as the room is short
			blocks[i].text = blocks[i].title + ": " + blocks[i].text
		}
	}
	room := lines - len(head) - len(tail)
	out := head
	if room > 0 {
		for _, b := range fitBlocks(blocks, columns, room) {
			out = append(out, b...)
		}
	}
	return keepTail(append(out, tail...), len(tail), lines)
}

// keepTail cuts lines to at most n, keeping the last tail of them.
func keepTail(lines []string, tail, n int) []string {
	if len(lines) <= n {
		return lines
	}
	if tail >= n {
		return lines[len(lines)-n:]
	}
	return append(lines[:n-tail:n-tail], lines[len(lines)-tail:]...)
}

// fitBlocks wraps each block at columns and shares room lines between them,
// the first first; a block cut short ends with …+N, the lines left out.
func fitBlocks(blocks []panelBlock, columns, room int) [][]string {
	wrapped := make([][]string, len(blocks))
	counts := make([]int, len(blocks))
	var open []int
	for i, b := range blocks {
		wrapped[i] = wrapWords(b.text, columns)
		open = append(open, i)
	}
	for left := room; left > 0 && len(open) > 0; {
		share := max(left/len(open), 1)
		var next []int
		for _, i := range open {
			give := min(len(wrapped[i])-counts[i], share, left)
			counts[i] += give
			left -= give
			if counts[i] < len(wrapped[i]) {
				next = append(next, i)
			}
		}
		open = next
	}
	out := make([][]string, len(blocks))
	for i, w := range wrapped {
		out[i] = w[:counts[i]]
		if hidden := len(w) - counts[i]; hidden > 0 && counts[i] > 0 {
			more := fmt.Sprintf(" …+%d", hidden)
			last := counts[i] - 1
			out[i] = append(slices.Clone(out[i][:last]), wrapWords(w[last], max(columns-ansi.StringWidth(more), 1))[0]+more)
		}
	}
	return out
}

// wrapWords wraps s at columns, at its words where it can, keeping its lines.
func wrapWords(s string, columns int) []string {
	s = strings.TrimRight(ansi.Strip(s), "\n ")
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, strings.Split(ansi.Wrap(strings.TrimRight(l, " "), max(columns, 1), ""), "\n")...)
	}
	return out
}

// layoutReading lays out the whole prompt and summary of the task v shows,
// filling the tab, when it shows one: its id and state on the first line, the
// keys on the last.
func (v *planView) layoutReading(width, height int, now time.Time, st style.Style) (planDrawn, bool) {
	if v.reading == "" {
		return planDrawn{}, false
	}
	items := planItems(v.content)
	i := slices.IndexFunc(items, func(it planItem) bool { return it.id == v.reading && it.task != nil })
	if i < 0 {
		v.reading = "" // gone in a new revision
		return planDrawn{}, false
	}
	it := items[i]
	p := v.parts(it, now, st)
	title := " " + st.Name(it.id) + "  " + strings.Join(append([]string{p.status}, p.facts...), " · ")
	var body []string
	for _, b := range p.blocks {
		body = append(body, "", " "+st.Muted(b.title))
		for _, l := range wrapWords(b.text, width-3) {
			body = append(body, "   "+l)
		}
	}
	room := max(height-2, 0) // the title and the keys
	v.readOffset = max(min(v.readOffset, len(body)-room), 0)
	end := min(v.readOffset+room, len(body))
	lines := append([]string{title}, body[v.readOffset:end]...)
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	keys := " " + st.Command("Esc") + " back"
	if len(body) > room {
		keys += "  " + st.Command("↑↓") + " scroll"
	}
	if height > 0 {
		lines = append(lines[:height-1], keys)
	}
	for j, l := range lines {
		lines[j] = ansi.Truncate(l, width, "")
	}
	return planDrawn{lines: lines, items: map[int]string{}}, true
}

// readingKey takes a key while a prompt is shown whole: ↑ ↓ and the pages
// scroll it, Esc goes back to the list.
func (m *Model) readingKey(v *planView, name string) {
	switch {
	case name == "esc":
		m.log.Info("plan task prompt closed", "tab", m.list().active, "task", v.reading)
		v.reading = ""
	case slices.Contains(upKeys, name):
		v.readOffset = max(v.readOffset-1, 0)
	case slices.Contains(downKeys, name):
		v.readOffset++
	case name == "pgup":
		v.readOffset = max(v.readOffset-valueWheelStep*3, 0)
	case name == "pgdown":
		v.readOffset += valueWheelStep * 3
	}
}

// childGoal is the goal of a plan a plan of plans lists, read at a revision of
// it, as the parent's content does not carry it.
type childGoal struct {
	revision  int64
	goal, err string
	loading   bool
}

// planGoalLoaded is the answer to reading the goal of the plan named name.
type planGoalLoaded struct {
	projectID, tab, name string
	revision             int64
	goal                 string
	err                  error
}

// planGoalRead reads the goal of the plan under the cursor of the plan of plans
// shown, when it has not been read at its latest revision.
func (m *Model) planGoalRead() tea.Cmd {
	v, ok := m.planTabView()
	if !ok || v.content.Definition.Plans == nil || m.client == nil {
		return nil
	}
	it, ok := v.cursorItem()
	if !ok {
		return nil
	}
	p, ok := m.planNamed(it.id)
	if g := v.goals[it.id]; !ok || (g != nil && (g.loading || g.revision == p.Revision)) {
		return nil
	}
	if v.goals == nil {
		v.goals = map[string]*childGoal{}
	}
	v.goals[it.id] = &childGoal{revision: p.Revision, loading: true}
	projectID, tab := m.following.project.Id, m.list().active
	return func() tea.Msg {
		c, err := m.client.GetPlan(m.ctx, "id:"+p.Id, "id:"+projectID, "", p.Revision)
		return planGoalLoaded{projectID: projectID, tab: tab, name: it.id, revision: p.Revision, goal: c.Definition.Goal, err: err}
	}
}

func (m *Model) planGoalLoadedMsg(msg planGoalLoaded) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.planViews[msg.tab] == nil {
		return
	}
	v := l.planViews[msg.tab]
	if v.goals == nil {
		v.goals = map[string]*childGoal{}
	}
	g := &childGoal{revision: msg.revision, goal: strings.Join(strings.Fields(msg.goal), " ")}
	if msg.err != nil {
		m.log.Info("plan goal not read", "tab", msg.tab, "name", msg.name, "err", msg.err)
		g.err = msg.err.Error()
	}
	v.goals[msg.name] = g
}
