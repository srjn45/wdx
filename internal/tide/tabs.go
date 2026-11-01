package tide

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
)

// The agent pane's tabs (docs/TIDE_TABS.md). A tab is only a view of an agent:
// closing one stops nothing. The current tab is the active agent, the only one
// attached; the others are a name and a mark from the project's stream.

const (
	tabClose       = "(-)"
	tabHiddenLeft  = "←"
	tabHiddenRight = "→"
	// tabFrame is what a tab takes besides its name: "[● (-)]".
	tabFrame = 7
	// tabMark is what the mark takes of that: itself and the space after it.
	tabMark = 2
	// tabArrow is what an arrow takes: itself and the line beside it.
	tabArrow = 2
)

// tabRested ends the rest after the tab switched to with the keys.
type tabRested struct{ rests int }

// openTab makes the tab current, opening it at the right end when it is not
// open.
func (l *agentList) openTab(tab string) {
	if !slices.Contains(l.tabs, tab) {
		l.tabs = append(l.tabs, tab)
	}
	l.active = tab
}

// closeTab closes the tab. When it was current the tab to its right takes over
// or, when it was the last, the one to its left.
func (l *agentList) closeTab(tab string) {
	i := slices.Index(l.tabs, tab)
	if i < 0 {
		return
	}
	l.tabs = slices.Delete(l.tabs, i, i+1)
	if l.active != tab {
		return
	}
	l.active = ""
	if len(l.tabs) > 0 {
		l.active = l.tabs[min(i, len(l.tabs)-1)]
	}
}

// closeTab closes the tab; with none left, the focus moves to the list, where
// the next one is opened.
func (m *Model) closeTab(tab, reason string) {
	l := m.list()
	l.closeTab(tab)
	m.log.Info("tab closed", "tab", tab, "reason", reason)
	if len(l.tabs) == 0 && m.layout.focus == agent {
		m.focusProjectPane()
	}
}

// tabsChanged closes the tabs of the agents gone or just removed, even when one
// stays as a tombstone: one already a tombstone was opened on purpose, and the
// first event of a run has nothing to compare with.
func (m *Model) tabsChanged(agents []api.Agent) {
	l := m.list()
	tombstones := map[string]bool{}
	for _, a := range agents {
		if a.State == api.AgentStateRemoved {
			tombstones[a.Id] = true
		}
	}
	for _, tab := range slices.Clone(l.tabs) {
		id := tabAgent(tab)
		// A diff's tab outlives what it shows, and says so.
		if tab == settingsTab || isDiffTab(tab) {
			continue
		}
		if agentIndex(agents, id) < 0 || (tombstones[id] && l.tombstones != nil && !l.tombstones[id]) {
			m.closeTab(tab, "agent removed")
		}
	}
	l.tombstones = tombstones
	if !slices.Contains(l.tabs, l.active) {
		l.active = ""
		if len(l.tabs) > 0 {
			l.active = l.tabs[0]
		}
	}
}

// agentTab is an open tab with the agent it is of, if any.
type agentTab struct {
	tab   string
	agent api.Agent
}

// marked reports whether it shows a state mark: only an agent has a state.
func (t agentTab) marked() bool { return t.tab != settingsTab && !isDiffTab(t.tab) }

// title is what the strip calls it.
func (t agentTab) title() string {
	switch {
	case isDiffTab(t.tab):
		return diffTitle(t.tab)
	case !t.marked():
		return settingsTab
	case isInfoTab(t.tab):
		return infoLabel + t.agent.Name
	}
	return t.agent.Name
}

// frame is what it takes besides its title.
func (t agentTab) frame() int {
	if t.marked() {
		return tabFrame
	}
	return tabFrame - tabMark
}

// openTabs returns the open tabs, in their order.
func (m *Model) openTabs() []agentTab {
	l, agents := m.list(), m.agents()
	if l == nil {
		return nil
	}
	var tabs []agentTab
	for _, tab := range l.tabs {
		if tab == settingsTab || isDiffTab(tab) {
			tabs = append(tabs, agentTab{tab: tab})
		} else if i := agentIndex(agents, tabAgent(tab)); i >= 0 {
			tabs = append(tabs, agentTab{tab: tab, agent: agents[i]})
		}
	}
	return tabs
}

// attachedAgent returns the agent the agent pane attaches: the current tab's,
// unless it is the agent's info.
func (m *Model) attachedAgent() (api.Agent, bool) {
	if m.infoShown() {
		return api.Agent{}, false
	}
	return m.activeAgent()
}

// tabKey moves along left pane - tab 1 - ... - tab n, as Alt+← and Alt+→ do in
// the agent pane.
func (m *Model) tabKey(d direction) tea.Cmd {
	l := m.list()
	i := -1
	if l != nil {
		i = slices.Index(l.tabs, l.active)
	}
	switch {
	case d == left && i <= 0:
		if m.layout.move(left) {
			m.changed("move")
		}
		return nil
	case d == left:
		i--
	case i < 0 || i == len(l.tabs)-1:
		return nil
	default:
		i++
	}
	l.active = l.tabs[i]
	m.log.Debug("tab selected", "tab", l.active)
	return m.restTabs()
}

// restTabs attaches the first tab switched to at once, so that one switch shows
// the agent without a wait, and holds a switch soon after until the keys rest,
// so that walking past tabs attaches none of them.
func (m *Model) restTabs() tea.Cmd {
	m.tabRests++
	m.tabResting = m.tabSwitching
	m.tabSwitching = true
	n := m.tabRests
	return m.tick(attachRest, func(time.Time) tea.Msg { return tabRested{rests: n} })
}

// fitTabs returns the tabs to show, first to last, of tabs of the widths given
// in room cells: the current one, moving the strip no further from the tab it
// started at than it must, and as many others as fit. Lines stand between the
// tabs, and an arrow on each side with tabs hidden there.
func fitTabs(widths []int, current, from, room int) (first, last int) {
	used := func(first, last int) int {
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
	first = min(max(from, 0), current)
	for first < current && used(first, current) > room {
		first++
	}
	last = current
	for {
		switch {
		case last < len(widths)-1 && used(first, last+1) <= room:
			last++
		case first > 0 && used(first-1, last) <= room:
			first--
		default:
			return first, last
		}
	}
}

// agentTabLabels returns the tabs in the cells from x up to end of row y: the
// current one bold, with the border line showing between them. A piece with no
// room left is dropped, which only a tiny window comes to.
func (m *Model) agentTabLabels(tabs []agentTab, x, end, y int) []label {
	l := m.list()
	widths := make([]int, len(tabs))
	current := 0
	for i, t := range tabs {
		widths[i] = ansi.StringWidth(t.title()) + t.frame()
		if t.tab == l.active {
			current = i
		}
	}
	first, last := fitTabs(widths, current, l.tabFrom, end-x)
	l.tabFrom = first

	var labels []label
	put := func(lb label, styled string, width int) {
		if x+width > end {
			return
		}
		lb.styled, lb.area = styled, uv.Rect(x, y, width, 1)
		labels = append(labels, lb)
		x += width
	}
	room := end - x
	if first > 0 {
		put(label{tab: first - 1, arrow: true}, m.st.Muted(tabHiddenLeft), 1)
		x++
		room -= tabArrow
	}
	if last < len(tabs)-1 {
		room -= tabArrow
	}
	for i := first; i <= last; i++ {
		t := tabs[i]
		styled := m.st.Muted
		if i == current {
			styled = m.st.Name
		}
		name := t.title()
		if widths[i] > room {
			name = ansi.Truncate(name, max(room-t.frame(), 0), "…")
		}
		put(label{tab: i}, styled("["), 1)
		if t.marked() {
			put(label{tab: i}, agentMark(m.st, t.agent), 1)
			name = " " + name
		}
		put(label{tab: i}, styled(name), ansi.StringWidth(name))
		put(label{tab: i, closes: true}, styled(tabClose), ansi.StringWidth(tabClose))
		put(label{tab: i}, styled("]"), 1)
		if i < last {
			x++
		}
	}
	if last < len(tabs)-1 {
		x++
		put(label{tab: last + 1, arrow: true}, m.st.Muted(tabHiddenRight), 1)
	}
	return labels
}
