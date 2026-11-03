package tide

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
)

func TestCodeTreeEmptyDirs(t *testing.T) {
	t.Parallel()
	got := flatTree(codeTree([]string{"a/x"}, []string{"a/empty", "b", "c/d"}))
	want := []string{"a/", "a/empty/", "a/x", "b/", "c/", "c/d/"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCodeFileKeys: n, N, r and x act on the Code section's files while the cursor
// is there, as the menu's lines for the selection, and not elsewhere.
func TestCodeFileKeys(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, renameKey)
	if f, ok := m.openTop().(*fieldScreen); !ok || f.value != "a.go" {
		t.Fatalf("r on dir/a.go opened %#v, want its rename", m.openTop())
	}
	keys(m, "esc", newFileKey)
	if s, ok := m.openTop().(*codeDirScreen); !ok || s.directory || s.dir != "dir" {
		t.Fatalf("n on dir/a.go opened %#v, want New File in dir", m.openTop())
	}
	wantText(t, m, "New File", "main/dir/", "create the file here")
	keys(m, "esc")
	if m.openTop() != nil {
		t.Fatal("Esc left the popup open")
	}

	m.moveToCode(1) // dir
	keys(m, newDirKey)
	if s, ok := m.openTop().(*codeDirScreen); !ok || !s.directory || s.dir != "dir" {
		t.Fatalf("N on dir opened %#v", m.openTop())
	}
	keys(m, "esc")
	m.moveToCode(0) // the selector
	keys(m, newFileKey)
	if s, ok := m.openTop().(*codeDirScreen); !ok || s.dir != "" {
		t.Fatalf("n on the selector opened %#v", m.openTop())
	}
	keys(m, "esc", renameKey, deleteFileKey)
	if m.openTop() != nil {
		t.Fatal("r or x on the selector opened a popup")
	}

	// Out of Code, the menu has no rename.
	m.moveCursor(0)
	if m.menuOffers(renameKey) {
		t.Error("r offered in the agents")
	}
}

func TestCodeDirPicker(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"a/b/x", "a/c", "top", ".hidden/y"}, codeWorktreeList()...)
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"a/b/x", "a/c", "top", ".hidden/y"}, emptyDirs: []string{"a/empty", "z"}}, nil))
	m.moveToCode(0)
	keys(m, newDirKey)
	s := m.openTop().(*codeDirScreen)
	if got := s.items(); !slices.Equal(got, []string{"create the directory here", "a", "z"}) {
		t.Fatalf("at the top: %q", got)
	}
	keys(m, "down", "right")
	if got := s.items(); s.dir != "a" || !slices.Equal(got, []string{"create the directory here", "b", "empty"}) {
		t.Fatalf("in a: %q %q", s.dir, got)
	}
	keys(m, "e")
	if got := s.items(); !slices.Equal(got, []string{"create the directory here", "empty"}) {
		t.Fatalf("filtered: %q", got)
	}
	keys(m, "backspace", "left")
	if s.dir != "" || s.items()[s.selected] != "a" {
		t.Fatalf("back up: %q on %q", s.dir, s.items()[s.selected])
	}
	keys(m, "left")
	if s.dir != "" {
		t.Fatalf("above the worktree: %q", s.dir)
	}
	keys(m, ".")
	if got := s.items(); !slices.Equal(got, []string{"create the directory here", ".hidden"}) {
		t.Fatalf("hidden ones on a dot: %q", got)
	}
	keys(m, "backspace", "down", "right", "enter")
	f, ok := m.openTop().(*fieldScreen)
	if !ok || !strings.Contains(f.prompt, "new directory, in main/a/") {
		t.Fatalf("the name asked: %#v", m.openTop())
	}
	if msg := f.submit("pkg"); msg != (createCodeEntry{worktree: "main", path: "a/pkg", directory: true}) {
		t.Errorf("submitted %#v", msg)
	}
	keys(m, "esc", "esc")
	if m.openTop() != nil {
		t.Error("two Esc left the popup open")
	}
}

func TestCodeEntryCreated(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	l := m.list()
	keys(m, newFileKey, "enter", "new.go", "enter")
	if f, ok := m.openTop().(*fieldScreen); !ok || !f.busy {
		t.Fatalf("not waiting for the answer: %#v", m.openTop())
	}
	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", err: errors.New(`"new.go" already exists`)})
	f, ok := m.openTop().(*fieldScreen)
	if !ok || f.busy || !strings.Contains(text(m), "already exists") {
		t.Fatalf("a failure closed the popup, or did not say:\n%s", text(m))
	}

	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", result: api.ResultCreated, path: "dir/sub/new.go"})
	if m.openTop() != nil {
		t.Fatal("the popup stayed")
	}
	files := l.code.files["main"]
	if files.read != 1 || !files.loading || !files.open["dir"] || !files.open["dir/sub"] {
		t.Errorf("the files not read again, or its directories not open: %+v", files)
	}
	if l.code.cursor != "dir/sub/new.go" || l.active != fileTab("main", "dir/sub/new.go") {
		t.Errorf("cursor %q, tab %q", l.code.cursor, l.active)
	}
	if !strings.Contains(statusRow(m), "created main/dir/sub/new.go") {
		t.Errorf("status %q", statusRow(m))
	}

	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", result: api.ResultCreated, path: "pkg", directory: true})
	if l.code.cursor != "pkg" || l.active != fileTab("main", "dir/sub/new.go") {
		t.Errorf("a directory: cursor %q, tab %q", l.code.cursor, l.active)
	}
}

// TestCodeCursorWaitsForTheNewRow: the cursor stays on what was created until a read
// shows it, through what comes first on a loaded machine: the stream's update, and
// the answer to a read asked before the change, which does not show it.
func TestCodeCursorWaitsForTheNewRow(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	l, f := m.list(), m.codeFilesShown()
	f.asked = false
	m.Update(nil) // a read asked before the change
	if !f.loading {
		t.Fatal("no read asked")
	}
	before := newCodeLoaded("proj-demo", "main", fileList{paths: f.paths}, nil)
	before.changes = f.changes

	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", result: api.ResultCreated, path: "pkg", directory: true})
	m.worktreesChanged() // the stream's update
	m.Update(before)
	if l.code.cursor != "pkg" {
		t.Fatalf("the cursor left what was created before it showed: %q", l.code.cursor)
	}
	if !f.loading {
		t.Fatal("the files not read again after the change")
	}

	after := newCodeLoaded("proj-demo", "main", fileList{paths: f.paths, emptyDirs: []string{"pkg"}}, nil)
	after.changes = f.changes
	m.Update(after)
	if r, ok := m.codeCursorRow(); !ok || r.key() != "pkg" || l.code.cursor != "pkg" {
		t.Errorf("the cursor is not on the new row: %q, row %d", l.code.cursor, l.code.row)
	}
	if l.code.awaiting != "" {
		t.Errorf("still awaiting %q", l.code.awaiting)
	}
}

func TestCodeEntryRenamed(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	l := m.list()
	keys(m, "enter") // its tab
	m.moveToCode(1)  // dir
	keys(m, renameKey)
	f, ok := m.openTop().(*fieldScreen)
	if !ok || f.value != "dir" {
		t.Fatalf("rename: %#v", m.openTop())
	}
	keys(m, "backspace", "backspace", "backspace", "lib", "enter")
	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", result: api.ResultRenamed, path: "lib", from: "dir"})
	if m.openTop() != nil || l.code.cursor != "lib" {
		t.Fatalf("popup %#v, cursor %q", m.openTop(), l.code.cursor)
	}
	if want := fileTab("main", "lib/a.go"); l.active != want || !slices.Contains(l.tabs, want) || slices.Contains(l.tabs, aFile) {
		t.Errorf("the file's tab did not follow: %q, %q", l.active, l.tabs)
	}
	if files := l.code.files["main"]; !files.open["lib"] || files.open["dir"] {
		t.Errorf("the fold did not follow: %v", files.open)
	}
}

func TestCodeEntryDeleted(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"dir/a.go", "dir/b.go", "dir/new.go", "top"}, api.WorktreeStatus{Name: "main", Path: "/r",
		Files: []api.WorktreeFile{changed("dir/b.go", api.WorktreeFileStatusM), changed("dir/new.go", api.WorktreeFileStatusEmpty), changed("top", api.WorktreeFileStatusEmpty)}})
	l := m.list()
	m.moveToCode(1)
	keys(m, "enter", "down", "enter") // dir/a.go's tab
	m.moveToCode(1)
	keys(m, deleteFileKey)
	if q := m.deleteQuestion("main", m.codeRowsShown()[1].node); q != "Delete main/dir/ and the 3 files in it? 2 of them untracked or changed: git cannot bring that back. Nothing goes to a trash." {
		t.Errorf("asked %q", q)
	}
	wantText(t, m, "Delete main/dir/", "keep it", "delete it")
	keys(m, "enter")
	if m.openTop() != nil || !slices.Contains(l.tabs, aFile) {
		t.Fatal("keep deleted, or left the popup")
	}
	keys(m, deleteFileKey, "down", "enter")
	if s, ok := m.openTop().(*choiceScreen); !ok || s.busy != "deleting…" {
		t.Fatalf("not deleting: %#v", m.openTop())
	}
	m.Update(codeEntryChanged{projectID: "proj-demo", worktree: "main", result: api.ResultRemoved, path: "dir"})
	if m.openTop() != nil || slices.Contains(l.tabs, aFile) || l.code.cursor != "" {
		t.Errorf("popup %#v, tabs %q, cursor %q", m.openTop(), l.tabs, l.code.cursor)
	}

	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "top" }))
	if q := m.deleteQuestion("main", m.codeRowsShown()[l.code.row].node); !strings.HasPrefix(q, "Delete main/top? It is untracked: git cannot bring it back.") {
		t.Errorf("asked %q", q)
	}
}

// TestCodeEntryWithAnEditor: what an editor has open, or holds a file it has open, is
// neither renamed nor deleted; the status line says why.
func TestCodeEntryWithAnEditor(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	withEditors(m, aEditor)
	for _, row := range []int{1, 2} { // dir, dir/a.go
		m.moveToCode(row)
		for _, k := range []string{renameKey, deleteFileKey} {
			keys(m, k)
			if m.openTop() != nil {
				t.Fatalf("%s on row %d opened a popup", k, row)
			}
			if !strings.Contains(statusRow(m), "close the editor of dir/a.go") {
				t.Errorf("%s on row %d: status %q", k, row, statusRow(m))
			}
		}
	}
	m.moveToCode(3) // top
	keys(m, renameKey)
	if m.openTop() == nil {
		t.Error("another file's rename refused")
	}
}

// TestCodeEntriesLive creates, renames and deletes a directory through a real
// daemon, which lists it while it is empty.
func TestCodeEntriesLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "tracked"), "x\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the files", func(m *Model) bool { return strings.Contains(text(m), "tracked") })
	m := p.m
	m.layout.setFocus(project)
	m.moveToCode(0)
	named := func(m *Model) bool { _, ok := m.openTop().(*fieldScreen); return ok }
	p.keys(newDirKey, "enter")
	p.until(t, "the name asked", named)
	if !strings.Contains(statusRow(m), "new directory: Esc goes back") {
		t.Errorf("status %q", statusRow(m))
	}
	p.keys("pkg", "enter")
	p.until(t, "the new directory, empty", func(m *Model) bool {
		return m.openTop() == nil && slices.ContainsFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "pkg" })
	})
	if info, err := os.Stat(filepath.Join(dir, "pkg")); err != nil || !info.IsDir() {
		t.Fatalf("pkg: %v, %v", info, err)
	}
	if m.list().code.cursor != "pkg" {
		t.Errorf("cursor %q", m.list().code.cursor)
	}

	p.keys(renameKey, "backspace", "backspace", "backspace", "lib", "enter")
	p.until(t, "the directory renamed", func(m *Model) bool {
		return m.openTop() == nil && slices.ContainsFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "lib" })
	})
	m.moveToCode(0) // at the top, where tracked is
	p.keys(newFileKey, "enter")
	p.until(t, "the name asked", named)
	p.keys("tracked", "enter")
	p.until(t, "a name taken refused", func(m *Model) bool { return strings.Contains(text(m), "already exists") })
	p.keys("esc")
	p.until(t, "the name field closed", func(m *Model) bool { _, ok := m.openTop().(*codeDirScreen); return ok })
	p.keys("esc")
	p.until(t, "the popup closed", func(m *Model) bool { return m.openTop() == nil })

	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "lib" }))
	p.keys(deleteFileKey, "down", "enter")
	p.until(t, "the directory deleted", func(m *Model) bool {
		return m.openTop() == nil && !slices.ContainsFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "lib" })
	})
	if _, err := os.Stat(filepath.Join(dir, "lib")); !os.IsNotExist(err) {
		t.Errorf("lib still there: %v", err)
	}
}
