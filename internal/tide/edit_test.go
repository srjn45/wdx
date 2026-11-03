package tide

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
	// One too long for the bar is cut short, not lost.
	long := "terminal balmy-glacier did not start: listen unix " + strings.Repeat("/very/long/path", 10) + ": bind: invalid argument"
	m.Update(editorOpened{projectID: "proj-demo", tab: aFile, err: &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeStartFailed, Detail: long}}})
	wantText(t, m, "terminal balmy-glacier did not start: listen unix /very/long/path", "…")
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

	// A tide started anew, as after this one quit, finds the editor and gives it a tab.
	other := h.follow(t)
	// It starts on the project when the first tide's view was saved by then, and
	// on the switcher otherwise.
	other.until(t, "a start", func(m *Model) bool { return m.switcher != nil || m.shown != "" })
	if other.m.switcher != nil {
		other.keys("enter")
	}
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

// TestCloseEditorTab checks closing an editor's tab asks first, even when it is not
// the current one, and goes back to it.
func TestCloseEditorTab(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	other := fileTab("main", "top")
	m.list().openTab(other)
	withEditors(m, aEditor)
	m.layout.setFocus(project)

	row := titleRow(m)
	at := strings.Index(row, "edit: main/a.go(-)")
	if at < 0 {
		t.Fatalf("title %q", row)
	}
	m.Update(clickAt(ansi.StringWidth(row[:at])+len("edit: main/a.go")+1, 0))
	if m.editorClose == nil || m.list().active != aFile || m.layout.focus != agent {
		t.Fatalf("box %v, tab %q, focus %v", m.editorClose, m.list().active, m.layout.focus)
	}
	wantText(t, m, "Close editor", "Close edit: main/a.go?", "s save  d discard  Esc keep editing")
	keys(m, "x") // not an answer
	if m.editorClose == nil {
		t.Fatal("another key closed the box")
	}
	keys(m, "esc")
	if m.editorClose != nil || !slices.Contains(m.list().tabs, aFile) || len(m.editorKeys) != 0 || m.list().closing[aFile] {
		t.Fatalf("box %v, tabs %v, keys %q, closing %v", m.editorClose, m.list().tabs, m.editorKeys, m.list().closing)
	}

	// Not attached yet: the keys wait for the attachment.
	m.askCloseTab(aFile, "asked")
	keys(m, discardKey)
	if m.editorKeys[aEditor.TerminalId] != "\x1c\x0e:b1 | q!\r" || !m.list().closing[aFile] {
		t.Fatalf("keys %q, closing %v", m.editorKeys, m.list().closing)
	}
	// It stays until the editor ends.
	withEditors(m, aEditor)
	if !slices.Contains(m.list().tabs, aFile) {
		t.Fatal("closed with its editor running")
	}
	withEditors(m)
	if slices.Contains(m.list().tabs, aFile) || len(m.editorKeys) != 0 {
		t.Fatalf("tabs %v, keys %q", m.list().tabs, m.editorKeys)
	}

	// A tab with no editor closes at once.
	m.askCloseTab(other, "asked")
	if m.editorClose != nil || slices.Contains(m.list().tabs, other) {
		t.Fatalf("box %v, tabs %v", m.editorClose, m.list().tabs)
	}
}

// TestCloseEditorTabLive answers the box with each key, into the test home's shell
// standing in for vi: it shows what it was typed and stays, as vi does when something
// stops it, and the tab with it, until it ends.
func TestCloseEditorTabLive(t *testing.T) {
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
	tab := fileTab("main", "file.go")
	for _, answer := range []struct{ key, typed string }{{saveKey, ":b1 | x"}, {discardKey, ":b1 | q!"}} {
		m := p.m
		m.layout.setFocus(project)
		m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "file.go" }))
		p.keys(editFileKey)
		p.until(t, "the editor attached", func(m *Model) bool {
			a := m.attachedIn(agent)
			return m.editShown() && a != nil && a.pane != nil && a.live
		})
		p.keys("C-q", "-")
		if m.editorClose == nil {
			t.Fatal("no box")
		}
		p.keys(answer.key)
		p.until(t, "the keys typed: "+answer.typed, func(m *Model) bool { return strings.Contains(text(m), answer.typed) })
		if !slices.Contains(m.list().tabs, tab) {
			t.Fatal("the tab closed with its editor running")
		}
		p.keys("exit", "enter")
		p.until(t, "the tab closed once the editor ended", func(m *Model) bool { return !slices.Contains(m.list().tabs, tab) })
	}

	// Asked while another tab is shown: the keys wait for the editor's to attach.
	m := p.m
	m.layout.setFocus(project)
	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "file.go" }))
	p.keys(editFileKey)
	p.until(t, "the editor attached", func(m *Model) bool {
		a := m.attachedIn(agent)
		return m.editShown() && a != nil && a.pane != nil && a.live
	})
	m.list().openTab(settingsTab)
	p.update(nil)
	if m.attachedIn(agent) != nil {
		t.Fatal("the editor is still attached")
	}
	m.askCloseTab(tab, "clicked")
	p.keys(saveKey)
	if m.editorKeys == nil || m.editorKeys[m.attachedIn(agent).id] == "" {
		t.Fatalf("no keys waiting: %q", m.editorKeys)
	}
	p.until(t, "the keys typed once attached", func(m *Model) bool { return strings.Contains(text(m), ":b1 | x") })
	p.keys("exit", "enter")
	p.until(t, "the tab closed once the editor ended", func(m *Model) bool { return !slices.Contains(m.list().tabs, tab) })
}
