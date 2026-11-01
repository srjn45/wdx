package tide

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// tabsModel returns a window 100 wide with n running agents tab-0, tab-1, ...,
// each with a tab open, the first current and the agent pane in focus. The pane
// has 67 cells for tabs, which are 12 wide.
func tabsModel(n int) (*Model, []api.Agent) {
	var agents []api.Agent
	for i := range n {
		agents = append(agents, running(fmt.Sprintf("tab-%d", i)))
	}
	m := newAgentsModel(100, 41, agents...)
	for _, a := range agents {
		m.list().openTab("agent-" + a.Name)
	}
	if n > 0 {
		m.list().active = "agent-tab-0"
	}
	m.Update(key(tea.KeyRight, tea.ModAlt))
	return m, agents
}

func titleRow(m *Model) string { return strings.SplitN(text(m), "\n", 2)[0] }

func wantTitle(t *testing.T, m *Model, want string) {
	t.Helper()
	if got := titleRow(m); !strings.HasSuffix(got, want) {
		t.Fatalf("title %q, want it to end %q", got, want)
	}
}

func wantCurrent(t *testing.T, m *Model, want string) {
	t.Helper()
	if a, ok := m.activeAgent(); !ok || a.Name != want {
		t.Fatalf("current tab %q, %v; want %s", a.Name, ok, want)
	}
}

func TestFitTabs(t *testing.T) {
	t.Parallel()
	same := func(n, width int) []int { return slices.Repeat([]int{width}, n) }
	for _, tc := range []struct {
		name                string
		widths              []int
		current, from, room int
		first, last         int
	}{
		{"all fit", same(3, 10), 0, 0, 32, 0, 2},
		{"all fit exactly", same(3, 10), 2, 0, 32, 0, 2},
		{"as many as fit, with the arrow", same(6, 10), 0, 0, 37, 0, 2},
		{"the arrow's room is counted", same(6, 10), 0, 0, 34, 0, 2},
		{"a cell short of the arrow", same(6, 10), 0, 0, 33, 0, 1},
		{"sliding right, by as little as it must", same(6, 10), 3, 0, 37, 1, 3},
		{"a tab shown moves nothing", same(6, 10), 2, 1, 37, 1, 3},
		{"sliding left, by as little as it must", same(6, 10), 0, 1, 37, 0, 2},
		{"at the end, no right arrow", same(6, 10), 5, 3, 37, 3, 5},
		{"the room at the end takes tabs back", same(6, 10), 5, 5, 37, 3, 5},
		{"names of other widths", []int{20, 5, 5, 20, 5}, 3, 0, 32, 2, 3},
		{"one too wide stays, alone", []int{10, 50, 10}, 1, 0, 20, 1, 1},
		{"no room stays on the current", same(3, 10), 1, 0, 0, 1, 1},
	} {
		first, last := fitTabs(tc.widths, tc.current, tc.from, tc.room)
		if first != tc.first || last != tc.last {
			t.Errorf("%s: got %d-%d, want %d-%d", tc.name, first, last, tc.first, tc.last)
		}
	}
}

// TestFitTabsInvariants moves at random among tabs of random widths, resizing
// now and then, and checks what must always hold.
func TestFitTabsInvariants(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 4))
	used := func(widths []int, first, last int) int {
		cells := last - first
		for _, w := range widths[first : last+1] {
			cells += w
		}
		if first > 0 {
			cells += tabArrow
		}
		if last < len(widths)-1 {
			cells += tabArrow
		}
		return cells
	}
	for run := range 100 {
		widths := make([]int, 1+rng.IntN(15))
		for i := range widths {
			widths[i] = tabFrame + 1 + rng.IntN(20)
		}
		room := 1 + rng.IntN(120)
		first, last, current := 0, 0, 0
		for step := range 100 {
			moved := false
			if rng.IntN(10) == 0 {
				room, moved = 1+rng.IntN(120), true
			} else {
				current = rng.IntN(len(widths))
			}
			was := current >= first && current <= last
			wasFirst := first
			first, last = fitTabs(widths, current, first, room)
			where := fmt.Sprintf("run %d step %d: widths %v room %d current %d: %d-%d", run, step, widths, room, current, first, last)
			switch {
			case current < first || current > last:
				t.Fatalf("%s: the current tab is not shown", where)
			case first != last && used(widths, first, last) > room:
				t.Fatalf("%s: past the room", where)
			case first == last && first != current:
				t.Fatalf("%s: one tab shown, not the current", where)
			case last < len(widths)-1 && used(widths, first, last+1) <= room:
				t.Fatalf("%s: the next tab to the right would fit", where)
			case first > 0 && used(widths, first-1, last) <= room:
				t.Fatalf("%s: the next tab to the left would fit", where)
			case !moved && was && first != wasFirst:
				t.Fatalf("%s: moved from %d to a tab shown", where, wasFirst)
			}
		}
	}
}

// TestTabStrip follows the drawing in docs/TIDE_TABS.md: whole tabs, an arrow on
// a side with tabs hidden, the line up to the zoom button, moving only as it must.
func TestTabStrip(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(7)
	const (
		tab0 = "[● tab-0(-)]"
		tab1 = "[● tab-1(-)]"
		tab2 = "[● tab-2(-)]"
		tab3 = "[● tab-3(-)]"
		tab4 = "[● tab-4(-)]"
		tab5 = "[● tab-5(-)]"
		tab6 = "[● tab-6(-)]"
	)
	row := func(parts ...string) string {
		s := "┬─" + strings.Join(parts, "─") + "─"
		return s + strings.Repeat("─", 70-ansi.StringWidth(s)) + "[⤢]─┐"
	}
	wantTitle(t, m, row(tab0, tab1, tab2, tab3, tab4, "→"))

	for range 4 {
		m.Update(key(tea.KeyRight, tea.ModAlt))
	}
	wantCurrent(t, m, "tab-4")
	wantTitle(t, m, row(tab0, tab1, tab2, tab3, tab4, "→"))

	// The tabs before it go, and the room left takes the last one.
	m.Update(key(tea.KeyRight, tea.ModAlt))
	wantCurrent(t, m, "tab-5")
	wantTitle(t, m, row("←", tab2, tab3, tab4, tab5, tab6))

	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key(tea.KeyRight, tea.ModAlt)) // nothing past the last
	wantCurrent(t, m, "tab-6")
	wantTitle(t, m, row("←", tab2, tab3, tab4, tab5, tab6))

	// Back through the tabs shown, nothing moves, until the one hidden.
	for range 4 {
		m.Update(key(tea.KeyLeft, tea.ModAlt))
	}
	wantCurrent(t, m, "tab-2")
	wantTitle(t, m, row("←", tab2, tab3, tab4, tab5, tab6))
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	wantCurrent(t, m, "tab-1")
	wantTitle(t, m, row(tab0, tab1, tab2, tab3, tab4, "→"))
}

func TestTabStripNoTabs(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(0)
	if got := titleRow(m); !strings.Contains(got, " agent ─") {
		t.Fatalf("title %q", got)
	}
}

// One tab too wide for the strip is cut, keeping its mark and button.
func TestTabStripLongName(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(40, 41, running(strings.Repeat("a", 60)))
	got := titleRow(m)
	if !strings.Contains(got, "[● aaa") || !strings.Contains(got, "…(-)]─") {
		t.Fatalf("title %q", got)
	}
	if !strings.HasSuffix(got, "[⤢]─┐") {
		t.Fatalf("past the zoom button: %q", got)
	}
}

// The current tab is bold and the others muted; the mark keeps its color.
func TestTabCurrentBold(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(2)
	m.st = style.New(true)
	frames, ok := m.frames()
	if !ok {
		t.Fatal("no frames")
	}
	var got strings.Builder
	for _, l := range m.labels(agent, frames[agent]) {
		got.WriteString(l.styled)
	}
	for _, want := range []string{m.st.Name(" tab-0"), m.st.Muted(" tab-1"), m.st.Name("(-)"), m.st.Muted("(-)"), m.st.Success("●")} {
		if !strings.Contains(got.String(), want) {
			t.Fatalf("no %q in %q", want, got.String())
		}
	}
}

func TestTabMark(t *testing.T) {
	t.Parallel()
	waiting := api.AgentActivityWaiting
	m := newAgentsModel(100, 41, api.Agent{Name: "a", State: api.AgentStateRunning, Activity: &waiting}, exited("b", 0))
	m.list().openTab("agent-b")
	if got := titleRow(m); !strings.Contains(got, "[? a(-)]─[○ b(-)]") {
		t.Fatalf("title %q", got)
	}
}

func TestTabKeys(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(3)

	m.Update(key(tea.KeyRight, tea.ModAlt))
	wantCurrent(t, m, "tab-1")

	// Alt+↑ goes to the list from any tab, keeping it; Alt+→ comes back to it.
	m.Update(key(tea.KeyUp, tea.ModAlt))
	if m.layout.focus != project {
		t.Fatalf("Alt+↑: focus %s", m.layout.focus)
	}
	wantCurrent(t, m, "tab-1")
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if m.layout.focus != agent {
		t.Fatalf("Alt+→ from the list: focus %s", m.layout.focus)
	}
	wantCurrent(t, m, "tab-1")

	// Alt+← walks to the first tab, then to the list.
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	wantCurrent(t, m, "tab-0")
	if m.layout.focus != agent {
		t.Fatalf("focus %s on the first tab", m.layout.focus)
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.layout.focus != project {
		t.Fatalf("Alt+← on the first tab: focus %s", m.layout.focus)
	}

	// Zoomed, the zoom follows the focus.
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key(']', tea.ModCtrl))
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.layout.focus != project || m.layout.zoomed != project {
		t.Fatalf("zoomed: %+v", m.layout)
	}

	// With no tabs, Alt+← still reaches the list, and Alt+→ does nothing.
	m, _ = tabsModel(0)
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if m.layout.focus != agent {
		t.Fatalf("no tabs, Alt+→: focus %s", m.layout.focus)
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.layout.focus != project {
		t.Fatalf("no tabs, Alt+←: focus %s", m.layout.focus)
	}
}

func TestTabOpen(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	if !slices.Equal(m.list().tabs, []string{"agent-swift-falcon"}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}

	// Enter opens a tab at the right end, leaving the focus in the list.
	m.Update(key('k', 0))
	m.Update(keyMsgs("enter")[0])
	if !slices.Equal(m.list().tabs, []string{"agent-swift-falcon", "agent-calm-otter"}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}
	wantCurrent(t, m, "calm-otter")
	if m.layout.focus != project {
		t.Fatalf("focus %s", m.layout.focus)
	}

	// On one open it goes there, opening nothing.
	m.Update(key('j', 0))
	m.Update(keyMsgs("enter")[0])
	wantCurrent(t, m, "swift-falcon")
	if len(m.list().tabs) != 2 {
		t.Fatalf("tabs %v", m.list().tabs)
	}

	// An agent another created opens no tab.
	sentAgents(m, append(demoAgents(), running("zany-yak"))...)
	if len(m.list().tabs) != 2 {
		t.Fatalf("tabs %v", m.list().tabs)
	}

	// One created here does.
	m.Update(agentCreated{projectID: "proj-demo", agent: api.Agent{Id: "agent-new", Name: "new"}})
	sentAgents(m, append(demoAgents(), running("zany-yak"), running("new"))...)
	wantCurrent(t, m, "new")
	if len(m.list().tabs) != 3 {
		t.Fatalf("tabs %v", m.list().tabs)
	}
}

func TestTabCloseKey(t *testing.T) {
	t.Parallel()
	m, agents := tabsModel(3)
	m.Update(key(tea.KeyRight, tea.ModAlt))

	m.Update(key('q', tea.ModCtrl))
	wantText(t, m, "-  close the tab of tab-1")
	m.Update(key('-', 0))

	// The tab to its right takes over, and the agent stays.
	wantCurrent(t, m, "tab-2")
	if len(m.agents()) != len(agents) {
		t.Fatalf("agents %v", m.agents())
	}
	m.Update(key('q', tea.ModCtrl))
	m.Update(key('-', 0))
	wantCurrent(t, m, "tab-0") // it was the last: the one to its left

	// The only one closed, the focus moves to the list.
	m.Update(key('q', tea.ModCtrl))
	m.Update(key('-', 0))
	if _, ok := m.activeAgent(); ok {
		t.Fatal("still an active agent")
	}
	if m.layout.focus != project {
		t.Fatalf("focus %s", m.layout.focus)
	}
	wantText(t, m, " agent ─", "no agent shown")

	// Enter on it opens it again.
	m.Update(keyMsgs("enter")[0])
	if len(m.list().tabs) != 1 {
		t.Fatalf("tabs %v", m.list().tabs)
	}
}

// C-q - closes nothing but with the agent pane in focus: elsewhere it could seem
// to close a terminal's tab.
func TestTabCloseKeyElsewhere(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(2)
	for _, focus := range []pane{project, terminal} {
		m.layout.setFocus(focus)
		m.Update(key('q', tea.ModCtrl))
		if strings.Contains(text(m), "close the tab") {
			t.Fatalf("%s: offered:\n%s", focus, text(m))
		}
		m.Update(key('-', 0))
		if len(m.list().tabs) != 2 {
			t.Fatalf("%s: closed a tab", focus)
		}
	}
	// In the list, nor does - without the menu.
	m.layout.setFocus(project)
	m.Update(key('-', 0))
	if len(m.list().tabs) != 2 {
		t.Fatal("- closed a tab from the list")
	}
}

// tabX returns the column of s on the title row.
func tabX(t *testing.T, m *Model, s string) int {
	t.Helper()
	got := titleRow(m)
	i := strings.Index(got, s)
	if i < 0 {
		t.Fatalf("no %q in %q", s, got)
	}
	return ansi.StringWidth(got[:i])
}

func TestTabClicks(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(7)

	// A click on a tab selects it and focuses the pane.
	m.layout.setFocus(project)
	m.Update(clickAt(tabX(t, m, "tab-2"), 0))
	wantCurrent(t, m, "tab-2")
	if m.layout.focus != agent {
		t.Fatalf("focus %s", m.layout.focus)
	}

	// An arrow selects the nearest tab hidden on its side.
	m.Update(clickAt(tabX(t, m, "→"), 0))
	wantCurrent(t, m, "tab-5")
	m.Update(clickAt(tabX(t, m, "←"), 0))
	wantCurrent(t, m, "tab-1")

	// Its button closes that tab, not the current one, and only the tab.
	m.Update(clickAt(tabX(t, m, "tab-3")+len("tab-3"), 0))
	if slices.Contains(m.list().tabs, "agent-tab-3") || len(m.agents()) != 7 {
		t.Fatalf("tabs %v, agents %v", m.list().tabs, m.agents())
	}
	wantCurrent(t, m, "tab-1")
}

func TestTabDoubleClicks(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(4)

	// Quick clicks on the close buttons close, and never zoom.
	m.Update(clickAt(tabX(t, m, "tab-3")+len("tab-3"), 0))
	m.Update(clickAt(tabX(t, m, "tab-2")+len("tab-2"), 0))
	if m.layout.zoomed != none || len(m.list().tabs) != 2 {
		t.Fatalf("zoomed %s, tabs %v", m.layout.zoomed, m.list().tabs)
	}

	// Nor do quick clicks on two tabs.
	m.Update(clickAt(tabX(t, m, "tab-0"), 0))
	m.Update(clickAt(tabX(t, m, "tab-1"), 0))
	if m.layout.zoomed != none {
		t.Fatal("two tabs zoomed")
	}
	wantCurrent(t, m, "tab-1")

	// Two on one tab do.
	m.Update(clickAt(tabX(t, m, "tab-1"), 0))
	if m.layout.zoomed != agent {
		t.Fatalf("a double-click zoomed %s", m.layout.zoomed)
	}
}

// Terminal tabs zoomed on quick clicks on two of them, too.
func TestTerminalTabDoubleClicks(t *testing.T) {
	t.Parallel()
	m, _ := newModel(100, 41)
	m.Update(clickAt(4, 30))
	m.Update(clickAt(14, 30))
	if m.layout.zoomed != none || m.tab != 1 {
		t.Fatalf("zoomed %s, tab %d", m.layout.zoomed, m.tab)
	}
	m.Update(clickAt(14, 30))
	if m.layout.zoomed != terminal {
		t.Fatalf("a double-click zoomed %s", m.layout.zoomed)
	}
}

func TestTabAgentRemoved(t *testing.T) {
	t.Parallel()
	m, agents := tabsModel(3)
	m.Update(key(tea.KeyRight, tea.ModAlt))

	// Gone, its tab closes and the next takes over.
	sentAgents(m, agents[0], agents[2])
	wantCurrent(t, m, "tab-2")
	if !slices.Equal(m.list().tabs, []string{"agent-tab-0", "agent-tab-2"}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}

	// An agent exited keeps its tab.
	sentAgents(m, agents[0], exited("tab-2", 0))
	wantCurrent(t, m, "tab-2")

	// The last gone, the focus moves to the list.
	sentAgents(m)
	if m.layout.focus != project || len(m.list().tabs) != 0 {
		t.Fatalf("focus %s, tabs %v", m.layout.focus, m.list().tabs)
	}
}

// A tombstone's tab closes when it is removed, and stays when it is opened later.
func TestTabTombstone(t *testing.T) {
	t.Parallel()
	removedParent := func(state api.AgentState) api.Agent {
		a := parentOf(running("parent"), running("child"))
		a.State = state
		return a
	}
	m := newAgentsModel(100, 41, removedParent(api.AgentStateRunning))
	wantCurrent(t, m, "parent")

	sentAgents(m, removedParent(api.AgentStateRemoved))
	if len(m.list().tabs) != 0 {
		t.Fatalf("the removed agent keeps its tab: %v", m.list().tabs)
	}

	m.moveCursor(0)
	m.Update(keyMsgs("enter")[0])
	wantCurrent(t, m, "parent")
	sentAgents(m, removedParent(api.AgentStateRemoved))
	wantCurrent(t, m, "parent")
}

// Only the current tab's agent attaches: at once on one switch, and after the
// keys rest on several.
func TestTabsAttachOnlyCurrent(t *testing.T) {
	t.Parallel()
	m, agents := tabsModel(4)
	terms := []api.Terminal{{Id: "term-shell", Name: "shell", State: api.TerminalStateRunning, Dir: "/work/demo"}}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents, Terminals: &terms}}})
	m.Update(rested{rests: m.rests})
	shell := m.term
	attached := func() string {
		if m.agentTerm == nil {
			return ""
		}
		return m.agentTerm.id
	}
	if attached() != "term-tab-0" {
		t.Fatalf("attached %q", attached())
	}

	m.Update(key(tea.KeyRight, tea.ModAlt))
	if attached() != "term-tab-1" {
		t.Fatalf("one switch attached %q", attached())
	}
	first := m.tabRests
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if attached() != "" {
		t.Fatalf("walking past tabs attached %q", attached())
	}
	wantCurrent(t, m, "tab-3")

	// An older rest ending changes nothing; the last one attaches.
	m.Update(tabRested{rests: first})
	if attached() != "" {
		t.Fatalf("an older rest attached %q", attached())
	}
	m.Update(tabRested{rests: m.tabRests})
	if attached() != "term-tab-3" {
		t.Fatalf("after the rest %q", attached())
	}

	if shell == nil || m.term != shell {
		t.Fatalf("the terminal pane's attachment changed: %+v, was %+v", m.term, shell)
	}
}

func TestTabsResume(t *testing.T) {
	t.Parallel()
	m, agents := tabsModel(3)
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.executable = executableFile(t)
	m.Update(key('q', tea.ModCtrl))
	m.update(key('r', 0))
	r := m.Reload()
	if r == nil || r.Agent != "agent-tab-1" || !slices.Equal(r.Tabs, []string{"agent-tab-0", "agent-tab-1", "agent-tab-2"}) {
		t.Fatalf("resume %+v", r)
	}

	// A tab whose agent is gone is dropped; the current one stays current.
	next := New(m.ctx, Options{Style: style.New(false), Resume: r})
	next.tick = noTick
	next.Update(tea.WindowSizeMsg{Width: 100, Height: 41})
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	left := []api.Agent{agents[1], agents[2]}
	next.Update(projectEvent{gen: next.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &left}}})
	wantCurrent(t, next, "tab-1")
	if !slices.Equal(next.list().tabs, []string{"agent-tab-1", "agent-tab-2"}) {
		t.Fatalf("tabs %v", next.list().tabs)
	}
}

// TestTabsLive walks the tabs against a daemon: each agent attaches when its tab
// becomes current, and closing a tab stops nothing.
func TestTabsLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	h.newAgent(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))

	p.keys("down", "enter", "alt+right")
	p.until(t, "b-two attached", agentAttached("b-two"))
	p.keys("alt+left")
	p.until(t, "a-one attached again", agentAttached("a-one"))

	p.keys("C-q", "-")
	p.until(t, "b-two attached after closing", agentAttached("b-two"))
	if len(p.m.agents()) != 2 || p.m.agents()[0].State != api.AgentStateRunning {
		t.Fatalf("closing a tab changed the agents: %+v", p.m.agents())
	}
}
