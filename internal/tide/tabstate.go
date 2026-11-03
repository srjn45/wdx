package tide

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The daemon keeps each project's tabs (docs/TIDE_STATE.md): tide sends the list
// whenever it changes, and the project's stream brings it back, with what a
// removal through wdx took from it. Inside tide a tab stays a string.

// tabsSaved is how sending a project's tabs ended.
type tabsSaved struct {
	projectID string
	tabs      []string // what was sent
	revision  int64    // the tabs' revision once saved
	err       error
}

// apiTab is tab as the API names it; false for one it has no kind for.
func apiTab(tab string) (api.Tab, bool) {
	text := func(s string) *string { return &s }
	switch {
	case tab == "":
		return api.Tab{}, false
	case tab == settingsTab:
		return api.Tab{Kind: api.TabKindSettings}, true
	case isInfoTab(tab):
		return api.Tab{Kind: api.TabKindInfo, AgentId: text(tabAgent(tab))}, true
	case isDocTab(tab):
		return api.Tab{Kind: api.TabKindDoc, DocId: text(strings.TrimPrefix(tab, docTabPrefix))}, true
	case isPlanTab(tab):
		return api.Tab{Kind: api.TabKindPlan, DocId: text(strings.TrimPrefix(tab, planTabPrefix))}, true
	case isFileTab(tab), strings.HasPrefix(tab, fileDiffPrefix):
		worktree, file := diffTabOf(tab)
		kind := api.TabKindFile
		if !isFileTab(tab) {
			kind = api.TabKindDiff
		}
		return api.Tab{Kind: kind, Worktree: text(worktree), Path: text(file)}, true
	case strings.HasPrefix(tab, committedPrefix):
		worktree, _ := diffTabOf(tab)
		return api.Tab{Kind: api.TabKindPr, Worktree: text(worktree)}, true
	case strings.Contains(tab, ":"): // a view of a tab, never a tab itself
		return api.Tab{}, false
	}
	return api.Tab{Kind: api.TabKindAgent, AgentId: text(tab)}, true
}

// tideTab is the tab the API's t names; false for a kind tide does not know.
func tideTab(t api.Tab) (string, bool) {
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	agentID, worktree, path := value(t.AgentId), value(t.Worktree), value(t.Path)
	switch t.Kind {
	case api.TabKindAgent:
		return agentID, agentID != ""
	case api.TabKindInfo:
		return infoTab(agentID), agentID != ""
	case api.TabKindSettings:
		return settingsTab, true
	case api.TabKindDoc:
		return docTab(value(t.DocId)), t.DocId != nil
	case api.TabKindPlan:
		return planTab(value(t.DocId)), t.DocId != nil
	case api.TabKindFile:
		return fileTab(worktree, path), worktree != "" && path != ""
	case api.TabKindDiff:
		return fileDiffTab(worktree, path), worktree != "" && path != ""
	case api.TabKindPr:
		return committedTab(worktree), worktree != ""
	}
	return "", false
}

// savable are the tabs the API has a kind for, in their order.
func savable(tabs []string) []string {
	list := []string{}
	for _, tab := range tabs {
		if _, ok := apiTab(tab); ok {
			list = append(list, tab)
		}
	}
	return list
}

// tabsSync sends the shown project's tabs when they differ from what was last
// sent or came, once its stream has brought what the daemon kept. One request is
// on its way at a time: two at once could reach the daemon in either order, and
// the older list win. What changes meanwhile goes once it is back.
func (m *Model) tabsSync() tea.Cmd {
	l := m.list()
	if l == nil || m.client == nil || !l.tabsRead || l.savingTabs {
		return nil
	}
	tabs := savable(l.tabs)
	if slices.Equal(tabs, l.savedTabs) {
		return nil
	}
	list := apiTabs(tabs)
	done := make(chan struct{})
	l.savingTabs, l.sendingTabs, l.tabsInFlight = true, tabs, done
	id := m.following.project.Id
	m.log.Info("tabs sent", "project_id", id, "count", len(tabs))
	return func() tea.Msg {
		defer close(done)
		r, err := m.client.ReplaceTabs(m.ctx, "id:"+id, list)
		return tabsSaved{projectID: id, tabs: tabs, revision: r.TabsRevision, err: err}
	}
}

// apiTabs are the tabs as the API names them.
func apiTabs(tabs []string) []api.Tab {
	list := make([]api.Tab, len(tabs))
	for i, tab := range tabs {
		list[i], _ = apiTab(tab)
	}
	return list
}

// tabsWereSaved ends a sending. A failure is not tried again: the next change
// sends the list, and the next event brings the daemon's.
func (m *Model) tabsWereSaved(msg tabsSaved) {
	l, ok := m.agentLists[msg.projectID]
	if !ok {
		return
	}
	l.savingTabs, l.savedTabs, l.sendingTabs, l.tabsInFlight = false, msg.tabs, nil, nil
	if msg.err == nil {
		l.tabsRevision = max(l.tabsRevision, msg.revision)
	}
	if msg.err != nil {
		m.log.Error("tabs not saved", "project_id", msg.projectID, "err", msg.err)
		m.statusNotice = "tabs not saved: " + msg.err.Error()
	}
}

// tabsFromStream takes the tabs a project's event brings, at revision. The first
// event brings what the daemon kept, which a project with none leaves to tide; a
// later one, what a removal or a rename through wdx changed. An event is older
// than what tide has, and is passed over, while a list of tide's is on its way or
// waits, and when its revision is before the one tide's last list was saved at:
// it was sent before, and came after, on the stream's own connection. A daemon
// too old to keep tabs sends none, and tide keeps its own.
func (m *Model) tabsFromStream(saved []api.Tab, revision int64) {
	l := m.list()
	if saved == nil || l == nil {
		return
	}
	tabs := []string{}
	l.missingTabs = map[string]bool{}
	for _, t := range saved {
		if tab, ok := tideTab(t); ok {
			tabs = append(tabs, tab)
			if t.Missing != nil && *t.Missing {
				l.missingTabs[tab] = true
			}
		}
	}
	if !l.tabsRead {
		l.tabsRead, l.tabsRevision = true, revision
		if len(tabs) == 0 {
			// The run before's are sent, but for an agent gone since, which the
			// daemon would refuse; with none, the first running agent's are.
			l.tabs = slices.DeleteFunc(l.tabs, func(tab string) bool {
				t, ok := apiTab(tab)
				return ok && t.AgentId != nil && agentIndex(m.agents(), *t.AgentId) < 0
			})
			m.log.Info("no tabs kept", "project_id", m.following.project.Id, "resumed", len(l.tabs))
			return
		}
		l.opened, l.tabs, l.savedTabs, l.active = true, tabs, tabs, tabs[0]
		if !slices.Contains(tabs, l.created) {
			l.created = "" // the run before's current tab, now closed
		}
		m.log.Info("tabs restored", "project_id", m.following.project.Id, "count", len(tabs))
		return
	}
	if l.savingTabs || !slices.Equal(savable(l.tabs), l.savedTabs) || revision < l.tabsRevision {
		return
	}
	l.tabsRevision = revision
	if slices.Equal(tabs, l.savedTabs) {
		return
	}
	l.savedTabs = tabs
	m.replaceTabs(tabs)
}

// replaceTabs makes tabs the project's, as something else changed them. A current
// tab that is gone gives its place to the one on its right, or on its left when
// it was the last, as closing it does.
func (m *Model) replaceTabs(tabs []string) {
	l := m.list()
	old := l.tabs
	l.tabs = slices.Clone(tabs)
	if !slices.Contains(l.tabs, l.active) {
		i := slices.Index(old, l.active)
		l.active = ""
		if len(l.tabs) > 0 {
			l.active = l.tabs[max(min(i, len(l.tabs)-1), 0)]
		}
	}
	m.log.Info("tabs changed through wdx", "project_id", m.following.project.Id, "count", len(tabs))
	if len(l.tabs) == 0 && m.layout.focus == agent {
		m.focusProjectPane()
	}
}

// missingShown reports whether the agent pane shows a tab whose file or worktree
// was removed outside wdx.
func (m *Model) missingShown() bool {
	l := m.list()
	return l != nil && l.missingTabs[l.active] && !m.editShown()
}

// missingLines say what the current tab showed and where it was, now gone.
func (m *Model) missingLines(st style.Style) []string {
	worktree, file := diffTabOf(m.list().active)
	what := st.Muted("worktree ") + st.Name(worktree) + st.Muted(" is no longer there")
	if w, ok := m.worktreeNamed(worktree); ok && !w.Missing && isFileTab(m.list().active) {
		what = st.Muted("file ") + st.Name(file) + st.Muted(" is no longer in worktree ") + st.Name(worktree)
	}
	return []string{what, st.Command("C-q -") + st.Muted(" closes this tab")}
}
