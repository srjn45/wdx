package tide

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/waittest"
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
	revision := int64(0)
	sent := func(tabs ...string) {
		m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents}, Tabs: apiTabs(tabs), TabsRevision: revision}})
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
	m.Update(tabsSaved{projectID: "proj-demo", tabs: all, revision: 5})
	revision = 4 // sent before tide's list reached the daemon, and come after its answer
	sent("agent-tab-0")
	if !slices.Equal(l.tabs, all) {
		t.Fatalf("an event older than the list saved undid it: %v", l.tabs)
	}
	revision = 6
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
	for _, tab := range []string{"agent-x", infoTab("agent-x"), settingsTab, docTab("doc-a"), planTab("doc-p"),
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

// TestTabsFlushedOnQuit: quitting sends a change to the tabs that waited for a list
// on its way, once that list is answered, so the later list is the one kept.
func TestTabsFlushedOnQuit(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	one := h.newAgent(t, "api", dir, "a-one")
	two := h.newAgent(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one's tab saved", tabsSavedAs(one.Id))
	p.keys("down", "enter") // b-two's tab: its list goes
	if !p.m.list().savingTabs {
		t.Fatal("no list on its way")
	}
	p.keys("C-q", "i") // its info: waits for the list on its way
	p.keys("C-q", "q")
	timeout := waittest.After(t)
	for quit := false; !quit; {
		select {
		case msg := <-p.msgs:
			_, quit = msg.(tea.QuitMsg)
		case <-timeout:
			t.Fatalf("tide did not quit:\n%s", text(p.m))
		}
	}
	stream, err := h.client().ProjectEvents(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	e, err := stream.Next()
	stream.Close()
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, a := range e.Tabs {
		tab, _ := tideTab(a)
		kept = append(kept, tab)
	}
	if want := []string{one.Id, two.Id, infoTab(two.Id)}; !slices.Equal(kept, want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
}

// TestMissingTab: a tab whose file the daemon says is gone is marked ✗, its pane
// says what is gone and where, it reads nothing, and C-q - closes it; one whose
// file is back is whole again.
func TestMissingTab(t *testing.T) {
	t.Parallel()
	worktrees := []api.WorktreeStatus{{Name: "main", Path: "/r"}}
	m := worktreesModel(worktrees...)
	tab := fileTab("main", "a.go")
	l := m.list()
	l.tabsRead, l.tabs, l.active, l.savedTabs = true, []string{tab}, tab, []string{tab}
	m.layout.setFocus(agent)
	sent := func(missing bool) {
		a, _ := apiTab(tab)
		a.Missing = &missing
		m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", WorktreeStatuses: &worktrees}, Tabs: []api.Tab{a}}})
	}
	sent(true)
	if row := titleRow(m); !strings.Contains(row, "[✗ ") {
		t.Errorf("the strip %q", row)
	}
	if got := text(m); !strings.Contains(got, "file a.go is no longer in worktree main") || !strings.Contains(got, "C-q - closes this tab") {
		t.Fatalf("the pane:\n%s", got)
	}
	if v := l.diffs[tab]; v != nil && v.asked {
		t.Errorf("a missing tab was read: %+v", v)
	}

	sent(false)
	if strings.Contains(titleRow(m), "✗") || strings.Contains(text(m), "no longer") {
		t.Errorf("back, still missing:\n%s", text(m))
	}
	sent(true)
	keys(m, "C-q", "-")
	if len(l.tabs) != 0 {
		t.Errorf("C-q - left %v", l.tabs)
	}
}

// TestMissingWorktreeTab: a tab whose worktree is gone says so, not that its file is.
func TestMissingWorktreeTab(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	tab := committedTab("feat")
	l := m.list()
	l.tabsRead, l.tabs, l.active, l.savedTabs = true, []string{tab}, tab, []string{tab}
	m.layout.setFocus(agent)
	a, _ := apiTab(tab)
	yes := true
	a.Missing = &yes
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: *m.following.project, Tabs: []api.Tab{a}}})
	if got := text(m); !strings.Contains(got, "worktree feat is no longer there") {
		t.Fatalf("the pane:\n%s", got)
	}
}
