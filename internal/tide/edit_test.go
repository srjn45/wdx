package tide

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/gittest"
)

// withEditors gives the project shown the editors, as an event of its stream does.
func withEditors(m *Model, editors ...api.Editor) {
	p := *m.following.project
	p.Editors = &editors
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: p}})
}

var aEditor = api.Editor{TerminalId: "term-edit", Worktree: "main", Path: "dir/a.go", Program: "vi", State: api.TerminalStateRunning}

func TestEditTab(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf("package a\n"))
	v := m.shownDiff()

	withEditors(m, aEditor)
	if !m.editShown() || m.textShown() {
		t.Fatalf("editing %v, text %v", m.editShown(), m.textShown())
	}
	if !strings.Contains(titleRow(m), "[edit: main/a.go") {
		t.Errorf("title %q", titleRow(m))
	}
	if shown, _, ok := m.attachedTerminal(); !ok || shown.Id != aEditor.TerminalId {
		t.Errorf("attached %+v, %v", shown, ok)
	}
	// Another file's editor is not this tab's.
	withEditors(m, api.Editor{TerminalId: "term-other", Worktree: "auth-fix", Path: "dir/a.go", State: api.TerminalStateRunning})
	if m.editShown() || !m.textShown() {
		t.Fatalf("another worktree's editor shown")
	}

	// Once the editor ends, the file is read again: it may have changed.
	withEditors(m, aEditor)
	withEditors(m)
	if !m.textShown() || !v.loading {
		t.Errorf("text %v, read again %v", m.textShown(), v.loading)
	}
	if !strings.Contains(titleRow(m), "[file: main/a.go") {
		t.Errorf("title %q", titleRow(m))
	}
}

// An editor stopping, on its way out, attaches nothing: it has no history to show.
func TestEditTabOfAnEditorStopping(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	stopping := aEditor
	stopping.State = api.TerminalStateStopped
	withEditors(m, stopping)
	if _, _, ok := m.attachedTerminal(); ok || m.attachedIn(agent) != nil {
		t.Error("an editor stopping is attached")
	}
}

func TestEditorRefused(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf("package a\n"))
	details := map[string]any{"dependency": "vi"}
	m.Update(editorOpened{projectID: "proj-demo", tab: aFile, err: &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeDependencyMissing, Detail: "vi not found", Details: &details}}})
	wantText(t, m, "vi not found: wdx deps install --name vi")
	m.Update(editorOpened{projectID: "proj-demo", tab: aFile, err: &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeInvalidPath, Detail: `"dir/a.go" is not a file`}}})
	wantText(t, m, `"dir/a.go" is not a file`)
}

// TestEditorGetsItsTab checks an editor running when the project comes gets a tab,
// as when tide starts again: nothing else could reach it.
func TestEditorGetsItsTab(t *testing.T) {
	t.Parallel()
	m := settingsModel()
	l := m.list()
	l.editorsSeen = false
	l.tabs, l.active = nil, ""
	withEditors(m, aEditor)
	if !slices.Equal(l.tabs, []string{aFile}) || l.active != aFile {
		t.Fatalf("tabs %v, active %q", l.tabs, l.active)
	}
	// Only then: one closed afterwards stays closed.
	m.closeTab(aFile, "asked")
	withEditors(m, aEditor)
	if len(l.tabs) != 0 {
		t.Fatalf("tabs %v", l.tabs)
	}
}

func TestEditorsConfirmed(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	m.following.project.State = api.ProjectStateOpen
	withEditors(m, aEditor)
	r, ok := m.closableProject()
	if !ok || !r.running {
		t.Fatalf("closing %+v", r)
	}
	m.confirm = &confirmBox{r: r}
	if body := strings.Join(m.confirmPopup().body, "\n"); !strings.Contains(body, "Its editors stop, losing what they hold unsaved: main/dir/a.go") {
		t.Errorf("close says:\n%s", body)
	}
	w := m.worktreeRemovalOf(api.WorktreeStatus{Name: "main", Path: "/r"})
	if lines := strings.Join(w.worktree.lines(), "\n"); !strings.Contains(lines, "Stopped, losing what they hold unsaved: main/dir/a.go") {
		t.Errorf("removal says:\n%s", lines)
	}
	if other := m.worktreeRemovalOf(api.WorktreeStatus{Name: "auth-fix", Path: "/a"}); len(other.worktree.editors) != 0 {
		t.Errorf("another worktree's removal names %v", other.worktree.editors)
	}
}

// TestEditLive edits a file in a real daemon's editor, whose stand-in is the test
// home's shell: it ignores what it is told to run, so the test types into a shell.
func TestEditLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "file.go"), "package one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the file listed", func(m *Model) bool { return strings.Contains(text(m), "file.go") })
	m := p.m
	m.layout.setFocus(project)
	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "file.go" }))
	p.keys(editFileKey)
	if m.layout.focus != agent {
		t.Fatalf("the focus stayed in %v", m.layout.focus)
	}
	p.until(t, "the editor attached", func(m *Model) bool {
		a := m.attachedIn(agent)
		return m.editShown() && a != nil && a.pane != nil && a.live
	})
	if !strings.Contains(titleRow(m), "[edit: main/file.go") {
		t.Errorf("title %q", titleRow(m))
	}

	// Another tide, started now, finds the editor and gives it a tab.
	other := h.follow(t)
	other.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	other.keys("enter")
	other.until(t, "the editor's tab", func(m *Model) bool {
		return m.list() != nil && slices.Contains(m.list().tabs, fileTab("main", "file.go"))
	})

	p.keys("printf 'package two\\n' > file.go; exit", "enter")
	p.until(t, "the file read again once the editor ended", func(m *Model) bool {
		return !m.editShown() && strings.Contains(text(m), "1 package two")
	})
	if m.attachedIn(agent) != nil {
		t.Error("still attached")
	}
	if !strings.Contains(titleRow(m), "[file: main/file.go") {
		t.Errorf("title %q", titleRow(m))
	}

	// e on the file's tab edits it again.
	p.keys(editFileKey)
	p.until(t, "the editor attached again", func(m *Model) bool {
		a := m.attachedIn(agent)
		return m.editShown() && a != nil && a.pane != nil && a.live
	})
	p.keys("exit", "enter")
	p.until(t, "the editor gone", func(m *Model) bool { return !m.editShown() && m.attachedIn(agent) == nil })
}

// C-q r keeps the editor's tab current, so that it attaches again.
func TestEditTabThroughReload(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	withEditors(m, aEditor)
	m.executable = executableFile(t)
	keys(m, "C-q", "r")
	if r := m.Reload(); r == nil || r.Agent != aFile || !slices.Contains(r.Tabs, aFile) {
		t.Fatalf("reload: %+v", r)
	}
}
