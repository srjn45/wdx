package tide

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// tabsSaved reports whether the project's tabs are tabs, and the daemon has them.
func tabsSavedAs(tabs ...string) func(*Model) bool {
	return func(m *Model) bool {
		l := m.list()
		return l != nil && !l.savingTabs && slices.Equal(l.tabs, tabs) && slices.Equal(l.savedTabs, tabs)
	}
}

// TestTabsKeptByDaemon: the tabs tide opens are the daemon's, a removal through the
// API closes its tab in the tide open, and a tide started later opens what is left.
func TestTabsKeptByDaemon(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	one := h.newAgent(t, "api", dir, "a-one")
	two := h.newAgent(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one's tab saved", tabsSavedAs(one.Id)) // none kept: the first running agent's
	p.keys("down", "enter", "C-q", "i")
	p.until(t, "three tabs saved", tabsSavedAs(one.Id, two.Id, infoTab(two.Id)))

	stream, err := h.client().ProjectEvents(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	e, err := stream.Next()
	stream.Close()
	if err != nil || len(e.Tabs) != 3 || e.Tabs[2].Kind != api.TabKindInfo || *e.Tabs[2].AgentId != two.Id {
		t.Fatalf("the daemon has %+v, %v", e.Tabs, err)
	}

	// a-one gone, what is left is no list a tide would open by itself.
	if _, err := h.client().DeleteAgent(context.Background(), "id:"+one.Id, "", "", true); err != nil {
		t.Fatal(err)
	}
	left := []string{two.Id, infoTab(two.Id)}
	p.until(t, "a-one's tab closed", func(m *Model) bool {
		return slices.Equal(m.list().tabs, left) && m.list().active == infoTab(two.Id)
	})

	p.until(t, "the view saved", viewSaved) // so the next tide's current tab is the one kept

	next := h.openShown(t)
	next.until(t, "the tabs kept", func(m *Model) bool {
		l := m.list()
		return l != nil && l.tabsRead && slices.Equal(l.tabs, left) && l.active == infoTab(two.Id)
	})
}

// TestTabsEventWhileSaving: an event that comes while tide's list is on its way,
// or waits to go, is older than tide's list and leaves it.
func TestTabsEventWhileSaving(t *testing.T) {
	t.Parallel()
	m, agents := tabsModel(3)
	l := m.list()
	l.tabsRead, l.savedTabs = true, []string{"agent-tab-0"} // the list since grew
	sent := func(tabs ...string) {
		list := []api.Tab{}
		for _, tab := range tabs {
			a, _ := apiTab(tab)
			list = append(list, a)
		}
		m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents}, Tabs: list}})
	}
	sent("agent-tab-0")
	all := []string{"agent-tab-0", "agent-tab-1", "agent-tab-2"}
	if !slices.Equal(l.tabs, all) {
		t.Fatalf("an event older than a list waiting undid it: %v", l.tabs)
	}
	l.savedTabs, l.savingTabs = all, true
	sent("agent-tab-0")
	if !slices.Equal(l.tabs, all) {
		t.Fatalf("an event older than a list on its way undid it: %v", l.tabs)
	}
	m.Update(tabsSaved{projectID: "proj-demo", tabs: all})
	sent("agent-tab-0", "agent-tab-2")
	if !slices.Equal(l.tabs, []string{"agent-tab-0", "agent-tab-2"}) {
		t.Fatalf("once saved, a change through wdx: %v", l.tabs)
	}
}

// TestTabsNotSaved: a refused list says so on the status line and is not sent again
// until the tabs change.
func TestTabsNotSaved(t *testing.T) {
	t.Parallel()
	m, _ := tabsModel(2)
	l := m.list()
	l.savingTabs = true
	m.Update(tabsSaved{projectID: "proj-demo", tabs: savable(l.tabs), err: errors.New("refused")})
	if l.savingTabs || !slices.Equal(l.savedTabs, l.tabs) || m.statusNotice != "tabs not saved: refused" {
		t.Fatalf("saving %v, saved %v, notice %q", l.savingTabs, l.savedTabs, m.statusNotice)
	}
}

// TestTabStrings: every tab tide has goes to the API and comes back the same, and a
// view of a tab is no tab.
func TestTabStrings(t *testing.T) {
	t.Parallel()
	for _, tab := range []string{"agent-x", infoTab("agent-x"), settingsTab, docTab("doc-a"),
		fileTab("main", "a/b.go"), fileDiffTab("feat", "c:d.go"), committedTab("feat")} {
		a, ok := apiTab(tab)
		back, ok2 := tideTab(a)
		if !ok || !ok2 || back != tab {
			t.Errorf("%q: %+v %v, back %q %v", tab, a, ok, back, ok2)
		}
	}
	if a, ok := apiTab(uncommittedPrefix + "feat"); ok {
		t.Errorf("a view is a tab: %+v", a)
	}
}
