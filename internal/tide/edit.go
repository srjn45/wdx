package tide

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// A file's tab shows the editor while the project has one running on its file, and
// the file read-only again once it ends (docs/TIDE_CODE.md). The editor runs in the
// daemon, so that it outlives tide as an agent does.

const (
	editFileKey  = "e"
	editTabLabel = "edit: "
)

// editorOpened is the answer to asking for the editor of a file's tab.
type editorOpened struct {
	projectID, tab string
	editor         api.EditorResult
	err            error
}

// editors returns the editors running in the project shown.
func (m *Model) editors() []api.Editor {
	if f := m.following; f != nil && f.project != nil && f.project.Editors != nil {
		return *f.project.Editors
	}
	return nil
}

// tabEditor returns the editor a file's tab shows: the one on its file.
func (m *Model) tabEditor(tab string) (api.Editor, bool) {
	if !isFileTab(tab) {
		return api.Editor{}, false
	}
	worktree, file := diffTabOf(tab)
	i := slices.IndexFunc(m.editors(), func(e api.Editor) bool { return e.Worktree == worktree && e.Path == file })
	if i < 0 {
		return api.Editor{}, false
	}
	return m.editors()[i], true
}

// editShown reports whether the agent pane shows an editor.
func (m *Model) editShown() bool {
	l := m.list()
	if l == nil {
		return false
	}
	_, ok := m.tabEditor(l.active)
	return ok
}

// editorTerminal is the terminal e runs in, as a pane attaches it.
func editorTerminal(e api.Editor, name string) api.Terminal {
	return api.Terminal{Id: e.TerminalId, Name: name, State: e.State}
}

// noHistory is an editor's: what scrolled by in it is the file.
func noHistory(context.Context, *int64, int) (api.TerminalHistoryEnvelope, error) {
	return api.TerminalHistoryEnvelope{}, nil
}

// askEditor asks the daemon for the editor of the file's tab, which shows it once
// the stream brings it; the focus moves to the tab, to type in it.
func (m *Model) askEditor(tab string) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("edit")
	id := f.project.Id
	worktree, file := diffTabOf(tab)
	m.log.Info("editor asked", "worktree", worktree, "path", file)
	return func() tea.Msg {
		r, err := m.client.OpenEditor(m.ctx, worktree, file, "id:"+id)
		return editorOpened{projectID: id, tab: tab, editor: r, err: err}
	}
}

// editorOpened says in the tab why its editor did not start.
func (m *Model) editorOpened(msg editorOpened) {
	if msg.err == nil {
		m.log.Info("editor opened", "result", msg.editor.Result, "terminal_id", msg.editor.Editor.TerminalId, "program", msg.editor.Editor.Program)
		return
	}
	m.log.Info("editor not opened", "tab", msg.tab, "err", msg.err)
	why := msg.err.Error()
	if e, ok := errors.AsType[*client.ProblemError](msg.err); ok {
		why = e.Problem.Detail
		if e.Problem.Code == api.ErrorCodeDependencyMissing && e.Problem.Details != nil {
			if name, ok := (*e.Problem.Details)["dependency"].(string); ok {
				why = name + " not found: wdx deps install --name " + name
			}
		}
	}
	if l, ok := m.agentLists[msg.projectID]; ok && l.diffs[msg.tab] != nil {
		l.diffs[msg.tab].notice = why
		return
	}
	m.statusNotice = why
}

// editorsChanged gives each editor running a tab, on the project's first event: one
// started before tide was, or by another tide, could not be reached otherwise.
func (m *Model) editorsChanged() {
	l := m.list()
	if l.editorsSeen {
		return
	}
	l.editorsSeen = true
	for _, e := range m.editors() {
		tab := fileTab(e.Worktree, e.Path)
		if slices.Contains(l.tabs, tab) {
			continue
		}
		l.tabs = append(l.tabs, tab)
		m.log.Info("editor's tab opened", "worktree", e.Worktree, "path", e.Path, "reason", "running")
	}
	if l.active == "" && len(l.tabs) > 0 {
		l.active = l.tabs[0]
	}
}

// editorsIn names the editors running in the worktree called name, or in every one
// when it is "".
func (m *Model) editorsIn(name string) []string {
	var names []string
	for _, e := range m.editors() {
		if name == "" || e.Worktree == name {
			names = append(names, e.Worktree+"/"+e.Path)
		}
	}
	return names
}
