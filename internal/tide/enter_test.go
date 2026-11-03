package tide

import (
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// openRows are the left pane's rows that open a tab, each with the model it is
// under the cursor of and the tab it opens.
func openRows(t *testing.T) []struct {
	name  string
	model func() *Model
	tab   string
} {
	t.Helper()
	return []struct {
		name  string
		model func() *Model
		tab   string
	}{
		{"an agent", func() *Model {
			m := newAgentsModel(128, 41, demoAgents()...)
			m.layout.setFocus(project)
			m.moveCursor(rowIndex(m.listRows(), "agent-wise-owl"))
			return m
		}, "agent-wise-owl"},
		{"a worktree", func() *Model {
			m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
			m.moveToWorktree(0)
			return m
		}, committedTab("main")},
		{"a worktree's file", func() *Model {
			m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r", Files: []api.WorktreeFile{changed("a", api.WorktreeFileStatusM)}})
			m.list().worktreesOpen = map[string]bool{"main": true}
			m.moveToWorktree(1)
			return m
		}, fileDiffTab("main", "a")},
		{"a file in Code", func() *Model { m := fileModel(t); m.layout.setFocus(project); return m }, aFile},
		{"a doc", func() *Model { return docsModel(t) }, authTab},
		{"a plan", func() *Model { return plansModel(t) }, planTab("doc-auth")},
	}
}

// TestEnterStays: Enter opens the row's tab and the focus stays in the list.
func TestEnterStays(t *testing.T) {
	t.Parallel()
	for _, r := range openRows(t) {
		m := r.model()
		keys(m, "enter")
		if got := m.list().active; got != r.tab || m.layout.focus != project {
			t.Errorf("%s: tab %q, focus %v; want %q in the list", r.name, got, m.layout.focus, r.tab)
		}
	}
}

// TestShiftEnterGoes: Shift+Enter, and o where the terminal cannot tell it from
// Enter, open the row's tab and go to it.
func TestShiftEnterGoes(t *testing.T) {
	t.Parallel()
	for _, r := range openRows(t) {
		for _, k := range []string{"shift+enter", openAndGoKey} {
			m := r.model()
			keys(m, k)
			if got := m.list().active; got != r.tab || m.layout.focus != agent {
				t.Errorf("%s, %s: tab %q, focus %v; want %q in focus", r.name, k, got, m.layout.focus, r.tab)
			}
		}
	}
}

// TestEnterInAZoomedList: a tab opened behind the zoomed list could not be seen, so
// Enter goes to it, the zoom with it.
func TestEnterInAZoomedList(t *testing.T) {
	t.Parallel()
	for _, r := range openRows(t) {
		m := r.model()
		m.layout.zoomed = project
		keys(m, "enter")
		if got := m.list().active; got != r.tab || m.layout.focus != agent || m.layout.zoomed != agent {
			t.Errorf("%s: tab %q, focus %v, zoomed %v; want %q zoomed in focus", r.name, got, m.layout.focus, m.layout.zoomed, r.tab)
		}
	}
}

// TestEnterOnADirectory: a Code directory has no tab, so Enter and Shift+Enter fold
// or unfold it, and o does nothing.
func TestEnterOnADirectory(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"enter", "shift+enter"} {
		m := fileModel(t)
		m.layout.setFocus(project)
		m.moveToCode(1)
		keys(m, k)
		if m.codeFilesShown().open["dir"] || m.layout.focus != project {
			t.Errorf("%s: dir open %v, focus %v", k, m.codeFilesShown().open["dir"], m.layout.focus)
		}
		keys(m, k)
		if !m.codeFilesShown().open["dir"] {
			t.Errorf("%s: dir not opened again", k)
		}
	}
	m := fileModel(t)
	m.layout.setFocus(project)
	m.moveToCode(1)
	active := m.list().active
	keys(m, openAndGoKey)
	if !m.codeFilesShown().open["dir"] || m.list().active != active || m.layout.focus != project {
		t.Errorf("o on a directory: dir open %v, tab %q, focus %v", m.codeFilesShown().open["dir"], m.list().active, m.layout.focus)
	}
}
